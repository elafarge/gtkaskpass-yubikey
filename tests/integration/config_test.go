//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServiceConfigFileAndOverrides(t *testing.T) {
	root, err := os.MkdirTemp("", "ga-config-")
	must(t, err)
	t.Cleanup(func() {
		if e := os.RemoveAll(root); e != nil {
			t.Error(e)
		}
	})
	dir := filepath.Join(root, "config", "ssh-askpass-fido")
	must(t, os.MkdirAll(dir, 0700))
	p := filepath.Join(dir, "config.yaml")
	must(t, os.WriteFile(p, []byte("cacheTTL: 0\npinVerification: off\ntouchNotifications: false\ntrace: false\n"), 0600))
	e := map[string]string{"XDG_RUNTIME_DIR": root, "XDG_CONFIG_HOME": filepath.Join(root, "config"), "SSH_ASKPASS_FIDO_CACHE": "on"}
	// The file disables cache, but an explicit CLI flag overrides just TTL.
	d := start(t, e, daemon, "serve", "--ttl=1h")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, "ssh-askpass-fido", "cache.sock")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	key := filepath.Join(root, "key")
	must(t, os.WriteFile(key, []byte("fixture"), 0600))
	prompt := "Enter passphrase for " + key + ": "
	first := helper(t, e, prompt)
	typeAnswer(t, first, "SSH passphrase", "from-config")
	wait(t, first, 0, "from-config\n")
	second := helper(t, e, prompt)
	wait(t, second, 0, "from-config\n")
	if strings.Contains(d.err.String(), "msg=daemon-start") || strings.Contains(d.err.String(), "msg=cache") {
		t.Fatal("trace:false not respected", d.err.String())
	}
}
