package askpass

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestModes(t *testing.T) {
	for _, tt := range []struct {
		prompt, hint string
		mode         Mode
		touch        bool
	}{
		{"Confirm\nuser presence for key ED25519-SK x", "", Notify, true},
		{"Confirm user presence for keyboard", "", Input, false},
		{"Confirm user presence for key x", "confirm", Confirm, true},
		{"Confirm user presence for key x", "unknown", Input, true},
		{"Enter PIN for authenticator:", "none", Notify, false},
		{"user@host password:", "", Input, false},
		{"--help", "", Input, false},
	} {
		r, err := Parse([]string{tt.prompt}, tt.hint)
		if err != nil || r.Mode != tt.mode || r.Touch != tt.touch || r.Prompt != tt.prompt {
			t.Fatalf("%+v: %+v %v", tt, r, err)
		}
	}
	if _, err := Parse([]string{"a", "b"}, ""); err == nil {
		t.Fatal("extra arguments accepted")
	}
	if r, _ := Parse(nil, ""); r.Prompt == "" {
		t.Fatal("no default")
	}
}

func TestResponse(t *testing.T) {
	for _, value := range []string{"", " secret  ", "é雪", strings.Repeat("é", 511)} {
		var b bytes.Buffer
		n, err := WriteResponse(&b, value)
		if err != nil || n != len(value)+1 || b.String() != value+"\n" {
			t.Fatal(n, err, b.String())
		}
	}
	for _, value := range []string{"a\nb", "\r", "\x00", "\xff", strings.Repeat("é", 512)} {
		var b bytes.Buffer
		if _, err := WriteResponse(&b, value); err == nil || b.Len() != 0 {
			t.Fatal("invalid response accepted")
		}
	}
	if n, err := WriteResponse(shortWriter{}, "secret"); n != 2 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(n, err)
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return 2, nil }

func TestCacheKey(t *testing.T) {
	for _, tt := range []struct{ prompt, path, kind string }{
		{"Enter passphrase for key '/a b': ", "/a b", "passphrase"},
		{"Enter passphrase for /a b: ", "/a b", "passphrase"},
		{"Bad passphrase, try again for /a b: ", "/a b", "passphrase"},
		{"Enter PIN for ED25519-SK key /a: ", "/a", "pin"},
		{"Enter PIN for ECDSA-SK key relative: ", "relative", "pin"},
	} {
		r, _ := Parse([]string{tt.prompt}, "")
		k, ok := r.CacheKey()
		if !ok || k.Path != tt.path || k.Kind != tt.kind {
			t.Fatal(tt, k, ok)
		}
	}
	for _, p := range []string{"Enter PIN for authenticator: ", "Enter passphrase for PKCS#11: ", "Enter passphrase for (stdin): ", "Enter passphrase for /a (will confirm each use): ", "Enter passphrase for key '" + strings.Repeat("a", 100) + "': ", "password:", "Enter passphrase for /a\nb: "} {
		r, _ := Parse([]string{p}, "")
		if k, ok := r.CacheKey(); ok {
			t.Fatal(p, k)
		}
	}
}
