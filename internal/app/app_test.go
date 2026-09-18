package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gtkaskpass-yubikey/internal/askpass"
	"gtkaskpass-yubikey/internal/cacheipc"
	"gtkaskpass-yubikey/internal/trace"
)

type fakeUI struct {
	result Result
	err    error
	called int
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCacheHitAndWriteFailure(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, hit := range []bool{true, false} {
		for _, broken := range []bool{true, false} {
			var output bytes.Buffer
			var w io.Writer = &output
			if broken {
				w = failingWriter{}
			}
			ui := &fakeUI{result: Result{Accepted: true, Remember: true, Value: "manual"}}
			commits := 0
			a := App{UI: ui, Out: w, Err: failingWriter{}, Getenv: func(k string) string {
				if k == "GTKASKPASS_TRACE" {
					return "secrets"
				}
				return ""
			},
				CacheCall: func(ctx context.Context, r cacheipc.Request) (cacheipc.Response, error) {
					if r.Op == "begin" {
						if hit {
							return cacheipc.Response{Secret: []byte("cached")}, nil
						}
						return cacheipc.Response{Token: 1, TTL: time.Hour}, nil
					}
					commits++
					return cacheipc.Response{}, errors.New("daemon disappeared")
				},
			}
			code := a.Run(context.Background(), []string{"Enter passphrase for " + key + ": "})
			if broken {
				if code != 2 || commits != 0 {
					t.Fatal(code, commits)
				}
			} else {
				if code != 0 {
					t.Fatal(code)
				}
				if !hit && commits != 1 {
					t.Fatal("missing commit")
				}
			}
			if hit && ui.called != 0 {
				t.Fatal("cache hit opened UI")
			}
		}
	}
}

func (f *fakeUI) Show(context.Context, askpass.Request, time.Duration, *trace.Logger) (Result, error) {
	f.called++
	return f.result, f.err
}

func TestContract(t *testing.T) {
	for _, tt := range []struct {
		hint   string
		result Result
		err    error
		code   int
		out    string
	}{
		{"", Result{Accepted: true, Value: " x "}, nil, 0, " x \n"},
		{"", Result{Accepted: true}, nil, 0, "\n"},
		{"confirm", Result{Accepted: true}, nil, 0, "yes\n"},
		{"confirm", Result{}, nil, 1, ""},
		{"", Result{}, nil, 1, ""},
		{"", Result{}, errors.New("display"), 2, ""},
		{"", Result{Accepted: true, Value: "bad\nvalue"}, nil, 2, ""},
	} {
		for _, mode := range []string{"off", "metadata", "secrets"} {
			var out, errout bytes.Buffer
			ui := &fakeUI{result: tt.result, err: tt.err}
			a := App{UI: ui, Out: &out, Err: &errout, Getenv: func(k string) string {
				if k == "SSH_ASKPASS_PROMPT" {
					return tt.hint
				}
				if k == "GTKASKPASS_TRACE" {
					return mode
				}
				return ""
			}}
			code := a.Run(context.Background(), []string{"prompt"})
			if code != tt.code || out.String() != tt.out {
				t.Fatal(tt, mode, code, out.String())
			}
			if mode == "metadata" && strings.Contains(errout.String(), " x ") {
				t.Fatal("secret leaked")
			}
		}
	}
}

func TestCancelledBeforeUI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, err bytes.Buffer
	ui := &fakeUI{result: Result{Accepted: true, Value: "secret"}}
	a := App{UI: ui, Out: &out, Err: &err, Getenv: func(string) string { return "" }}
	if code := a.Run(ctx, nil); code != 1 || out.Len() != 0 || ui.called != 0 {
		t.Fatal(code, ui.called)
	}
}
