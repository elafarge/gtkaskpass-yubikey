// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elafarge/ssh-askpass-fido/internal/askpass"
	"github.com/elafarge/ssh-askpass-fido/internal/dialog"
	"github.com/elafarge/ssh-askpass-fido/internal/touch"
)

type desiredTouch struct {
	device touch.Device
	epoch  uint64
}
type popup struct {
	epoch  uint64
	cancel context.CancelFunc
	done   chan struct{}
}

// TouchPopups owns session-scoped device popups, independently of askpass
// adapters. The latest-state mailbox is bounded and cannot lose a close event.
type TouchPopups struct {
	WorkerPath string
	Env        map[string]string
	NewUI      func(context.Context, map[string]string) (UI, error)
	mu         sync.Mutex
	wake       chan struct{}
	desired    map[string]desiredTouch
	epoch      uint64
	failed     atomic.Bool
}

func NewTouchPopups(path string, env map[string]string) *TouchPopups {
	copyEnv := map[string]string{}
	for _, k := range SessionKeys {
		if v := env[k]; v != "" {
			copyEnv[k] = v
		}
	}
	// Never reuse an activation token from another operation for passive popups.
	delete(copyEnv, "XDG_ACTIVATION_TOKEN")
	delete(copyEnv, "DESKTOP_STARTUP_ID")
	return &TouchPopups{WorkerPath: path, Env: copyEnv, wake: make(chan struct{}, 1), desired: map[string]desiredTouch{}}
}
func (p *TouchPopups) Available() bool {
	return (p.Env["DISPLAY"] != "" || p.Env["WAYLAND_DISPLAY"] != "") && !p.failed.Load()
}
func (p *TouchPopups) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *TouchPopups) SetTouch(d touch.Device, active bool) {
	p.mu.Lock()
	if active {
		if _, exists := p.desired[d.Path]; !exists && len(p.desired) < touch.MaxDevices {
			p.epoch++
			p.desired[d.Path] = desiredTouch{d, p.epoch}
		}
	} else {
		delete(p.desired, d.Path)
	}
	p.mu.Unlock()
	p.signal()
}
func (p *TouchPopups) runPopup(ctx context.Context, d touch.Device) {
	// Avoid flashing a window for a touch supplied before GTK could map it.
	select {
	case <-ctx.Done():
		return
	case <-time.After(120 * time.Millisecond):
	}
	if p.Env["DISPLAY"] == "" && p.Env["WAYLAND_DISPLAY"] == "" {
		return
	}
	var ui UI
	var err error
	if p.NewUI != nil {
		ui, err = p.NewUI(ctx, p.Env)
	} else {
		ui, err = StartWorker(ctx, p.WorkerPath, p.Env)
	}
	if err != nil {
		p.failed.Store(true)
		_, _ = fmt.Fprintln(os.Stderr, "ssh-askpass-fido: touch popup could not start")
		return
	}
	defer ui.Close()
	view := dialog.View{Request: askpass.Request{Mode: askpass.Notify, Title: "Touch your security key", Prompt: touch.CleanLabel(d.Label)}, PassiveTouch: true, Message: "Touch this device to continue."}
	if err = ui.Show(view); err != nil {
		p.failed.Store(true)
		return
	}
	for {
		a, err := ui.Action(ctx)
		if err != nil {
			if ctx.Err() == nil {
				p.failed.Store(true)
				_, _ = fmt.Fprintln(os.Stderr, "ssh-askpass-fido: touch popup disconnected")
			}
			return
		}
		if a.Kind == "shown" {
			p.failed.Store(false)
		}
		// Dismissal only hides this worker. Further keepalives do not recreate it.
	}
}

func (p *TouchPopups) Run(ctx context.Context) {
	popups := map[string]*popup{}
	// Finished epochs stay suppressed until a new device pending episode starts.
	finished := map[string]uint64{}
	defer func() {
		for _, r := range popups {
			r.cancel()
		}
		for _, r := range popups {
			<-r.done
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}
		p.mu.Lock()
		desired := map[string]desiredTouch{}
		for k, v := range p.desired {
			desired[k] = v
		}
		p.mu.Unlock()
		for path, r := range popups {
			want, ok := desired[path]
			if !ok || want.epoch != r.epoch {
				r.cancel()
			}
			select {
			case <-r.done:
				finished[path] = r.epoch
				delete(popups, path)
			default:
			}
		}
		for path := range finished {
			if _, ok := desired[path]; !ok {
				delete(finished, path)
			}
		}
		for path, want := range desired {
			if popups[path] != nil || finished[path] == want.epoch || len(popups) >= touch.MaxDevices {
				continue
			}
			child, cancel := context.WithCancel(ctx)
			r := &popup{epoch: want.epoch, cancel: cancel, done: make(chan struct{})}
			popups[path] = r
			go func() { defer func() { close(r.done); p.signal() }(); p.runPopup(child, want.device) }()
		}
	}
}
