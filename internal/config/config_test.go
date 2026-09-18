package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func flagSet(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	f := pflag.NewFlagSet("test", pflag.ContinueOnError)
	Flags(f)
	// Cobra help/config flags must not leak into the application's schema.
	f.Bool("help", false, "")
	f.String("config", "", "")
	if e := f.Parse(args); e != nil {
		t.Fatal(e)
	}
	return f
}
func file(t *testing.T, dir, name, contents string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(contents), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestFormatsAndOverrides(t *testing.T) {
	for name, text := range map[string]string{
		"config.yaml": "cacheTTL: 15m\npinVerification: off\ntouchNotifications: false\ntrace: true\n",
		"config.yml":  "cacheTTL: 15m\npinVerification: off\ntouchNotifications: false\ntrace: true\n",
		"config.toml": "cacheTTL = '15m'\npinVerification = 'off'\ntouchNotifications = false\ntrace = true\n",
		"config.json": `{"cacheTTL":"15m","pinVerification":"off","touchNotifications":false,"trace":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			p := file(t, dir, "gtkaskpass-yubikey/"+name, text)
			cfg, used, e := Load("", dir, flagSet(t))
			if e != nil || used != p {
				t.Fatal(cfg, used, e)
			}
			if cfg != (Settings{CacheTTL: "15m", PINVerification: "off", TouchNotifications: false, Trace: true}) {
				t.Fatal(cfg)
			}
			cfg, _, e = Load(p, dir, flagSet(t, "--ttl=0", "--pin-verification=required", "--touch-monitor=true", "--trace=false"))
			if e != nil {
				t.Fatal(e)
			}
			if cfg != (Settings{CacheTTL: "0", PINVerification: "required", TouchNotifications: true, Trace: false}) {
				t.Fatal("explicit flags lost", cfg)
			}
		})
	}
}
func TestDefaultsAndExplicitSelection(t *testing.T) {
	dir := t.TempDir()
	cfg, used, e := Load("", dir, flagSet(t))
	if e != nil || used != "" || cfg != (Settings{CacheTTL: "1h", PINVerification: "required", TouchNotifications: true}) {
		t.Fatal(cfg, used, e)
	}
	p := file(t, dir, "gtkaskpass-yubikey/config.yaml", "cacheTTL: 2h\n")
	file(t, dir, "gtkaskpass-yubikey/config.json", `{}`)
	if _, _, e := Load("", dir, flagSet(t)); e == nil {
		t.Fatal("ambiguous configuration accepted")
	}
	cfg, _, e = Load(p, dir, flagSet(t))
	if e != nil || cfg.CacheTTL != "2h" || cfg.PINVerification != "required" {
		t.Fatal(cfg, e)
	}
	if _, _, e := Load(filepath.Join(dir, "missing.json"), dir, flagSet(t)); e == nil {
		t.Fatal("explicit missing file ignored")
	}
}
func TestValidation(t *testing.T) {
	for _, text := range []string{
		`{"cacheTTL":"-1h"}`, `{"cacheTTL":"forever"}`, `{"cacheTTL":3600}`,
		`{"pinVerification":"optional"}`, `{"pinVerification":false}`, `{"pinverification":"off"}`,
		`{"touchNotifications":"false"}`, `{"trace":1}`, `{"trace":null}`,
		`{"trace":{"enabled":true}}`, `{"unknown":{}}`, `{"cache":{"ttl":"1h"}}`,
		`{"cacheTTL":`, `null`, `[]`, "", strings.Repeat(" ", 65537),
	} {
		t.Run(text[:min(len(text), 30)], func(t *testing.T) {
			p := file(t, t.TempDir(), "config.json", text)
			if _, _, e := Load(p, "", flagSet(t)); e == nil {
				t.Fatalf("invalid config accepted: %.80s", text)
			}
		})
	}
	for _, args := range [][]string{{"--ttl=-1s"}, {"--ttl=forever"}, {"--pin-verification=maybe"}} {
		if _, _, e := Load("", t.TempDir(), flagSet(t, args...)); e == nil {
			t.Fatal("invalid flags accepted", args)
		}
	}
}
func TestConfigMustBeRegularButMayBeSymlink(t *testing.T) {
	dir := t.TempDir()
	p := file(t, dir, "stored.json", `{"cacheTTL":"2s"}`)
	link := filepath.Join(dir, "config.json")
	if e := os.Symlink(p, link); e != nil {
		t.Fatal(e)
	}
	if cfg, _, e := Load(link, dir, flagSet(t)); e != nil || cfg.CacheTTL != "2s" {
		t.Fatal(cfg, e)
	}
	if _, _, e := Load(dir, dir, flagSet(t)); e == nil {
		t.Fatal("directory accepted")
	}
}
