// SPDX-License-Identifier: Apache-2.0
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/elafarge/ssh-askpass-fido/internal/askpass"
	"github.com/elafarge/ssh-askpass-fido/internal/cacheipc"
	"github.com/elafarge/ssh-askpass-fido/internal/lifecycle"
	"github.com/elafarge/ssh-askpass-fido/internal/service"
	"github.com/elafarge/ssh-askpass-fido/internal/trace"
)

type App struct {
	Out, Err io.Writer
	Getenv   func(string) string
}

func (a App) Run(parent context.Context, args []string) (code int) {
	log, err := trace.New(a.Getenv("SSH_ASKPASS_FIDO_TRACE"), a.Err)
	if err != nil {
		_, _ = fmt.Fprintln(a.Err, err)
		return 2
	}
	defer func() { log.Event("exit", "status", code) }()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if p, err := lifecycle.ReadProcess(os.Getppid()); err == nil {
		go func() { lifecycle.WaitParent(ctx, p); cancel() }()
	}
	if ctx.Err() != nil {
		return 1
	}
	c, err := cacheipc.Dial(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(a.Err, "ssh-askpass-fido: request service unavailable; start ssh-askpass-fido.service in your graphical session")
		return 2
	}
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	env := map[string]string{}
	for _, k := range service.SessionKeys {
		env[k] = a.Getenv(k)
	}
	if err := c.SetDeadline(time.Now().Add(cacheipc.Timeout)); err != nil {
		return 2
	}
	in := cacheipc.Request{Version: 2, Op: "ask", Args: args, Hint: a.Getenv("SSH_ASKPASS_PROMPT"), Env: env, NoCache: a.Getenv("SSH_ASKPASS_FIDO_CACHE") == "off", Trace: a.Getenv("SSH_ASKPASS_FIDO_TRACE") != "" && a.Getenv("SSH_ASKPASS_FIDO_TRACE") != "off"}
	log.Event("input", "argv", fmt.Sprintf("%q", args), "hint", in.Hint)
	if cacheipc.WriteFrame(c, in) != nil {
		return 2
	}
	notify := false
	for {
		var r cacheipc.Response
		if err := cacheipc.ReadFrame(c, &r); err != nil {
			if ctx.Err() != nil {
				if notify {
					return 0
				}
				return 1
			}
			_, _ = fmt.Fprintln(a.Err, "ssh-askpass-fido: request service disconnected")
			return 2
		}
		if r.Version != 2 {
			clear(r.Secret)
			return 2
		}
		switch r.Event {
		case "accepted":
			notify = r.Notify
			if err := c.SetDeadline(time.Time{}); err != nil {
				return 2
			}
		case "result":
			defer clear(r.Secret)
			if r.Code != 0 {
				return r.Code
			}
			if ctx.Err() != nil {
				return 1
			}
			value := string(r.Secret)
			log.Response(value, r.SecretResponse)
			n, err := askpass.WriteResponse(a.Out, value)
			log.Event("stdout-write", "written", n, "success", err == nil)
			if err != nil {
				return 2
			}
			if !r.SecretResponse {
				return 0
			}
			if c.SetDeadline(time.Now().Add(cacheipc.Timeout)) != nil {
				return 0
			}
			if cacheipc.WriteFrame(c, cacheipc.Request{Version: 2, Op: "delivered"}) != nil {
				return 0
			}
			for {
				var done cacheipc.Response
				if cacheipc.ReadFrame(c, &done) != nil {
					return 0
				}
				log.Event(done.Event, "reason", done.Reason)
				clear(done.Secret)
				if done.Event == "done" {
					return 0
				}
			}
		default:
			log.Event(r.Event, "reason", r.Reason)
		}
	}
}
