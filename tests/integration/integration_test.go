//go:build integration

// SPDX-License-Identifier: Apache-2.0
package integration

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var askpass, daemon string

func TestMain(m *testing.M) {
	// Subprocess caller harnesses inherit their parent's test environment.
	if os.Getenv("GTKASKPASS_CALLER") != "" {
		os.Exit(m.Run())
	}
	askpass, daemon = os.Getenv("ASKPASS_BIN"), os.Getenv("CACHE_BIN")
	if askpass == "" || daemon == "" || os.Getenv("GTKASKPASS_TEST_DISPLAY") != "1" {
		fmt.Fprintln(os.Stderr, "run via scripts/integration.sh with built binaries")
		os.Exit(2)
	}
	for _, tool := range []string{"xdotool", "openbox", "ssh-agent", "ssh-add", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	wm := exec.Command("openbox")
	if err := wm.Start(); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = wm.Process.Kill() // best-effort cleanup; the window manager may have exited
	_ = wm.Wait()
	os.Exit(code)
}

// TestCallerProcess is a launcher, not an alternate UI or credential source.
// It gives each test request a genuine distinct caller identity like ssh does.
func TestCallerProcess(t *testing.T) {
	if os.Getenv("GTKASKPASS_CALLER") == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(2)
	}
	cmd := exec.Command(args[1], args[2:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		}
		os.Exit(2)
	}
	os.Exit(0)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

type process struct {
	cmd      *exec.Cmd
	out, err lockedBuffer
	done     chan error
}

func env(overrides map[string]string) []string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	delete(m, "SSH_ASKPASS_PROMPT")
	delete(m, "GTKASKPASS_CALLER")
	m["GTKASKPASS_CACHE"] = "off"
	m["GTKASKPASS_TRACE"] = "metadata"
	for k, v := range overrides {
		m[k] = v
	}
	r := make([]string, 0, len(m))
	for k, v := range m {
		r = append(r, k+"="+v)
	}
	return r
}

func start(t *testing.T, overrides map[string]string, program string, args ...string) *process {
	t.Helper()
	p := &process{done: make(chan error, 1)}
	p.cmd = exec.Command(program, args...)
	p.cmd.Env = env(overrides)
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.err
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL) })
	return p
}

func helper(t *testing.T, e map[string]string, prompt string) *process {
	t.Helper()
	if e == nil {
		e = map[string]string{}
	}
	e["GTKASKPASS_CALLER"] = "1"
	return start(t, e, os.Args[0], "-test.run=^TestCallerProcess$", "--", askpass, prompt)
}

func wait(t *testing.T, p *process, code int, out string) {
	t.Helper()
	select {
	case err := <-p.done:
		actual := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				actual = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if actual != code || p.out.String() != out {
			t.Fatalf("exit=%d want=%d stdout=%q want=%q stderr=%s", actual, code, p.out.String(), out, p.err.String())
		}
	case <-time.After(12 * time.Second):
		t.Fatalf("process timeout: %s", p.err.String())
	}
}

func xd(t *testing.T, args ...string) string {
	t.Helper()
	b, err := exec.Command("xdotool", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("xdotool %v: %s %v", args, b, err)
	}
	return strings.TrimSpace(string(b))
}

func windows(title string) []string {
	b, err := exec.Command("xdotool", "search", "--onlyvisible", "--name", "^"+title+"$").Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(b))
}

