// SPDX-License-Identifier: Apache-2.0
package lifecycle

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type Process struct {
	PID    int
	Parent int
	Start  string
	State  string
}

func ReadProcess(pid int) (Process, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return Process{}, err
	}
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return Process{}, fmt.Errorf("invalid process stat")
	}
	f := strings.Fields(string(b[i+1:]))
	if len(f) < 20 {
		return Process{}, fmt.Errorf("short process stat")
	}
	parent, err := strconv.Atoi(f[1])
	if err != nil {
		return Process{}, err
	}
	return Process{pid, parent, f[19], f[0]}, nil
}

func (p Process) ID() string { return fmt.Sprintf("%d:%s", p.PID, p.Start) }

func (p Process) Alive() bool {
	n, err := ReadProcess(p.PID)
	return err == nil && n.Start == p.Start && n.State != "Z" && n.State != "X"
}

// WaitParent uses pidfd when supported. /proc identity checks are the fallback
// and also cover the race between observing the parent and opening its pidfd.
func WaitParent(ctx context.Context, p Process) {
	fd, err := unix.PidfdOpen(p.PID, 0)
	if err == nil {
		defer unix.Close(fd)
	}
	for ctx.Err() == nil && p.Alive() {
		if err == nil {
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
			if n, e := unix.Poll(fds, 100); e == nil && n > 0 {
				return
			}
		} else {
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

// BootTime includes suspend, unlike Go's monotonic time on Linux.
func BootTime() time.Duration {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		panic(err)
	}
	return time.Duration(ts.Nano())
}
