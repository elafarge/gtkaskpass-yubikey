// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elafarge/gtkaskpass-yubikey/internal/askpass"
	"github.com/elafarge/gtkaskpass-yubikey/internal/cache"
	"github.com/elafarge/gtkaskpass-yubikey/internal/cacheipc"
	"github.com/elafarge/gtkaskpass-yubikey/internal/device"
	"github.com/elafarge/gtkaskpass-yubikey/internal/dialog"
	"github.com/elafarge/gtkaskpass-yubikey/internal/preferences"
)

type Service struct {
	Cache       *cache.Store
	Devices     device.Backend
	Preferences *preferences.Store
	WorkerPath  string
	VerifyPIN   bool
	NewUI       func(context.Context, map[string]string) (UI, error)
	// Global hardware serialization is deliberately conservative. It prevents
	// concurrent requests from trying the same rejected cached PIN repeatedly.
	hardware chan struct{}
	once     sync.Once
}

func (s *Service) Handle(parent context.Context, c *net.UnixConn, in cacheipc.Request, caller string) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	acks := make(chan cacheipc.Request, 1)
	var ui UI
	go func() {
		defer cancel()
		var ack cacheipc.Request
		if cacheipc.ReadFrame(c, &ack) == nil {
			acks <- ack
			<-ctx.Done()
		}
	}()
	send := func(r cacheipc.Response) bool {
		if r.Event == "result" && ui != nil {
			ui.Close()
			ui = nil
		}
		r.Version = 2
		if err := c.SetWriteDeadline(time.Now().Add(cacheipc.Timeout)); err != nil {
			return false
		}
		return cacheipc.WriteFrame(c, r) == nil
	}
	finish := func(code int) { _ = send(cacheipc.Response{Event: "result", Code: code}) }
	req, err := askpass.Parse(in.Args, in.Hint)
	if err != nil {
		finish(2)
		return
	}
	if !send(cacheipc.Response{Event: "accepted", Notify: req.Mode == askpass.Notify}) {
		return
	}
	// Verify caller-supplied runtime is the current user's runtime, not a route
	// to another user's desktop. No service environment is overwritten.
	if v := in.Env["XDG_RUNTIME_DIR"]; v != "" && v != os.Getenv("XDG_RUNTIME_DIR") {
		finish(2)
		return
	}
	defer func() {
		if ui != nil {
			ui.Close()
		}
	}()
	show := func(v dialog.View) error {
		if ui == nil {
			if s.NewUI != nil {
				ui, err = s.NewUI(ctx, in.Env)
			} else {
				ui, err = StartWorker(ctx, s.WorkerPath, in.Env)
			}
			if err != nil {
				return err
			}
		}
		return ui.Show(v)
	}
	view := dialog.View{Request: req, Stage: "input", Selected: -1}
	action := func() (dialog.Action, error) {
		for {
			a, e := ui.Action(ctx)
			if e != nil || a.Kind != "shown" {
				return a, e
			}
			if in.Trace {
				_ = send(cacheipc.Response{Event: "window-shown"})
			}
		}
	}
	if req.Mode != askpass.Input {
		if err := show(view); err != nil {
			finish(2)
			return
		}
		if req.Mode == askpass.Notify {
			_, _ = action()
			return
		}
		a, err := action()
		if err != nil {
			return
		}
		if req.Mode == askpass.Notify {
			<-ctx.Done()
			return
		}
		if a.Kind != "submit" {
			finish(1)
			return
		}
		_ = send(cacheipc.Response{Event: "result", Secret: []byte("yes"), Code: 0})
		return
	}
	k, eligible := req.CacheKey()
	key := cache.Key{Path: k.Path, Kind: k.Kind, Fingerprint: k.Fingerprint}
	// Resolve relative filenames in the originating helper's working directory.
	if key.Path != "" && !filepath.IsAbs(key.Path) {
		pid, _, _ := strings.Cut(caller, ":")
		cwd, e := os.Readlink("/proc/" + pid + "/cwd")
		if e != nil {
			eligible = false
		} else {
			key.Path = filepath.Join(cwd, key.Path)
		}
	}
	id, resolveErr := cache.Resolve(key)
	eligible = eligible && resolveErr == nil && !in.NoCache
	verify := s.VerifyPIN && req.Title == "Security key PIN"
	var selected device.Device
	var candidate cache.Lookup
	var value []byte
	remember := false
	baseStamp := id.Stamp
	defer func() { clear(value) }()
	if verify {
		s.once.Do(func() { s.hardware = make(chan struct{}, 1) })
		select {
		case s.hardware <- struct{}{}:
			defer func() { <-s.hardware }()
		case <-ctx.Done():
			return
		}
	}
