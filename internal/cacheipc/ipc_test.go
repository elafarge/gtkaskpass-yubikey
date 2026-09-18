package cacheipc

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elafarge/ssh-askpass-fido/internal/cache"
	"github.com/elafarge/ssh-askpass-fido/internal/lifecycle"
)

func TestFrames(t *testing.T) {
	var b bytes.Buffer
	in := Request{Version: 1, Op: "commit", Secret: []byte("test\x00value")}
	if err := writeFrame(&b, in); err != nil {
		t.Fatal(err)
	}
	var out Request
	if err := readFrame(&b, &out); err != nil || !bytes.Equal(in.Secret, out.Secret) {
		t.Fatal(out, err)
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], maxFrame+1)
	if err := readFrame(bytes.NewReader(h[:]), &out); err == nil {
		t.Fatal("oversize accepted")
	}
	if err := readFrame(bytes.NewReader([]byte{0, 0, 0, 4, '{'}), &out); err == nil {
		t.Fatal("truncation accepted")
	}
}

func TestServerLifecycle(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", root)
	l, err := Listen()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	s := cache.New(time.Hour, lifecycle.BootTime)
	go func() { done <- Serve(ctx, l, s, nil) }()
	p := filepath.Join(root, "key")
	if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	k := cache.Key{Path: p, Kind: "passphrase"}
	r, err := Call(ctx, Request{Op: "begin", Key: k})
	if err != nil || r.Token == 0 {
		t.Fatal(r, err)
	}
	r, err = Call(ctx, Request{Op: "commit", Key: k, Token: r.Token, Secret: []byte("answer")})
	if err != nil || r.Reason != "stored" {
		t.Fatal(r, err)
	}
	r, err = Call(ctx, Request{Op: "begin", Key: k})
	if err != nil || r.Reason != "retry" || len(r.Secret) != 0 {
		t.Fatal(r, err)
	}
	if _, err := Call(ctx, Request{Op: "unknown"}); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if _, err := Listen(); err == nil {
		t.Fatal("second daemon bound")
	}
	path, _ := SocketPath(false)
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := Call(ctx, Request{Op: "forget-all"}); err == nil {
		t.Fatal("insecure socket accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	// A stalled client must not hold up daemon termination beyond the deadline.
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeQuietly(c)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown blocked")
	}
}

func TestPrivateRuntimeDirectory(t *testing.T) {
	r := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", r)
	if err := os.Chmod(r, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := SocketPath(true); err == nil {
		t.Fatal("public runtime directory accepted")
	}
	if err := os.Chmod(r, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(r, "ssh-askpass-fido")); err != nil {
		t.Fatal(err)
	}
	if _, err := SocketPath(true); err == nil {
		t.Fatal("symlink accepted")
	}
}
