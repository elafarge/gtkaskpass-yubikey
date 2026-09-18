// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/elafarge/ssh-askpass-fido/internal/gtkui"
)

func init() { runtime.LockOSThread() }
func main() {
	signal.Ignore(syscall.SIGPIPE)
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	f := os.NewFile(3, "service-ui")
	c, err := net.FileConn(f)
	_ = f.Close()
	if err == nil {
		defer func() { _ = c.Close() }()
		err = gtkui.Run(context.Background(), c)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ssh-askpass-fido UI:", err)
		os.Exit(2)
	}
}
