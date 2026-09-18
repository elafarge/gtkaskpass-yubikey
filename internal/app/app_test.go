package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gtkaskpass-yubikey/internal/askpass"
	"gtkaskpass-yubikey/internal/trace"
)

type fakeUI struct {
	result Result
	err    error
	called int
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
