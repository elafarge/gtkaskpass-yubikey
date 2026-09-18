package app

import (
	"bytes"
	"context"
	"net"
	"os"
	"testing"

	"github.com/elafarge/gtkaskpass-yubikey/internal/cacheipc"
)

func TestAdapterDelivery(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", root)
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	l, err := cacheipc.Listen()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	done := make(chan error, 1)
	go func() {
		c, e := l.AcceptUnix()
		if e != nil {
			done <- e
			return
		}
		defer func() { _ = c.Close() }()
		var r cacheipc.Request
		if e = cacheipc.ReadFrame(c, &r); e == nil {
			e = cacheipc.WriteFrame(c, cacheipc.Response{Version: 2, Event: "accepted"})
		}
		if e == nil {
			e = cacheipc.WriteFrame(c, cacheipc.Response{Version: 2, Event: "result", Secret: []byte(" exact "), SecretResponse: true})
		}
		if e == nil {
			e = cacheipc.ReadFrame(c, &r)
		}
		if e == nil && r.Op != "delivered" {
			e = net.ErrClosed
		}
		if e == nil {
			e = cacheipc.WriteFrame(c, cacheipc.Response{Version: 2, Event: "done"})
		}
		done <- e
	}()
	var out, diagnostics bytes.Buffer
	a := App{Out: &out, Err: &diagnostics, Getenv: os.Getenv}
	if code := a.Run(context.Background(), []string{"--literal prompt"}); code != 0 || out.String() != " exact \n" {
		t.Fatal(code, out.String(), diagnostics.String())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
