// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/elafarge/gtkaskpass-yubikey/internal/cacheipc"
	"github.com/elafarge/gtkaskpass-yubikey/internal/dialog"
	"golang.org/x/sys/unix"
)

// Only display/session routing and locale values cross the service boundary.
var SessionKeys = []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "LANG", "LC_ALL", "LC_CTYPE", "XDG_ACTIVATION_TOKEN", "DESKTOP_STARTUP_ID"}

type UI interface {
	Show(dialog.View) error
	Action(context.Context) (dialog.Action, error)
	Close()
	Cancelled() bool
}
type Worker struct {
	conn      net.Conn
	cmd       *exec.Cmd
	stop      func() bool
	done      chan struct{}
	actions   chan dialog.Action
	cancelled atomic.Bool
}

func StartWorker(ctx context.Context, path string, env map[string]string) (UI, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	a, b := os.NewFile(uintptr(fds[0]), "ui-parent"), os.NewFile(uintptr(fds[1]), "ui-child")
	defer func() { _ = a.Close(); _ = b.Close() }()
	c, err := net.FileConn(a)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.ExtraFiles = []*os.File{b}
	cmd.Env = []string{"PATH=/nonexistent", "HOME=" + os.Getenv("HOME")}
	// Service-owned rendering settings, never caller-supplied injection variables.
	for _, k := range []string{"GSK_RENDERER", "GDK_BACKEND", "GTK_A11Y"} {
		if v := os.Getenv(k); v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	for _, k := range SessionKeys {
		if v := env[k]; v != "" {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	// Packaged worker wrapper supplies its own GTK libraries, schemas and icons.
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = c.Close()
		return nil, err
	}
	w := &Worker{conn: c, cmd: cmd, done: make(chan struct{}), actions: make(chan dialog.Action, 1)}
	w.stop = context.AfterFunc(ctx, func() { _ = c.Close() })
	go func() { defer close(w.done); _ = cmd.Wait(); _ = c.Close() }()
	go func() {
		defer close(w.actions)
		for {
			var v dialog.Action
			if cacheipc.ReadFrame(c, &v) != nil {
				return
			}
			if v.Kind == "cancel" {
				w.cancelled.Store(true)
			}
			select {
			case w.actions <- v:
			case <-ctx.Done():
				return
			case <-w.done:
				return
			}
		}
	}()
	return w, nil
}

func (w *Worker) Cancelled() bool { return w.cancelled.Load() }
func (w *Worker) Show(v dialog.View) error {
	if err := w.conn.SetWriteDeadline(time.Now().Add(cacheipc.Timeout)); err != nil {
		return err
	}
	return cacheipc.WriteFrame(w.conn, v)
}
func (w *Worker) Action(ctx context.Context) (dialog.Action, error) {
	select {
	case a, ok := <-w.actions:
		if !ok {
			return a, errors.New("UI disconnected")
		}
		return a, nil
	case <-ctx.Done():
		return dialog.Action{}, ctx.Err()
	}
}
func (w *Worker) Close() { w.stop(); _ = w.conn.Close(); _ = w.cmd.Process.Kill(); <-w.done }
