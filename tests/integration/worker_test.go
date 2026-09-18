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

func TestWorkerDeviceSelectionAndRetry(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	must(t, err)
	a, b := os.NewFile(uintptr(fds[0]), "test-parent"), os.NewFile(uintptr(fds[1]), "test-worker")
	defer func() { _ = a.Close(); _ = b.Close() }()
	c, err := net.FileConn(a)
	must(t, err)
	defer func() { _ = c.Close() }()
	cmd := exec.Command(filepath.Join(filepath.Dir(askpass), "gtkaskpass-yubikey-ui"))
	cmd.Env = env(nil)
	cmd.ExtraFiles = []*os.File{b}
	var diagnostics lockedBuffer
	cmd.Stderr = &diagnostics
	must(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	p := &process{} // window helper only; diagnostics stay on the worker
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
