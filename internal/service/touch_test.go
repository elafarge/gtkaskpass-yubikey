package service

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elafarge/ssh-askpass-fido/internal/cacheipc"
	"github.com/elafarge/ssh-askpass-fido/internal/dialog"
	"github.com/elafarge/ssh-askpass-fido/internal/touch"
)

type touchUI struct {
	shown  chan dialog.View
	closed chan struct{}
	once   sync.Once
}

func TestMonitoredTouchKeepsAdapterButSuppressesDuplicate(t *testing.T) {
	for _, prompt := range []string{"Confirm user presence for key test", "Generic information"} {
		t.Run(prompt, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "socket")
			l, e := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = l.Close() }()
			shown := make(chan dialog.View, 1)
			s := &Service{TouchMonitored: func() bool { return true }, NewUI: func(context.Context, map[string]string) (UI, error) {
				return &touchUI{shown: shown, closed: make(chan struct{})}, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			defer func() { cancel(); <-done }()
			go func() {
				defer close(done)
				c, e := l.AcceptUnix()
				if e != nil {
					return
				}
				defer func() { _ = c.Close() }()
				s.Handle(ctx, c, cacheipc.Request{Hint: "none", Args: []string{prompt}}, "agent")
			}()
			c, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: p, Net: "unix"})
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = c.Close() }()
			if e = c.SetReadDeadline(time.Now().Add(2 * time.Second)); e != nil {
				t.Fatal(e)
			}
			var accepted cacheipc.Response
			if e = cacheipc.ReadFrame(c, &accepted); e != nil || !accepted.Notify {
				t.Fatal(accepted, e)
			}
			if prompt == "Generic information" {
				select {
				case <-shown:
				case <-time.After(time.Second):
					t.Fatal("generic notification suppressed")
				}
			} else {
				select {
				case <-shown:
					t.Fatal("duplicate touch popup")
				case <-time.After(150 * time.Millisecond):
				}
			}
			select {
			case <-done:
				t.Fatal("notification adapter ended before cancellation")
			default:
			}
		})
	}
}

func (u *touchUI) Show(v dialog.View) error { u.shown <- v; return nil }
func (u *touchUI) Action(ctx context.Context) (dialog.Action, error) {
	<-ctx.Done()
	return dialog.Action{}, ctx.Err()
}
func (u *touchUI) Cancelled() bool { return false }
func (u *touchUI) Close()          { u.once.Do(func() { close(u.closed) }) }

func TestTouchPopupIndependentLifecycle(t *testing.T) {
	p := NewTouchPopups("unused", map[string]string{"DISPLAY": ":test", "XDG_ACTIVATION_TOKEN": "must-not-forward", "LD_PRELOAD": "bad"})
	if p.Env["LD_PRELOAD"] != "" || p.Env["XDG_ACTIVATION_TOKEN"] != "" {
		t.Fatal("unsafe popup environment")
	}
	created := make(chan *touchUI, 4)
	p.NewUI = func(context.Context, map[string]string) (UI, error) {
		u := &touchUI{shown: make(chan dialog.View, 1), closed: make(chan struct{})}
		created <- u
		return u, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	defer func() { cancel(); <-done }()
	d := touch.Device{Path: "/dev/hidraw-test", Label: "Test FIDO"}
	p.SetTouch(d, true)
	var ui *touchUI
	select {
	case ui = <-created:
	case <-time.After(2 * time.Second):
		t.Fatal("no independent popup")
	}
	view := <-ui.shown
	if !view.PassiveTouch || view.Request.Prompt != "Test FIDO" {
		t.Fatal(view)
	}
	for i := 0; i < 100; i++ {
		p.SetTouch(d, true)
	}
	select {
	case <-created:
		t.Fatal("keepalives reopened popup")
	case <-time.After(150 * time.Millisecond):
	}
	p.SetTouch(d, false)
	select {
	case <-ui.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("completion did not close worker")
	}
	p.SetTouch(d, true)
	select {
	case <-created:
	case <-time.After(2 * time.Second):
		t.Fatal("new operation did not show popup")
	}
}

func TestTouchPopupQuickCompletion(t *testing.T) {
	p := NewTouchPopups("unused", map[string]string{"DISPLAY": ":test"})
	created := make(chan struct{}, 1)
	p.NewUI = func(ctx context.Context, env map[string]string) (UI, error) {
		created <- struct{}{}
		return &touchUI{shown: make(chan dialog.View, 1), closed: make(chan struct{})}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	d := touch.Device{Path: "/test"}
	p.SetTouch(d, true)
	p.SetTouch(d, false)
	select {
	case <-created:
		t.Fatal("completed operation flashed a popup")
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	<-done
}
