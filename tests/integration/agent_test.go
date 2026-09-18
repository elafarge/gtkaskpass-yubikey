//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Real ssh-agent generates both PIN prompt variants. The test-only software
// provider signs with disposable keys, so this never consumes hardware retries.
func TestAgentPINCache(t *testing.T) {
	provider := os.Getenv("SK_TEST_PROVIDER")
	if provider == "" {
		t.Fatal("SK_TEST_PROVIDER required; use scripts/integration.sh")
	}
	for _, touch := range []bool{false, true} {
		t.Run(map[bool]string{false: "PIN", true: "PIN-and-presence"}[touch], func(t *testing.T) {
			e, _ := cacheEnv(t, "1h")
			e["HOME"] = e["XDG_RUNTIME_DIR"]
			e["SSH_AUTH_SOCK"] = filepath.Join(e["HOME"], "agent.sock")
			e["SSH_ASKPASS"], e["SSH_ASKPASS_REQUIRE"] = askpass, "force"
			e["SSH_SK_PROVIDER"] = provider
			agent := start(t, e, "ssh-agent", "-D", "-a", e["SSH_AUTH_SOCK"], "-P", provider, "-O", "no-restrict-websafe")
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(e["SSH_AUTH_SOCK"]); err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			key := filepath.Join(e["HOME"], "fido-test")
			args := []string{"-q", "-t", "ed25519-sk", "-w", provider, "-N", "", "-f", key, "-O", "verify-required"}
			if !touch {
				args = append(args, "-O", "no-touch-required")
			}
			wait(t, start(t, e, "ssh-keygen", args...), 0, "")
			wait(t, start(t, e, "ssh-add", "-S", provider, key), 0, "")
			first := start(t, e, "ssh-add", "-T", key+".pub")
			typeAnswer(t, agent, "Security key PIN", "integration-pin")
			wait(t, first, 0, "") // ssh-add verifies the actual FIDO signature
			if !strings.Contains(agent.err.String(), "reason=stored") {
				t.Fatal("PIN was not stored", agent.err.String())
			}
			for i := 0; i < 2; i++ {
				wait(t, start(t, e, "ssh-add", "-T", key+".pub"), 0, "")
			}
			if strings.Count(agent.err.String(), "reason=hit") != 2 {
				t.Fatal("persistent agent did not reuse cache", agent.err.String())
			}
			// A separate OpenSSH connection forwards this same agent to a local
			// test server, which requests a FIDO signature through that channel.
			wait(t, start(t, e, "python3", "../forwarded-agent.py", e["HOME"]), 0, "")
			if strings.Count(agent.err.String(), "reason=hit") != 3 {
				t.Fatal("forwarded operation did not reuse PIN", agent.err.String())
			}
			if strings.Contains(agent.err.String(), "integration-pin") {
				t.Fatal("agent PIN leaked to metadata trace")
			}
			fingerprintCommand := exec.Command("ssh-keygen", "-lf", key+".pub")
			out, err := fingerprintCommand.Output()
			must(t, err)
			fingerprint := strings.Fields(string(out))[1]
			wait(t, start(t, e, daemon, "forget", "--fingerprint", fingerprint), 0, "")
			next := start(t, e, "ssh-add", "-T", key+".pub")
			typeAnswer(t, agent, "Security key PIN", "integration-pin")
			wait(t, next, 0, "")
			must(t, agent.cmd.Process.Signal(syscall.SIGTERM))
		})
	}
}