selectDevice:
	id.Stamp = baseStamp
	clear(value)
	value = nil
	remember = false
	if verify {
		for {
			devices, e := s.Devices.Discover(ctx)
			if e != nil || len(devices) == 0 {
				view.Stage = "wait"
				view.Message = "Connect an accessible FIDO2 security key with a configured PIN, then refresh."
				if show(view) != nil {
					finish(2)
					return
				}
				a, e := action()
				if e != nil {
					return
				}
				if a.Kind == "cancel" {
					finish(1)
					return
				}
				continue
			}
			if len(devices) == 1 {
				selected = devices[0]
				break
			}
			view.Stage = "choose"
			view.Devices = devices
			view.Selected = -1
			view.Message = "Select the security key for this SSH key. Confirm the selection before PIN verification."
			if s.Preferences != nil {
				preferred := s.Preferences.Preferred(k.Fingerprint)
				matches := 0
				for i, d := range devices {
					if preferred != "" && d.StableID == preferred {
						view.Selected = i
						matches++
					}
				}
				if matches != 1 {
					view.Selected = -1
				}
			}
			if show(view) != nil {
				finish(2)
				return
			}
			a, e := action()
			if e != nil {
				return
			}
			if a.Kind == "cancel" {
				finish(1)
				return
			}
			if a.Kind != "select" || a.Selected < 0 || a.Selected >= len(devices) {
				continue
			}
			selected = devices[a.Selected]
			break
		}
		if selected.Connection == "" {
			view.Stage = "error"
			view.Message = "Cannot reliably identify this device connection. PIN verification is unavailable."
			_ = show(view)
			if ui != nil {
				_, _ = action()
			}
			finish(1)
			return
		}
		id.Stamp += "\x00device:" + selected.Connection
	}
	candidate = cache.Lookup{}
	if eligible {
		candidate = s.Cache.Begin(id, caller)
	}
	if in.Trace {
		_ = send(cacheipc.Response{Event: "cache", Reason: candidate.Reason})
	}
	value = candidate.Secret
	view.Stage = "input"
	view.TTL = candidate.TTL
	view.Devices = nil
	view.Selected = -1
	view.ChangeDevice = verify
	if verify {
		view.Message = "Device: " + selected.Label
		if selected.Retries >= 0 {
			view.Message += "\nPIN attempts remaining: " + strconv.Itoa(selected.Retries)
		}
	}
	for {
		if ctx.Err() != nil {
			return
		}
		if len(value) == 0 {
			if show(view) != nil {
				finish(2)
				return
			}
			a, e := action()
			if e != nil {
				return
			}
			if a.Kind == "change" && verify {
				goto selectDevice
			}
			if a.Kind != "submit" {
				finish(1)
				return
			}
			if askpass.Validate(a.Value) != nil {
				view.Message = "Invalid response format."
				continue
			}
			value = []byte(a.Value)
			remember = a.Remember
		}
		if !verify {
			break
		}
		if candidate.Generation != 0 && !s.Cache.Current(id, candidate.Generation) {
			clear(value)
			value = nil
			candidate = s.Cache.Begin(id, caller)
			view.TTL = candidate.TTL
			continue
		}
		// Re-enumerate before each attempted PIN; device replacement never reuses
		// a previous connection's credential, even if its hidraw path is reused.
		devices, e := s.Devices.Discover(ctx)
		present := false
		if e == nil {
			for _, d := range devices {
				if d.Connection == selected.Connection && d.Path == selected.Path {
					present = true
				}
			}
		}
		if !present {
			if candidate.Generation != 0 {
				s.Cache.Reject(id, candidate.Generation)
			}
			clear(value)
			value = nil
			view.Stage = "error"
			view.Message = "Selected device disconnected or changed. Cancel and start a new request."
			_ = show(view)
			if ui != nil {
				_, _ = action()
			}
			finish(1)
			return
		}
		if ui != nil {
			busy := view
			busy.Stage = "busy"
			busy.Message = "Verifying PIN…"
			if show(busy) != nil {
				return
			}
		}
		result := s.Devices.Verify(ctx, selected, value)
		if ui != nil && ui.Cancelled() {
			finish(1)
			return
		}
		if ctx.Err() != nil {
			return
		}
		if result.Status == device.Accepted {
			if candidate.Generation != 0 && !s.Cache.Current(id, candidate.Generation) {
				clear(value)
				value = nil
				candidate = s.Cache.Begin(id, caller)
				view.TTL = candidate.TTL
				continue
			}
			break
		}
		clear(value)
		value = nil
		if result.Status == device.Invalid || result.Status == device.Blocked || result.Status == device.TemporaryBlock {
			if candidate.Generation != 0 {
				s.Cache.Reject(id, candidate.Generation)
			}
			if eligible {
				candidate = s.Cache.Begin(id, caller)
				view.TTL = candidate.TTL
			}
		}
		view.Message = result.Message
		if result.Retries >= 0 {
			view.Message += "\nPIN attempts remaining: " + strconv.Itoa(result.Retries)
		}
		if result.Status != device.Invalid {
			view.Stage = "error"
			if show(view) != nil {
				finish(2)
				return
			}
			_, _ = action()
			finish(1)
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	if ui != nil && ui.Cancelled() {
		finish(1)
		return
	}
	if ui != nil {
		_ = ui.Show(dialog.View{Stage: "close"})
	}
	if !send(cacheipc.Response{Event: "result", Secret: value, SecretResponse: true}) {
		return
	}
	select {
	case ack := <-acks:
		if ack.Op != "delivered" || ack.Version != 2 {
			return
		}
	case <-ctx.Done():
		return
	case <-time.After(cacheipc.Timeout):
		return
	}
	savePreference := verify && (candidate.Reason == "disabled" || !eligible || !remember)
	if candidate.Token != 0 {
		stored := false
		if remember && len(value) > 0 {
			// Recheck file metadata at store time; never resurrect a changed key.
			current, e := cache.Resolve(key)
			if e == nil {
				if verify {
					current.Stamp += "\x00device:" + selected.Connection
				}
				if current == id {
					stored = s.Cache.Commit(candidate.Token, id, caller, value)
				}
			}
		} else if !remember {
			s.Cache.Forget(&id.Key)
		}
		if in.Trace {
			reason := "stale"
			if stored {
				reason = "stored"
			}
			_ = send(cacheipc.Response{Event: "cache-update", Reason: reason})
		}
		savePreference = verify && (stored || !remember)
	}
	if savePreference && s.Preferences != nil {
		if e := s.Preferences.Save(k.Fingerprint, selected.StableID); e != nil {
			_, _ = fmt.Fprintln(os.Stderr, "gtkaskpass: could not save device preference")
		}
	}
	_ = send(cacheipc.Response{Event: "done"})
}
