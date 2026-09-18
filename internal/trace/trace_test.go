package trace

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestRedaction(t *testing.T) {
	for _, mode := range []string{"", "off", "metadata", "secrets"} {
		var b bytes.Buffer
		l, err := New(mode, &b)
		if err != nil {
			t.Fatal(err)
		}
		l.Response("synthetic-secret", true)
		if strings.Contains(b.String(), "synthetic-secret") != (mode == "secrets") {
			t.Fatal(mode, b.String())
		}
		l.Event("input", "prompt", "hello\n\x1bworld")
		if strings.Contains(b.String(), "\x1b") || strings.Contains(b.String(), "hello\n") {
			t.Fatal("unescaped input", b.String())
		}
	}
	l, _ := New("secrets", brokenWriter{})
	l.Response("value", true) // logging failure must not propagate or panic
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken") }