func window(t *testing.T, p *process, title string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if w := windows(title); len(w) > 0 {
			return w[len(w)-1]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %q window: %s", title, p.err.String())
	return ""
}

func typeAnswer(t *testing.T, p *process, title, value string) {
	t.Helper()
	w := window(t, p, title)
	xd(t, "windowactivate", "--sync", w)
	if value != "" {
		xd(t, "type", "--clearmodifiers", "--delay", "1", "--", value)
	}
	xd(t, "key", "--clearmodifiers", "Return")
}

func TestInputContract(t *testing.T) {
	for _, mode := range []string{"off", "metadata", "secrets"} {
		t.Run(mode, func(t *testing.T) {
			p := helper(t, map[string]string{"GTKASKPASS_TRACE": mode}, "<b>literal prompt</b>\nsecond line")
			typeAnswer(t, p, "SSH input", " synthetic value ")
			wait(t, p, 0, " synthetic value \n")
			if strings.Contains(p.err.String(), "synthetic value") != (mode == "secrets") {
				t.Fatalf("trace redaction: %s", p.err.String())
			}
		})
	}
	p := helper(t, nil, "Empty answer")
	typeAnswer(t, p, "SSH input", "")
	wait(t, p, 0, "\n")
	for _, key := range []string{"Escape", "alt+F4"} {
		p := helper(t, nil, "Cancel me")
		w := window(t, p, "SSH input")
		xd(t, "windowactivate", "--sync", w)
		xd(t, "key", key)
		wait(t, p, 1, "")
	}
}

func TestConfirmation(t *testing.T) {
	for _, tt := range []struct {
		key  string
		code int
		out  string
	}{{"alt+a", 0, "yes\n"}, {"alt+d", 1, ""}} {
		p := helper(t, map[string]string{"SSH_ASKPASS_PROMPT": "confirm"}, "Allow key use?")
		w := window(t, p, "SSH confirmation")
		xd(t, "windowactivate", "--sync", w)
		xd(t, "key", tt.key)
		wait(t, p, tt.code, tt.out)
	}
}

func TestNotificationLifecycle(t *testing.T) {
	p := start(t, map[string]string{"SSH_ASKPASS_PROMPT": "none"}, askpass, "Confirm user presence for key ED25519-SK test")
	w := window(t, p, "Touch your security key")
	xd(t, "windowactivate", "--sync", w)
	xd(t, "key", "Escape")
	deadline := time.Now().Add(3 * time.Second)
	for len(windows("Touch your security key")) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(windows("Touch your security key")) > 0 {
		t.Fatal("dismiss did not hide")
	}
	if err := p.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("dismiss ended process")
	}
	must(t, p.cmd.Process.Signal(syscall.SIGTERM))
	wait(t, p, 0, "")
	// Parent death must also stop the actual helper, including a hidden notifier.
	p = helper(t, map[string]string{"SSH_ASKPASS_PROMPT": "none"}, "Confirm user presence for key orphan")
	w = window(t, p, "Touch your security key")
	pid, _ := strconv.Atoi(xd(t, "getwindowpid", w))
	must(t, p.cmd.Process.Kill())
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(windows("Touch your security key")) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL) // best-effort cleanup before failure
	t.Fatal("orphan window remained")
}

func TestErrorsAndSignals(t *testing.T) {
	p := start(t, nil, askpass, "one", "two")
	wait(t, p, 2, "")
	p = start(t, map[string]string{"DISPLAY": "", "WAYLAND_DISPLAY": ""}, askpass, "no display")
	wait(t, p, 2, "")
	p = start(t, nil, askpass, "input")
	window(t, p, "SSH input")
	must(t, p.cmd.Process.Signal(syscall.SIGTERM))
	wait(t, p, 1, "")
	p = start(t, map[string]string{"SSH_ASKPASS_PROMPT": "none"}, askpass, "notification")
	must(t, p.cmd.Process.Signal(syscall.SIGTERM))
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		t.Fatal("early termination blocked")
	}
	if p.out.String() != "" {
		t.Fatal("early termination output")
	}
}

func TestConcurrentWindows(t *testing.T) {
	a := helper(t, nil, "one")
	window(t, a, "SSH input")
	b := helper(t, nil, "two")
	deadline := time.Now().Add(10 * time.Second)
	for len(windows("SSH input")) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(windows("SSH input")) != 2 {
		t.Fatal("requests merged")
	}
	for _, w := range windows("SSH input") {
		xd(t, "windowactivate", "--sync", w)
		xd(t, "key", "Escape")
	}
	wait(t, a, 1, "")
	wait(t, b, 1, "")
}

func TestBrokenTracePipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	must(t, r.Close())
	p := &process{done: make(chan error, 1)}
	p.cmd = exec.Command(askpass, "Broken trace pipe")
	p.cmd.Env = env(map[string]string{"GTKASKPASS_TRACE": "secrets"})
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.cmd.Stdout, p.cmd.Stderr = &p.out, w
	if err := p.cmd.Start(); err != nil {
		_ = w.Close() // cleanup after failed process startup
		t.Fatal(err)
	}
	must(t, w.Close())
	t.Cleanup(func() { _ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL) })
	go func() { p.done <- p.cmd.Wait() }()
	typeAnswer(t, p, "SSH input", "answer")
	wait(t, p, 0, "answer\n")
}

