// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/elafarge/gtkaskpass-yubikey/internal/cache"
	"github.com/elafarge/gtkaskpass-yubikey/internal/cacheipc"
	"github.com/elafarge/gtkaskpass-yubikey/internal/lifecycle"
	"github.com/elafarge/gtkaskpass-yubikey/internal/trace"
	"golang.org/x/sys/unix"
)

func main() {
	signal.Ignore(syscall.SIGPIPE)
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gtkaskpass-yubikey-cache:", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: gtkaskpass-yubikey-cache serve [--ttl 1h] [--trace] | forget (--all | --fingerprint SHA256:... | --key PATH --kind passphrase|pin)")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	switch args[0] {
	case "serve":
		ttl := f.Duration("ttl", time.Hour, "absolute credential lifetime; 0 disables caching")
		tracing := f.Bool("trace", false, "trace metadata to stderr")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *ttl < 0 || f.NArg() != 0 {
			return errors.New("invalid TTL or extra arguments")
		}
		if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
			return err
		}
		unix.Umask(0077)
		mode := "off"
		if *tracing {
			mode = "metadata"
		}
		log, _ := trace.New(mode, os.Stderr)
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		defer stop()
		l, err := cacheipc.Listen()
		if err != nil {
			return err
		}
		return cacheipc.Serve(ctx, l, cache.New(*ttl, lifecycle.BootTime), log)
	case "forget":
		all := f.Bool("all", false, "forget every key")
		path := f.String("key", "", "key-file path")
		kind := f.String("kind", "", "passphrase or pin")
		fingerprint := f.String("fingerprint", "", "SHA256 fingerprint of an agent's FIDO key (PIN only)")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 {
			return errors.New("unexpected arguments")
		}
		req := cacheipc.Request{Op: "forget-all"}
		if *all {
			if *path != "" || *kind != "" || *fingerprint != "" {
				return errors.New("--all cannot be combined with --key/--kind")
			}
		} else if *fingerprint != "" {
			k := cache.Key{Kind: "pin", Fingerprint: *fingerprint}
			if *path != "" || *kind != "" || !k.AgentPIN() {
				return errors.New("--fingerprint requires a canonical SHA256 fingerprint and cannot be combined with --key/--kind")
			}
			req.Op, req.Key = "forget", k
		} else {
			k := cache.Key{Path: *path, Kind: *kind}
			if !k.Valid() {
				return errors.New("specify --all or --key PATH --kind passphrase|pin")
			}
			p, err := filepath.Abs(k.Path)
			if err != nil {
				return err
			}
			k.Path = p
			req.Op, req.Key = "forget", k
		}
		_, err := cacheipc.Call(context.Background(), req)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil
		}
		return err
	default:
		return errors.New("unknown subcommand")
	}
}
