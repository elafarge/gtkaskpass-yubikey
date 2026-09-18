package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elafarge/gtkaskpass-yubikey/internal/config"
)

func TestCobraConfiguration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "settings.yaml")
	if e := os.WriteFile(path, []byte("cacheTTL: 3m\ntrace: true\ntouchNotifications: false\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"--config", path, "serve", "--ttl", "4m", "--trace=false"}, {"serve", "--config=" + path, "--ttl=4m", "--trace=false"}} {
		var out, errout bytes.Buffer
		called := false
		root := newCommand(&out, &errout, func(_ context.Context, cfg config.Settings) error {
			called = true
			if cfg.CacheTTL != "4m" || cfg.Trace || cfg.TouchNotifications {
				t.Fatal(cfg)
			}
			return nil
		})
		root.SetArgs(args)
		if e := root.Execute(); e != nil {
			t.Fatal(e)
		}
		if !called {
			t.Fatal("serve not called")
		}
	}
	for _, cmd := range []string{"show", "check"} {
		var out bytes.Buffer
		root := newCommand(&out, &out, func(context.Context, config.Settings) error { t.Fatal("config command started service"); return nil })
		root.SetArgs([]string{"config", cmd, "--config", path})
		if e := root.Execute(); e != nil {
			t.Fatal(e)
		}
		if cmd == "show" {
			var cfg config.Settings
			if e := json.Unmarshal(out.Bytes(), &cfg); e != nil || cfg.CacheTTL != "3m" || !cfg.Trace {
				t.Fatal(cfg, e)
			}
		}
	}
}
func TestCobraErrorsAndHelp(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, args := range [][]string{{"serve", "--config="}, {"--config=", "serve"}, {"serve", "extra"}, {"serve", "--ttl", "invalid"}, {"serve", "--trace=secrets"}, {"forget", "--all", "--key", "/key"}, {"devices", "extra"}, {"bogus"}} {
		var out bytes.Buffer
		root := newCommand(&out, &out, func(context.Context, config.Settings) error { t.Fatal("invalid config started service"); return nil })
		root.SetArgs(args)
		if e := root.Execute(); e == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
	var out bytes.Buffer
	root := newCommand(&out, &out, nil)
	root.SetArgs([]string{"serve", "--help", "--config=/missing.yaml"})
	if e := root.Execute(); e != nil || !strings.Contains(out.String(), "--config") {
		t.Fatal(out.String(), e)
	}
}
func TestConfigDiscoveryUsesHomeAndXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "gtkaskpass-yubikey")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"cacheTTL":"8m"}`), 0600); e != nil {
		t.Fatal(e)
	}
	for _, explicitXDG := range []bool{false, true} {
		if explicitXDG {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		}
		var out bytes.Buffer
		root := newCommand(&out, &out, nil)
		root.SetArgs([]string{"config", "show"})
		if e := root.Execute(); e != nil {
			t.Fatal(e)
		}
		var cfg config.Settings
		if e := json.Unmarshal(out.Bytes(), &cfg); e != nil {
			t.Fatal(e)
		}
		want := "8m"
		if explicitXDG {
			want = "1h"
		}
		if cfg.CacheTTL != want {
			t.Fatal(cfg)
		}
	}
}
