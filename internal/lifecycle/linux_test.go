package lifecycle

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestParentLifetime(t *testing.T) {
	if os.Getenv("LIFECYCLE_TEST_CHILD") == "1" {
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestParentLifetime$")
	cmd.Env = append(os.Environ(), "LIFECYCLE_TEST_CHILD=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	p, err := ReadProcess(cmd.Process.Pid)
	if err != nil || !p.Alive() {
		t.Fatal(p, err)
	}
	wrong := p
	wrong.Start = "0"
	if wrong.Alive() {
		t.Fatal("reused PID accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { WaitParent(ctx, p); close(done) }()
	cmd.Process.Kill()
	cmd.Wait()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("parent exit was not observed")
	}
	if BootTime() <= 0 {
		t.Fatal("invalid elapsed time")
	}
}
