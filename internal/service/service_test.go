package service

import (
	"context"
	"encoding/base64"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elafarge/ssh-askpass-fido/internal/cache"
	"github.com/elafarge/ssh-askpass-fido/internal/cacheipc"
	"github.com/elafarge/ssh-askpass-fido/internal/device"
	"github.com/elafarge/ssh-askpass-fido/internal/dialog"
	"github.com/elafarge/ssh-askpass-fido/internal/preferences"
)

type fakeDevices struct {
	list        []device.Device
	answers     []device.Result
	tried       []string
	afterVerify func()
}

func (f *fakeDevices) Discover(context.Context) ([]device.Device, error) { return f.list, nil }
func (f *fakeDevices) Verify(_ context.Context, d device.Device, p []byte) device.Result {
	f.tried = append(f.tried, d.Connection+":"+string(p))
	r := f.answers[0]
	f.answers = f.answers[1:]
	if f.afterVerify != nil {
		f.afterVerify()
	}
	return r
}

type fakeUI struct {
	mu        sync.Mutex
	views     []dialog.View
	actions   []dialog.Action
	cancelled bool
}

func (f *fakeUI) Show(v dialog.View) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.views = append(f.views, v)
	return nil
}
func (f *fakeUI) Action(context.Context) (dialog.Action, error) {
	a := f.actions[0]
	f.actions = f.actions[1:]
	return a, nil
}
func (f *fakeUI) Close()          {}
func (f *fakeUI) Cancelled() bool { return f.cancelled }

func request(t *testing.T, s *Service, prompt string) cacheipc.Response {
	t.Helper()
	p := filepath.Join(t.TempDir(), "socket")
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = l.Close() }()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := l.AcceptUnix()
		if e != nil {
			return
		}
		defer func() { _ = c.Close() }()
		s.Handle(context.Background(), c, cacheipc.Request{Args: []string{prompt}, Env: map[string]string{}}, "agent")
	}()
	c, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: p, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = c.Close() }()
	if e = c.SetDeadline(time.Now().Add(5 * time.Second)); e != nil {
		t.Fatal(e)
	}
	var r cacheipc.Response
	for {
		if e = cacheipc.ReadFrame(c, &r); e != nil {
			t.Fatal(e)
		}
		if r.Event == "result" {
			break
		}
	}
	if r.Code == 0 {
		if e = cacheipc.WriteFrame(c, cacheipc.Request{Version: 2, Op: "delivered"}); e != nil {
			t.Fatal(e)
		}
		for {
			var done cacheipc.Response
			if e = cacheipc.ReadFrame(c, &done); e != nil {
				t.Fatal(e)
			}
			if done.Event == "done" {
				break
			}
		}
	}
	_ = c.Close()
	<-done
	return r
}

func TestCancelDuringVerificationDoesNotReturnPIN(t *testing.T) {
	ui := &fakeUI{actions: []dialog.Action{{Kind: "submit", Value: "right", Remember: true}}}
	d := &fakeDevices{list: []device.Device{{Connection: "one"}}, answers: []device.Result{{Status: device.Accepted}}, afterVerify: func() { ui.cancelled = true }}
	s := &Service{Cache: cache.New(time.Hour, func() time.Duration { return 0 }), Devices: d, VerifyPIN: true, NewUI: func(context.Context, map[string]string) (UI, error) { return ui, nil }}
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	if r := request(t, s, "Enter PIN for ED25519-SK key "+fp+": "); r.Code != 1 || len(r.Secret) != 0 {
		t.Fatal("cancelled response escaped", r.Code)
	}
}

func TestBlockedPINStopsWithoutAutomaticRetry(t *testing.T) {
	ui := &fakeUI{actions: []dialog.Action{{Kind: "submit", Value: "test-pin", Remember: true}, {Kind: "cancel"}}}
	d := &fakeDevices{list: []device.Device{{Connection: "one"}}, answers: []device.Result{{Status: device.Blocked, Retries: 0, Message: "PIN blocked"}}}
	s := &Service{Cache: cache.New(time.Hour, func() time.Duration { return 0 }), Devices: d, VerifyPIN: true, NewUI: func(context.Context, map[string]string) (UI, error) { return ui, nil }}
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	r := request(t, s, "Enter PIN for ED25519-SK key "+fp+": ")
	if r.Code != 1 || len(r.Secret) > 0 || len(d.tried) != 1 {
		t.Fatal("blocked PIN retried or returned", r.Code, d.tried)
	}
}

