// SPDX-License-Identifier: Apache-2.0
// Package config loads service settings using Koanf. Askpass prompts never pass
// through a CLI/configuration parser.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/pflag"
)

type Settings struct {
	CacheTTL           string `koanf:"cacheTTL" json:"cacheTTL"`
	PINVerification    string `koanf:"pinVerification" json:"pinVerification"`
	TouchNotifications bool   `koanf:"touchNotifications" json:"touchNotifications"`
	Trace              bool   `koanf:"trace" json:"trace"`
}

func (s Settings) Validate() error {
	ttl, err := time.ParseDuration(s.CacheTTL)
	if err != nil || ttl < 0 {
		return errors.New("cacheTTL must be a nonnegative Go duration, e.g. 1h, 15m, or 0")
	}
	if s.PINVerification != "required" && s.PINVerification != "off" {
		return errors.New("pinVerification must be required or off")
	}
	return nil
}

// Flags keeps existing service CLI names while binding them to configuration
// keys. Only explicitly changed flags override file values.
func Flags(f *pflag.FlagSet) {
	f.String("ttl", "1h", "absolute credential lifetime; 0 disables caching")
	f.String("pin-verification", "required", "required or off (unverified compatibility mode)")
	f.Bool("touch-monitor", true, "passively monitor USB FIDO2 touch requests")
	f.Bool("trace", false, "trace service metadata to stderr")
}

// Load uses an explicit file exclusively, otherwise exactly one config file in
// configDir/gtkaskpass-yubikey. Missing automatic config is fine; an explicit
// missing file or ambiguous formats is an error. No global configuration/env.
func Load(path, configDir string, flags *pflag.FlagSet) (Settings, string, error) {
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(map[string]any{"cacheTTL": "1h", "pinVerification": "required", "touchNotifications": true, "trace": false}, "."), nil); err != nil {
		return Settings{}, "", err
	}
	if path == "" {
		for _, ext := range []string{"yaml", "yml", "toml", "json"} {
			candidate := filepath.Join(configDir, "gtkaskpass-yubikey", "config."+ext)
			_, err := os.Lstat(candidate)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return Settings{}, "", err
			}
			if path != "" {
				return Settings{}, "", errors.New("multiple configuration files found; select one with --config")
			}
			path = candidate
		}
	}
	if path != "" {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
		var parser koanf.Parser
		switch ext {
		case "yaml", "yml":
			parser = yaml.Parser()
		case "toml":
			parser = toml.Parser()
		case "json":
			parser = json.Parser()
		default:
			return Settings{}, path, errors.New("configuration must be YAML, TOML, or JSON")
		}
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return Settings{}, path, err
		}
		defer func() { _ = f.Close() }()
		stat, err := f.Stat()
		if err != nil {
			return Settings{}, path, err
		}
		if !stat.Mode().IsRegular() {
			return Settings{}, path, errors.New("configuration must be a regular file")
		}
		b, err := io.ReadAll(io.LimitReader(f, 65537))
		if err != nil {
			return Settings{}, path, err
		}
		if len(b) > 65536 {
			return Settings{}, path, errors.New("configuration exceeds 64 KiB")
		}
		if len(bytes.TrimSpace(b)) == 0 {
			return Settings{}, path, errors.New("configuration is empty")
		}
		values, err := parser.Unmarshal(b)
		if err != nil {
			return Settings{}, path, fmt.Errorf("parse configuration: %w", err)
		}
		if values == nil {
			return Settings{}, path, errors.New("configuration must contain an object")
		}
		for key, value := range values {
			if value == nil {
				return Settings{}, path, fmt.Errorf("configuration field %q cannot be null", key)
			}
		}
		if err := k.Load(confmap.Provider(values, ""), nil); err != nil {
			return Settings{}, path, err
		}
	}
	provider := posflag.ProviderWithFlag(flags, ".", k, func(f *pflag.Flag) (string, any) {
		key := map[string]string{"ttl": "cacheTTL", "pin-verification": "pinVerification", "touch-monitor": "touchNotifications", "trace": "trace"}[f.Name]
		if key == "" || !f.Changed {
			return "", nil
		}
		return key, posflag.FlagVal(flags, f)
	})
	if err := k.Load(provider, nil); err != nil {
		return Settings{}, path, err
	}
	var settings Settings
	if err := k.UnmarshalWithConf("", &settings, koanf.UnmarshalConf{DecoderConfig: &mapstructure.DecoderConfig{ErrorUnused: true, WeaklyTypedInput: false, MatchName: func(key, field string) bool { return key == field }}}); err != nil {
		return Settings{}, path, fmt.Errorf("invalid configuration fields: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return Settings{}, path, err
	}
	return settings, path, nil
}
