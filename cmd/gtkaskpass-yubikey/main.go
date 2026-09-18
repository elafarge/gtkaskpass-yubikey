// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/elafarge/gtkaskpass-yubikey/internal/app"
	"github.com/elafarge/gtkaskpass-yubikey/internal/cacheipc"
	"github.com/elafarge/gtkaskpass-yubikey/internal/gtkui"
	"github.com/elafarge/gtkaskpass-yubikey/internal/trace"
)

func init() { runtime.LockOSThread() }

func main() {
	// Broken tracing pipes must not terminate the helper; stdout failures should
	// be reported through the normal nonzero exit path as well.
	signal.Ignore(syscall.SIGPIPE)
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	log, _ := trace.New(os.Getenv("GTKASKPASS_TRACE"), os.Stderr)
	go func() {
		select {
		case sig := <-signals:
			log.Event("signal", "name", sig.String())
			cancel()
		case <-ctx.Done():
		}
	}()
	a := app.App{UI: gtkui.UI{}, Out: os.Stdout, Err: os.Stderr, Getenv: os.Getenv, CacheCall: cacheipc.Call}
	code := a.Run(ctx, os.Args[1:])
	cancel()
	signal.Stop(signals)
	os.Exit(code)
}