func TestForgetDuringCachedVerificationSuppressesResult(t *testing.T) {
	ui := &fakeUI{actions: []dialog.Action{{Kind: "submit", Value: "test-pin", Remember: true}, {Kind: "cancel"}}}
	d := &fakeDevices{list: []device.Device{{Connection: "one"}}, answers: []device.Result{{Status: device.Accepted}, {Status: device.Accepted}}}
	s := &Service{Cache: cache.New(time.Hour, func() time.Duration { return 0 }), Devices: d, VerifyPIN: true, NewUI: func(context.Context, map[string]string) (UI, error) { return ui, nil }}
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	prompt := "Enter PIN for ED25519-SK key " + fp + ": "
	request(t, s, prompt)
	d.afterVerify = func() { s.Cache.Forget(nil) }
	r := request(t, s, prompt)
	if r.Code != 1 || len(r.Secret) > 0 {
		t.Fatal("forgotten credential returned", r.Code)
	}
}

func TestVerifiedCacheAndRejection(t *testing.T) {
	d := &fakeDevices{list: []device.Device{{Path: "/fake", Connection: "one", StableID: "serial-one", Label: "device"}}, answers: []device.Result{{Status: device.Invalid, Retries: 7, Message: "wrong"}, {Status: device.Accepted}, {Status: device.Accepted}, {Status: device.Invalid, Retries: 7}, {Status: device.Accepted}}}
	ui := &fakeUI{actions: []dialog.Action{{Kind: "submit", Value: "wrong", Remember: true}, {Kind: "submit", Value: "right", Remember: true}, {Kind: "submit", Value: "changed", Remember: true}}}
	s := &Service{Cache: cache.New(time.Hour, func() time.Duration { return 0 }), Devices: d, VerifyPIN: true, Preferences: preferences.New(filepath.Join(t.TempDir(), "prefs", "devices.json")), NewUI: func(context.Context, map[string]string) (UI, error) { return ui, nil }}
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	prompt := "Enter PIN and confirm user presence for ED25519-SK key " + fp + ": "
	if r := request(t, s, prompt); string(r.Secret) != "right" {
		t.Fatal("rejected PIN returned")
	}
	if r := request(t, s, prompt); string(r.Secret) != "right" {
		t.Fatal("cache miss")
	}
	if r := request(t, s, prompt); string(r.Secret) != "changed" {
		t.Fatal("stale PIN retained")
	}
	if len(d.tried) != 5 || d.tried[0] != "one:wrong" {
		t.Fatal(d.tried)
	}
	if s.Preferences.Preferred(fp) != "serial-one" {
		t.Fatal("no remembered device")
	}
}

func TestMultipleDeviceConfirmationAndBinding(t *testing.T) {
	d := &fakeDevices{list: []device.Device{{Connection: "one", StableID: "one"}, {Connection: "two", StableID: "two"}}, answers: []device.Result{{Status: device.Accepted}, {Status: device.Accepted}}}
	ui := &fakeUI{actions: []dialog.Action{{Kind: "select", Selected: 1}, {Kind: "submit", Value: "pin2", Remember: true}, {Kind: "select", Selected: 0}, {Kind: "submit", Value: "pin1", Remember: true}}}
	s := &Service{Cache: cache.New(time.Hour, func() time.Duration { return 0 }), Devices: d, VerifyPIN: true, NewUI: func(context.Context, map[string]string) (UI, error) { return ui, nil }}
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	p := "Enter PIN for ED25519-SK key " + fp + ": "
	request(t, s, p)
	request(t, s, p)
	if len(d.tried) != 2 || d.tried[0] != "two:pin2" || d.tried[1] != "one:pin1" {
		t.Fatal("PIN sent to wrong device", d.tried)
	}
}
