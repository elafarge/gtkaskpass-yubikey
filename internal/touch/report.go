// SPDX-License-Identifier: Apache-2.0
// Package touch passively interprets incoming USB FIDO HID reports. It never
// sends authenticator commands, requests presence, or inspects credential data.
package touch

import (
	"encoding/binary"
	"errors"
	"time"
)

// Layout maps input report IDs to their complete on-wire lengths. ID zero means
// an unnumbered report (no leading ID byte in hidraw input).
type Layout map[byte]int

// Descriptor recognizes the FIDO application collection (usage page F1D0,
// usage 1) and its input reports, respecting global push/pop and report IDs.
// Ambiguous reports shared with non-FIDO collections are not monitored.
func Descriptor(data []byte) (Layout, error) {
	bad := errors.New("unsupported or malformed FIDO HID descriptor")
	if len(data) == 0 || len(data) > 4096 {
		return nil, bad
	}
	type globals struct {
		page, size, count uint32
		id                byte
	}
	var g globals
	var stack []globals
	var collections []bool
	var usage uint32
	var hasUsage bool
	bits, fido, other := map[byte]uint32{}, map[byte]bool{}, map[byte]bool{}
	numbered := false
	for i := 0; i < len(data); {
		prefix := data[i]
		i++
		if prefix == 0xfe { // Long items have no defined FIDO meaning; do not guess.
			return nil, bad
		}
		n := int(prefix & 3)
		if n == 3 {
			n = 4
		}
		if i+n > len(data) {
			return nil, bad
		}
		var v uint32
		for j := 0; j < n; j++ {
			v |= uint32(data[i+j]) << (8 * j)
		}
		i += n
		typ, tag := (prefix>>2)&3, prefix>>4
		switch typ {
		case 1: // Global
			switch tag {
			case 0:
				if v > 0xffff {
					return nil, bad
				}
				g.page = v
			case 7:
				g.size = v
			case 8:
				if v == 0 || v > 255 {
					return nil, bad
				}
				g.id = byte(v)
				numbered = true
			case 9:
				g.count = v
			case 10:
				if len(stack) >= 16 {
					return nil, bad
				}
				stack = append(stack, g)
			case 11:
				if len(stack) == 0 {
					return nil, bad
				}
				g = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			}
		case 2: // Local
			if tag == 0 {
				usage = v
				if n < 4 {
					usage |= g.page << 16
				}
				hasUsage = true
			}
		case 0: // Main
			inside := len(collections) > 0 && collections[len(collections)-1]
			switch tag {
			case 10:
				if len(collections) >= 32 {
					return nil, bad
				}
				collections = append(collections, inside || (v == 1 && hasUsage && usage == 0xf1d00001))
			case 12:
				if len(collections) == 0 {
					return nil, bad
				}
				collections = collections[:len(collections)-1]
			case 8:
				if g.size == 0 || g.count == 0 || g.size > 8192 || g.count > 8192 || g.size*g.count > 8192 {
					return nil, bad
				}
				bits[g.id] += g.size * g.count
				if bits[g.id] > 8192 {
					return nil, bad
				}
				if inside && v&1 == 0 {
					fido[g.id] = true
				} else {
					other[g.id] = true
				}
			}
			hasUsage = false
			usage = 0
		}
	}
	if len(collections) != 0 || len(stack) != 0 {
		return nil, bad
	}
	result := Layout{}
	for id, b := range bits {
		if !fido[id] || other[id] || b%8 != 0 || b/8 < 8 {
			continue
		}
		if numbered && id == 0 {
			return nil, bad
		}
		length := int(b / 8)
		if numbered {
			length++
		}
		result[id] = length
	}
	return result, nil
}

const (
	keepalive     = 0xbb
	cborResponse  = 0x90
	errorResponse = 0xbf
	initResponse  = 0x86
	upNeeded      = 2
	processing    = 1
	MaxChannels   = 32
	StaleAfter    = 3 * time.Second
)

// Tracker retains only channel IDs and expiry times, never raw HID payloads.
// Completion closes a notification without claiming successful authentication.
type Tracker struct{ pending map[uint32]time.Time }

func (t *Tracker) Active() bool { return len(t.pending) > 0 }
func (t *Tracker) Report(layout Layout, report []byte, now time.Time) {
	id := byte(0)
	if _, plain := layout[0]; !plain {
		if len(report) == 0 {
			return
		}
		id = report[0]
	}
	size, ok := layout[id]
	if !ok || len(report) != size {
		return
	}
	if id != 0 {
		report = report[1:]
	}
	if len(report) < 8 || report[4]&0x80 == 0 {
		return
	} // continuation, not a header
	cid := binary.BigEndian.Uint32(report[:4])
	if cid == 0 || cid == 0xffffffff {
		return
	}
	command, length := report[4], int(binary.BigEndian.Uint16(report[5:7]))
	if length > len(report)-7+128*(len(report)-5) {
		return
	}
	switch command {
	case keepalive:
		if length != 1 {
			return
		}
		switch report[7] {
		case upNeeded:
			if t.pending == nil {
				t.pending = map[uint32]time.Time{}
			}
			if _, ok := t.pending[cid]; ok || len(t.pending) < MaxChannels {
				t.pending[cid] = now.Add(StaleAfter)
			}
		case processing:
			if _, ok := t.pending[cid]; ok {
				t.pending[cid] = now.Add(StaleAfter)
			}
		}
	case cborResponse:
		if length >= 1 {
			delete(t.pending, cid)
		}
	case errorResponse:
		if length == 1 {
			delete(t.pending, cid)
		}
	case initResponse:
		if length == 17 {
			delete(t.pending, cid)
		} // channel resynchronization
	}
}
func (t *Tracker) Expire(now time.Time) {
	for cid, deadline := range t.pending {
		if !now.Before(deadline) {
			delete(t.pending, cid)
		}
	}
}
