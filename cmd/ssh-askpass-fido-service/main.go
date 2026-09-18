// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/elafarge/ssh-askpass-fido/internal/cache"
	"github.com/elafarge/ssh-askpass-fido/internal/cacheipc"
	"github.com/elafarge/ssh-askpass-fido/internal/config"
	"github.com/elafarge/ssh-askpass-fido/internal/fido"
	"github.com/elafarge/ssh-askpass-fido/internal/lifecycle"
	"github.com/elafarge/ssh-askpass-fido/internal/preferences"
	"github.com/elafarge/ssh-askpass-fido/internal/service"
	"github.com/elafarge/ssh-askpass-fido/internal/touch"
	"github.com/elafarge/ssh-askpass-fido/internal/trace"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

func main() {
	signal.Ignore(syscall.SIGPIPE)
	root := newCommand(os.Stdout, os.Stderr, runService)
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ssh-askpass-fido-service:", err)
		os.Exit(2)
	}
}

func newCommand(out, diagnostics io.Writer, serve func(context.Context, config.Settings) error) *cobra.Command {
	root := &cobra.Command{Use: "ssh-askpass-fido-service", Short: "SSH askpass request service and FIDO2 touch monitor", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(out)
	root.SetErr(diagnostics)
	var path string
	root.PersistentFlags().StringVar(&path, "config", "", "service config file (default: $XDG_CONFIG_HOME/ssh-askpass-fido/config.{yaml,yml,toml,json})")
	load := func(cmd *cobra.Command) (config.Settings, error) {
		if cmd.Flags().Changed("config") && path == "" {
			return config.Settings{}, errors.New("--config requires a nonempty path")
		}
		dir := ""
		if path == "" {
			var err error
			dir, err = os.UserConfigDir()
			if err != nil {
				return config.Settings{}, err
			}
		}
		cfg, _, err := config.Load(path, dir, cmd.Flags())
		return cfg, err
	}
	server := &cobra.Command{Use: "serve", Short: "Run the session service in the foreground", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := load(cmd)
		if err != nil {
			return err
		}
		return serve(cmd.Context(), cfg)
	}}
	config.Flags(server.Flags())
	root.AddCommand(server)
	configuration := &cobra.Command{Use: "config", Short: "Validate or display effective nonsecret service settings"}
	for _, name := range []string{"check", "show"} {
		command := &cobra.Command{Use: name, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := load(cmd)
			if err != nil {
				return err
			}
			if cmd.Name() == "check" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Configuration is valid.")
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(cfg)
		}}
		config.Flags(command.Flags())
		configuration.AddCommand(command)
	}
	root.AddCommand(configuration)
	root.AddCommand(&cobra.Command{Use: "devices", Short: "List public FIDO2 device metadata (does not test PINs)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		devices, err := (fido.Backend{}).Discover(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(devices)
	}})
	var all bool
	var key, kind, fingerprint string
	forget := &cobra.Command{Use: "forget", Short: "Forget cached credentials without displaying them", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		req := cacheipc.Request{Op: "forget-all"}
		switch {
		case all:
			if key != "" || kind != "" || fingerprint != "" {
				return errors.New("--all cannot be combined with --key, --kind, or --fingerprint")
			}
		case fingerprint != "":
			k := cache.Key{Kind: "pin", Fingerprint: fingerprint}
			if key != "" || kind != "" || !k.AgentPIN() {
				return errors.New("--fingerprint requires a canonical SHA256 fingerprint and cannot be combined with --key/--kind")
			}
			req.Op, req.Key = "forget", k
		default:
			k := cache.Key{Path: key, Kind: kind}
			if !k.Valid() {
				return errors.New("specify --all, --fingerprint SHA256:..., or --key PATH --kind passphrase|pin")
			}
			p, err := filepath.Abs(k.Path)
			if err != nil {
				return err
			}
			k.Path = p
			req.Op, req.Key = "forget", k
		}
		_, err := cacheipc.Call(cmd.Context(), req)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil
		}
		return err
	}}
	forget.Flags().BoolVar(&all, "all", false, "forget every credential")
	forget.Flags().StringVar(&key, "key", "", "key-file path")
	forget.Flags().StringVar(&kind, "kind", "", "passphrase or pin")
	forget.Flags().StringVar(&fingerprint, "fingerprint", "", "SHA256 fingerprint of an agent FIDO key (PIN only)")
	root.AddCommand(forget)
	return root
}

func runService(parent context.Context, cfg config.Settings) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	ttl, err := time.ParseDuration(cfg.CacheTTL)
	if err != nil {
		return err
	}
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
		return err
	}
	unix.Umask(0077)
	mode := "off"
	if cfg.Trace {
		mode = "metadata"
	}
	log, _ := trace.New(mode, os.Stderr)
	ctx, stop := signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer stop()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	l, err := cacheipc.Listen()
	if err != nil {
		return err
	}
	store := cache.New(ttl, lifecycle.BootTime)
	svc := &service.Service{Cache: store, Devices: fido.Backend{}, VerifyPIN: cfg.PINVerification == "required", Preferences: preferences.New(filepath.Join(configDir, "ssh-askpass-fido", "devices.json")), WorkerPath: filepath.Join(filepath.Dir(executable), "ssh-askpass-fido-ui")}
	if cfg.TouchNotifications {
		env := map[string]string{}
		for _, k := range service.SessionKeys {
			env[k] = os.Getenv(k)
		}
		popups := service.NewTouchPopups(svc.WorkerPath, env)
		monitor := &touch.Monitor{Sink: popups, Diagnostic: func(message string) { _, _ = fmt.Fprintln(os.Stderr, "ssh-askpass-fido:", message) }}
		monitor.Event = func(d touch.Device, active bool) { log.Event("touch-state", "device", d.Path, "needed", active) }
		svc.TouchMonitored = func() bool { return monitor.Covered() && popups.Available() }
		monitorCtx, cancel := context.WithCancel(ctx)
		doneMonitor, donePopups := make(chan struct{}), make(chan struct{})
		go func() { defer close(donePopups); popups.Run(monitorCtx) }()
		go func() { defer close(doneMonitor); monitor.Run(monitorCtx) }()
		defer func() { cancel(); <-doneMonitor; <-donePopups }()
		if !popups.Available() {
			_, _ = fmt.Fprintln(os.Stderr, "ssh-askpass-fido: no graphical session environment for touch popups")
		}
	}
	return cacheipc.Serve(ctx, l, store, log, svc.Handle)
}
