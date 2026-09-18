//go:build integration

package integration

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func startUIWorker(t *testing.T) (func(any), func() map[string]any, *exec.Cmd) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	must(t, err)
	a, b := os.NewFile(uintptr(fds[0]), "test-parent"), os.NewFile(uintptr(fds[1]), "test-worker")
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	c, err := net.FileConn(a)
	must(t, err)
	t.Cleanup(func() { _ = c.Close() })
	cmd := exec.Command(filepath.Join(filepath.Dir(askpass), "ssh-askpass-fido-ui"))
	cmd.Env = env(nil)
	cmd.ExtraFiles = []*os.File{b}
	var diagnostics lockedBuffer
	cmd.Stderr = &diagnostics
	must(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	write := func(v any) {
		t.Helper()
		data, e := json.Marshal(v)
		must(t, e)
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], uint32(len(data)))
		_, e = c.Write(append(h[:], data...))
		must(t, e)
	}
	read := func() map[string]any {
		t.Helper()
		must(t, c.SetReadDeadline(time.Now().Add(10*time.Second)))
		var h [4]byte
		_, e := io.ReadFull(c, h[:])
		must(t, e)
		size := binary.BigEndian.Uint32(h[:])
		if size > 16384 {
			t.Fatal(size)
		}
		data := make([]byte, size)
		_, e = io.ReadFull(c, data)
		must(t, e)
		var r map[string]any
		must(t, json.Unmarshal(data, &r))
		return r
	}
	return write, read, cmd
}

func TestWorkerDeviceSelectionAndRetry(t *testing.T) {
	write, read, _ := startUIWorker(t)
	p := &process{} // window helper only; diagnostics stay on the worker
	view := map[string]any{"Request": map[string]any{"Mode": "input", "Title": "Device worker test", "Prompt": "Synthetic PIN request"}, "Stage": "choose", "Selected": 1, "Devices": []map[string]string{{"Label": "Token one"}, {"Label": "Token two"}}}
	write(view)
	if r := read(); r["Kind"] != "shown" {
		t.Fatal(r)
	}
	w := window(t, p, "Device worker test")
	xd(t, "windowactivate", "--sync", w)
	xd(t, "key", "alt+u")
	if r := read(); r["Kind"] != "select" || r["Selected"] != float64(1) {
		t.Fatal("saved preselection not confirmed", r)
	}
	view["Stage"] = "input"
	view["Message"] = "Selected Token two; 8 PIN attempts remaining"
	view["ChangeDevice"] = true
	write(view)
	read()
	typeAnswer(t, p, "Device worker test", "bad-test-pin")
	if r := read(); r["Value"] != "bad-test-pin" {
		t.Fatal(r)
	}
	view["Stage"] = "busy"
	write(view)
	read()
	view["Stage"] = "input"
	view["Message"] = "Incorrect PIN; 7 attempts remaining"
	write(view)
	read()
	typeAnswer(t, p, "Device worker test", "correct-test-pin")
	if r := read(); r["Value"] != "correct-test-pin" {
		t.Fatal("old entry was not cleared", r)
	}
	write(map[string]string{"Stage": "close"})
}

func TestPassiveTouchWindow(t *testing.T) {
	// Keep a real input window focused while the independent touch popup maps.
	input := helper(t, nil, "Keep typing here")
	focus := window(t, input, "SSH input")
	xd(t, "windowactivate", "--sync", focus)
	write, read, cmd := startUIWorker(t)
	write(map[string]any{"Request": map[string]string{"Mode": "notify", "Title": "Passive touch test", "Prompt": "Test security key"}, "PassiveTouch": true})
	if r := read(); r["Kind"] != "shown" {
		t.Fatal(r)
	}
	p := &process{}
	w := window(t, p, "Passive touch test")
	if got := xd(t, "getwindowfocus"); got != focus {
		t.Fatalf("passive popup stole focus: %s -> %s", focus, got)
	}
	xd(t, "windowactivate", "--sync", w)
	xd(t, "key", "Escape")
	deadline := time.Now().Add(3 * time.Second)
	for len(windows("Passive touch test")) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(windows("Passive touch test")) != 0 {
		t.Fatal("dismiss did not hide popup")
	}
	must(t, cmd.Process.Signal(syscall.Signal(0)))
	write(map[string]string{"Stage": "close"})
	xd(t, "windowactivate", "--sync", focus)
	xd(t, "key", "Escape")
	wait(t, input, 1, "")
}
