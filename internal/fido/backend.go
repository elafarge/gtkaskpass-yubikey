// SPDX-License-Identifier: Apache-2.0
package fido

/*
#cgo pkg-config: libfido2
#include <fido.h>
#include <stdlib.h>
#include <string.h>
static void wipe_free(char *p, size_t n) {
    volatile char *q = p;
    while (n--) *q++ = 0;
    free(p);
}
static int verify_pin(fido_dev_t *dev, const char *pin) {
    int r = fido_dev_get_puat(dev, FIDO_PUAT_GETASSERT, "ssh:", pin);
    fido_dev_set_puat(dev, NULL, 0);
    return r;
}
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/elafarge/ssh-askpass-fido/internal/device"
)

type Backend struct{}

var initialize sync.Once

func initLibrary() { initialize.Do(func() { C.fido_init(0) }) }

func open(path string) (*C.fido_dev_t, error) {
	initLibrary()
	d := C.fido_dev_new()
	if d == nil {
		return nil, fmt.Errorf("cannot allocate FIDO handle")
	}
	C.fido_dev_set_timeout(d, 3000)
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	if C.fido_dev_open(d, p) != C.FIDO_OK {
		C.fido_dev_free(&d)
		return nil, fmt.Errorf("cannot open FIDO device")
	}
	return d, nil
}
func closeDevice(d *C.fido_dev_t) { C.fido_dev_close(d); C.fido_dev_free(&d) }

func (Backend) Discover(ctx context.Context) ([]device.Device, error) {
	initLibrary()
	list := C.fido_dev_info_new(32)
	if list == nil {
		return nil, fmt.Errorf("cannot enumerate devices")
	}
	defer C.fido_dev_info_free(&list, 32)
	var n C.size_t
	if C.fido_dev_info_manifest(list, 32, &n) != C.FIDO_OK {
		return nil, fmt.Errorf("FIDO discovery failed")
	}
	var result []device.Device
	for i := C.size_t(0); i < n; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info := C.fido_dev_info_ptr(list, i)
		path := C.GoString(C.fido_dev_info_path(info))
		d, err := open(path)
		if err != nil {
			continue
		}
		eligible := bool(C.fido_dev_is_fido2(d)) && bool(C.fido_dev_has_pin(d))
		retries := -1
		var count C.int
		if eligible && C.fido_dev_get_retry_count(d, &count) == C.FIDO_OK {
			retries = int(count)
		}
		closeDevice(d)
		if !eligible {
			continue
		}
		stable, connection, serial := identity(path)
		label := strings.TrimSpace(C.GoString(C.fido_dev_info_manufacturer_string(info)) + " " + C.GoString(C.fido_dev_info_product_string(info)))
		if serial != "" {
			label += " (serial " + serial + ")"
		} else {
			label += " (" + path + ")"
		}
		result = append(result, device.Device{Path: path, Label: label, StableID: stable, Connection: connection, Retries: retries})
	}
	return result, nil
}

// USB serials are preferences, not credential ownership proofs. Device-node and
// sysfs inode identities bind cached PINs to this connection, not a reused path.
func identity(path string) (stable, connection, serial string) {
	st, err := os.Stat(path)
	if err != nil {
		return "", "", ""
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", ""
	}
	base, err := filepath.EvalSymlinks("/sys/class/hidraw/" + filepath.Base(path) + "/device")
	if err != nil {
		return "", "", ""
	}
	connection = fmt.Sprintf("%s:%d:%d:%d:%d", base, sys.Ino, sys.Rdev, sys.Ctim.Sec, sys.Ctim.Nsec)
	read := func(p string) string {
		b, e := os.ReadFile(p)
		if e != nil || len(b) > 256 {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	for p := base; p != "/"; p = filepath.Dir(p) {
		vid, pid := read(filepath.Join(p, "idVendor")), read(filepath.Join(p, "idProduct"))
		if vid == "" || pid == "" {
			continue
		}
		serial = read(filepath.Join(p, "serial"))
		connection += ":" + read(filepath.Join(p, "busnum")) + ":" + read(filepath.Join(p, "devnum"))
		if serial != "" {
			stable = fmt.Sprintf("usb-serial-v1:%x", sha256.Sum256([]byte(vid+":"+pid+":"+serial)))
		}
		break
	}
	return
}

func (Backend) Verify(ctx context.Context, selected device.Device, pin []byte) device.Result {
	r := device.Result{Status: device.Unavailable, Retries: -1, Message: "Device unavailable, busy, or unsupported. Reconnect or select another device."}
	if ctx.Err() != nil {
		return r
	}
	_, connection, _ := identity(selected.Path)
	if connection == "" || connection != selected.Connection {
		return r
	}
	d, err := open(selected.Path)
	if err != nil {
		return r
	}
	defer closeDevice(d)
	_, openedConnection, _ := identity(selected.Path)
	if openedConnection != selected.Connection {
		return r
	}
	var retries C.int
	if C.fido_dev_get_retry_count(d, &retries) == C.FIDO_OK {
		r.Retries = int(retries)
	}
	if r.Retries == 0 {
		r.Status = device.Blocked
		r.Message = "PIN blocked. Use the device's recovery procedure."
		return r
	}
	if ctx.Err() != nil {
		return r
	}
	p := C.CString(string(pin))
	defer C.wipe_free(p, C.size_t(len(pin)))
	code := C.verify_pin(d, p)
	switch code {
	case C.FIDO_OK:
		r.Status = device.Accepted
		r.Message = "PIN accepted"
	case C.FIDO_ERR_PIN_INVALID:
		r.Status = device.Invalid
		r.Message = "Incorrect PIN. Enter it again; no retry is automatic."
	case C.FIDO_ERR_PIN_AUTH_BLOCKED:
		r.Status = device.TemporaryBlock
		r.Message = "PIN authentication temporarily blocked. Reconnect the device."
	case C.FIDO_ERR_PIN_BLOCKED:
		r.Status = device.Blocked
		r.Message = "PIN blocked. Use the device's recovery procedure."
	}
	if r.Status != device.Accepted {
		r.Retries = -1
		if C.fido_dev_get_retry_count(d, &retries) == C.FIDO_OK {
			r.Retries = int(retries)
		}
	}
	return r
}
