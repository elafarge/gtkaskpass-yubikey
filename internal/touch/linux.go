// SPDX-License-Identifier: Apache-2.0
package touch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

const MaxDevices = 32

type Device struct{ Path, Label string }
type Sink interface{ SetTouch(Device, bool) }
type Monitor struct {
	Sink       Sink
	Diagnostic func(string)
	Event      func(Device, bool)
	covered    atomic.Bool
}

func (m *Monitor) Covered() bool { return m.covered.Load() }

func (m *Monitor) state(d Device, active bool) {
	if m.Event != nil {
		m.Event(d, active)
	}
	m.Sink.SetTouch(d, active)
}

type reader struct {
	fd      int
	device  Device
	layout  Layout
	tracker Tracker
	stat    unix.Stat_t
	active  bool
}

func readBounded(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	b, e := io.ReadAll(io.LimitReader(f, 4097))
	if len(b) > 4096 {
		return nil, errors.New("oversized HID metadata")
	}
	return b, e
}

// Open uses O_RDONLY; the only ioctls retrieve cached kernel metadata. There are
// no output/feature reports, libfido calls, channel allocation or device probes.
func open(path string) (*reader, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			_ = unix.Close(fd)
		}
	}()
	var stat unix.Stat_t
	if e = unix.Fstat(fd, &stat); e != nil {
		return nil, e
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFCHR {
		return nil, errors.New("not a character device")
	}
	info, e := unix.IoctlHIDGetRawInfo(fd)
	if e != nil {
		return nil, e
	}
	if info.Bustype != unix.BUS_USB {
		return nil, errors.New("not a USB HID device")
	}
	size, e := unix.IoctlGetInt(fd, unix.HIDIOCGRDESCSIZE)
	if e != nil {
		return nil, e
	}
	if size < 1 || size > 4096 {
		return nil, errors.New("invalid HID descriptor size")
	}
	d := unix.HIDRawReportDescriptor{Size: uint32(size)}
	if e = unix.IoctlHIDGetDesc(fd, &d); e != nil {
		return nil, e
	}
	layout, e := Descriptor(d.Value[:size])
	if e != nil {
		return nil, e
	}
	if len(layout) == 0 {
		return nil, errors.New("no supported FIDO input report")
	}
	name, e := unix.IoctlHIDGetRawName(fd)
	if e != nil || name == "" {
		name = "FIDO2 security key"
	}
	// The path disambiguates identical model names without claiming stable ID.
	r := &reader{fd: fd, device: Device{Path: path, Label: name + " (" + filepath.Base(path) + ")"}, layout: layout, stat: stat}
	ok = true
	return r, nil
}

// Run rescans kernel metadata every second for hotplug/ACL changes and polls
// read-only handles between scans. No user-space FIDO operation is sent.
func (m *Monitor) Run(ctx context.Context) {
	readers := map[string]*reader{}
	defer func() {
		m.covered.Store(false)
		for _, r := range readers {
			_ = unix.Close(r.fd)
			if r.active {
				m.state(r.device, false)
			}
		}
	}()
	lastDiagnostic := ""
	diagnose := func(text string) {
		if text != lastDiagnostic {
			lastDiagnostic = text
			if m.Diagnostic != nil {
				m.Diagnostic(text)
			}
		}
	}
	remove := func(path string) {
		r := readers[path]
		_ = unix.Close(r.fd)
		if r.active {
			m.state(r.device, false)
		}
		delete(readers, path)
		m.covered.Store(false)
	}
	nextScan := time.Time{}
	for ctx.Err() == nil {
		now := time.Now()
		if !now.Before(nextScan) {
			nextScan = now.Add(time.Second)
			paths, e := filepath.Glob("/sys/class/hidraw/hidraw*")
			seen := map[string]bool{}
			wanted, unavailable := 0, 0
			if e != nil {
				unavailable++
			}
			if len(paths) > 256 {
				paths = paths[:256]
				unavailable++
			}
			for _, sysPath := range paths {
				data, e := readBounded(filepath.Join(sysPath, "device/report_descriptor"))
				if e != nil {
					unavailable++
					continue
				}
				// Non-FIDO and unsupported descriptors are never read as input.
				layout, e := Descriptor(data)
				clear(data)
				if e != nil {
					// Unknown layouts must not count as complete coverage when
					// deciding whether OpenSSH's own popup can be suppressed.
					unavailable++
					continue
				}
				if len(layout) == 0 {
					continue
				}
				path := "/dev/" + filepath.Base(sysPath)
				wanted++
				seen[path] = true
				if r := readers[path]; r != nil {
					var st unix.Stat_t
					if unix.Lstat(path, &st) == nil && st.Ino == r.stat.Ino && st.Rdev == r.stat.Rdev && st.Ctim == r.stat.Ctim {
						continue
					}
					remove(path)
				}
				if len(readers) >= MaxDevices {
					unavailable++
					continue
				}
				r, e := open(path)
				if e != nil {
					unavailable++
					continue
				}
				readers[path] = r
			}
			for path := range readers {
				if !seen[path] {
					remove(path)
				}
			}
			m.covered.Store(wanted > 0 && len(readers) == wanted && unavailable == 0)
			diagnose(fmt.Sprintf("touch monitor: %d FIDO HID interface(s) watched, %d unavailable", len(readers), unavailable))
		}
		fds := make([]unix.PollFd, 0, len(readers))
		list := make([]*reader, 0, len(readers))
		for _, r := range readers {
			fds = append(fds, unix.PollFd{Fd: int32(r.fd), Events: unix.POLLIN})
			list = append(list, r)
		}
		_, e := unix.Poll(fds, 100)
		if e != nil && e != unix.EINTR {
			diagnose("touch monitor: HID polling failed")
			m.covered.Store(false)
		}
		for i, r := range list {
			if fds[i].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
				remove(r.device.Path)
				continue
			}
			if fds[i].Revents&unix.POLLIN != 0 {
				var buffer [1025]byte
				for burst := 0; burst < 64; burst++ {
					n, e := unix.Read(r.fd, buffer[:])
					if e == unix.EAGAIN || e == unix.EINTR {
						break
					}
					if e != nil || n == 0 {
						clear(buffer[:])
						remove(r.device.Path)
						break
					}
					r.tracker.Report(r.layout, buffer[:n], time.Now())
					clear(buffer[:])
					active := r.tracker.Active()
					if active != r.active {
						r.active = active
						m.state(r.device, active)
					}
				}
			}
			if _, exists := readers[r.device.Path]; !exists {
				continue
			}
			r.tracker.Expire(time.Now())
			active := r.tracker.Active()
			if active != r.active {
				r.active = active
				m.state(r.device, active)
			}
		}
	}
}

// Device labels are plain text; avoid terminal control characters in metadata.
func CleanLabel(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}
