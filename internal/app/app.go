// SPDX-License-Identifier: Apache-2.0
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/elafarge/gtkaskpass-yubikey/internal/askpass"
	"github.com/elafarge/gtkaskpass-yubikey/internal/cache"
	"github.com/elafarge/gtkaskpass-yubikey/internal/cacheipc"
	"github.com/elafarge/gtkaskpass-yubikey/internal/lifecycle"
	"github.com/elafarge/gtkaskpass-yubikey/internal/trace"
)

type Result struct {
	Value    string
	Accepted bool
	Remember bool
}
type UI interface {
	Show(context.Context, askpass.Request, time.Duration, *trace.Logger) (Result, error)
}

type App struct {
	UI        UI
	Out, Err  io.Writer
	Getenv    func(string) string
	CacheCall func(context.Context, cacheipc.Request) (cacheipc.Response, error)
}

func (a App) Run(ctx context.Context, args []string) (code int) {
	log, err := trace.New(a.Getenv("GTKASKPASS_TRACE"), a.Err)
	if err != nil {
		a.diagnostic(err)
		return 2
	}
	defer func() { log.Event("exit", "status", code) }()
	hint := a.Getenv("SSH_ASKPASS_PROMPT")
	log.Event("input", "argv", fmt.Sprintf("%q", args), "hint", hint, "cache", a.Getenv("GTKASKPASS_CACHE"), "askpass_require", a.Getenv("SSH_ASKPASS_REQUIRE"))
	req, err := askpass.Parse(args, hint)
	if err != nil {
		a.diagnostic(err)
		return 2
	}
	log.Event("classified", "mode", req.Mode, "title", req.Title)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if req.Mode == askpass.Notify {
		p, err := lifecycle.ReadProcess(os.Getppid())
		if err != nil || p.PID <= 1 {
			log.Event("parent-exited")
			return 0
		}
		go func() {
			lifecycle.WaitParent(ctx, p)
			if ctx.Err() == nil {
				log.Event("parent-exited")
				cancel()
			}
		}()
	}
	var key cache.Key
	var token uint64
	var ttl time.Duration
	if k, ok := req.CacheKey(); ok && a.Getenv("GTKASKPASS_CACHE") != "off" && a.CacheCall != nil {
		if id, err := cache.Resolve(cache.Key{Path: k.Path, Kind: k.Kind}); err == nil {
			key = id.Key
			res, err := a.CacheCall(ctx, cacheipc.Request{Op: "begin", Key: key})
			if err != nil {
				a.diagnostic("gtkaskpass-yubikey: cache unavailable; using input dialog")
				log.Event("cache-fallback", "error", err.Error())
			} else {
				log.Event("cache", "path", key.Path, "kind", key.Kind, "reason", res.Reason, "token", res.Token)
				if len(res.Secret) > 0 {
					defer clear(res.Secret)
					return a.respond(ctx, req, string(res.Secret), log)
				}
				token, ttl = res.Token, res.TTL
			}
		} else {
			log.Event("cache-ineligible", "reason", "unresolved key file")
		}
	} else {
		log.Event("cache-bypass")
	}
	if ctx.Err() != nil {
		if req.Mode == askpass.Notify {
			return 0
		}
		return 1
	}
	r, err := a.UI.Show(ctx, req, ttl, log)
	if err != nil {
		a.diagnostic("gtkaskpass-yubikey:", err)
		return 2
	}
	if req.Mode == askpass.Notify {
		return 0
	}
	if !r.Accepted || ctx.Err() != nil {
		log.Event("cancel")
		return 1
	}
	if req.Mode == askpass.Confirm {
		r.Value = "yes"
	}
	code = a.respond(ctx, req, r.Value, log)
	if code == 0 && token != 0 && a.CacheCall != nil {
		var res cacheipc.Response
		var err error
		if !r.Remember {
			res, err = a.CacheCall(ctx, cacheipc.Request{Op: "forget", Key: key})
		} else if r.Value != "" {
			secret := []byte(r.Value)
			res, err = a.CacheCall(ctx, cacheipc.Request{Op: "commit", Key: key, Token: token, Secret: secret})
			clear(secret)
		}
		if err != nil {
			a.diagnostic("gtkaskpass-yubikey: response supplied but cache update failed")
		}
		log.Event("cache-update", "reason", res.Reason, "failed", err != nil)
	}
	return code
}

func (a App) respond(ctx context.Context, req askpass.Request, value string, log *trace.Logger) int {
	if ctx.Err() != nil {
		return 1
	}
	if err := askpass.Validate(value); err != nil {
		a.diagnostic(err)
		return 2
	}
	log.Response(value, req.Mode == askpass.Input)
	n, err := askpass.WriteResponse(a.Out, value)
	log.Event("stdout-write", "written", n, "expected", len(value)+1, "success", err == nil)
	if err != nil {
		a.diagnostic("gtkaskpass-yubikey: response write failed")
		return 2
	}
	return 0
}

func (a App) diagnostic(args ...any) {
	// stderr is best-effort; a broken diagnostic stream must not break SSH.
	_, _ = fmt.Fprintln(a.Err, args...)
}