func cacheEnv(t *testing.T, ttl string) (map[string]string, *process) {
	t.Helper()
	root, err := os.MkdirTemp("", "ga-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	e := map[string]string{"XDG_RUNTIME_DIR": root, "GTKASKPASS_CACHE": "on"}
	d := start(t, e, daemon, "serve", "--ttl", ttl, "--trace")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(root, "gtkaskpass-yubikey", "cache.sock")); err == nil {
			return e, d
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("daemon did not start: %s", d.err.String())
	return nil, nil
}

func TestCache(t *testing.T) {
	e, d := cacheEnv(t, "1h")
	key := filepath.Join(e["XDG_RUNTIME_DIR"], "key")
	must(t, os.WriteFile(key, []byte("fixture"), 0600))
	prompt := "Enter passphrase for " + key + ": "
	p := helper(t, e, prompt)
	typeAnswer(t, p, "SSH passphrase", "cached-answer")
	wait(t, p, 0, "cached-answer\n")
	p = helper(t, e, prompt)
	wait(t, p, 0, "cached-answer\n")
	if !strings.Contains(p.err.String(), "reason=hit") {
		t.Fatal(p.err.String())
	}
	// A cache hit needs no graphical display.
	e["DISPLAY"] = ""
	e["WAYLAND_DISPLAY"] = ""
	p = helper(t, e, prompt)
	wait(t, p, 0, "cached-answer\n")
	delete(e, "DISPLAY")
	delete(e, "WAYLAND_DISPLAY")
	p = helper(t, e, "Enter PIN for ED25519-SK key "+key+": ")
	typeAnswer(t, p, "Security key PIN", "123456")
	wait(t, p, 0, "123456\n")
	forget := start(t, e, daemon, "forget", "--key", key, "--kind", "passphrase")
	wait(t, forget, 0, "")
	p = helper(t, e, prompt)
	typeAnswer(t, p, "SSH passphrase", "new-answer")
	wait(t, p, 0, "new-answer\n")
	must(t, os.WriteFile(key, []byte("changed fixture"), 0600))
	p = helper(t, e, prompt)
	typeAnswer(t, p, "SSH passphrase", "changed-answer")
	wait(t, p, 0, "changed-answer\n")
	must(t, d.cmd.Process.Signal(syscall.SIGTERM))
	wait(t, d, 0, "")
	p = helper(t, e, prompt)
	typeAnswer(t, p, "SSH passphrase", "fallback")
	wait(t, p, 0, "fallback\n")
	if strings.Contains(d.err.String(), "cached-answer") || strings.Contains(d.err.String(), "123456") {
		t.Fatal("daemon secret leak")
	}
}

func TestCacheExpiry(t *testing.T) {
	e, _ := cacheEnv(t, "100ms")
	key := filepath.Join(e["XDG_RUNTIME_DIR"], "key")
	must(t, os.WriteFile(key, []byte("fixture"), 0600))
	prompt := "Enter passphrase for " + key + ": "
	p := helper(t, e, prompt)
	typeAnswer(t, p, "SSH passphrase", "short-lived")
	wait(t, p, 0, "short-lived\n")
	// This wait deliberately advances real time past the configured expiry.
	time.Sleep(150 * time.Millisecond)
	p = helper(t, e, prompt)
	typeAnswer(t, p, "SSH passphrase", "fresh")
	wait(t, p, 0, "fresh\n")
}

func TestOpenSSH(t *testing.T) {
	e, _ := cacheEnv(t, "1h")
	e["HOME"] = e["XDG_RUNTIME_DIR"]
	e["SSH_AUTH_SOCK"] = filepath.Join(e["HOME"], "agent.sock")
	e["SSH_ASKPASS"] = askpass
	e["SSH_ASKPASS_REQUIRE"] = "force"
	agent := start(t, e, "ssh-agent", "-D", "-a", e["SSH_AUTH_SOCK"])
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(e["SSH_AUTH_SOCK"]); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	key := filepath.Join(e["HOME"], "testkey")
	k := start(t, e, "ssh-keygen", "-q", "-t", "ed25519", "-N", "test-passphrase", "-C", "integration", "-f", key)
	wait(t, k, 0, "")
	p := start(t, e, "ssh-add", key)
	typeAnswer(t, p, "SSH passphrase", "wrong-passphrase")
	// Wait for the first helper to close and the retry's trace to arrive before
	// typing again; otherwise input can target a window being destroyed.
	deadline = time.Now().Add(10 * time.Second)
	for !strings.Contains(p.err.String(), "reason=retry") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(p.err.String(), "reason=retry") {
		t.Fatal("no retry", p.err.String())
	}
	typeAnswer(t, p, "SSH passphrase", "test-passphrase")
	wait(t, p, 0, "")
	list := exec.Command("ssh-add", "-L")
	list.Env = env(e)
	pub, err := list.Output()
	if err != nil || !strings.Contains(string(pub), "integration") {
		t.Fatal(string(pub), err)
	}
	p = start(t, e, "ssh-add", "-d", key)
	wait(t, p, 0, "")
	p = start(t, e, "ssh-add", key)
	wait(t, p, 0, "")
	if !strings.Contains(p.err.String(), "reason=hit") {
		t.Fatal("not cached", p.err.String())
	}
	p = start(t, e, daemon, "forget", "--all")
	wait(t, p, 0, "")
	p = start(t, e, "ssh-add", "-d", key)
	wait(t, p, 0, "")
	p = start(t, e, "ssh-add", key)
	w := window(t, p, "SSH passphrase")
	xd(t, "windowactivate", "--sync", w)
	xd(t, "key", "Escape")
	wait(t, p, 1, "")
	must(t, agent.cmd.Process.Signal(syscall.SIGTERM))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
