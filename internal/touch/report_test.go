package touch

import (
	"encoding/binary"
	"testing"
	"time"
)

// Standard FIDO application: 64-byte input/output reports, no report ID.
var descriptor = []byte{0x06, 0xd0, 0xf1, 0x09, 1, 0xa1, 1, 0x09, 0x20, 0x15, 0, 0x26, 0xff, 0, 0x75, 8, 0x95, 64, 0x81, 2, 0x09, 0x21, 0x95, 64, 0x91, 2, 0xc0}

func packet(cid uint32, cmd byte, length uint16, status byte) []byte {
	p := make([]byte, 64)
	binary.BigEndian.PutUint32(p, cid)
	p[4] = cmd
	binary.BigEndian.PutUint16(p[5:7], length)
	p[7] = status
	return p
}
func TestDescriptor(t *testing.T) {
	l, e := Descriptor(descriptor)
	if e != nil || l[0] != 64 {
		t.Fatal(l, e)
	}
	numbered := append([]byte{0x85, 7}, descriptor...)
	l, e = Descriptor(numbered)
	if e != nil || l[7] != 65 {
		t.Fatal(l, e)
	}
	keyboard := append([]byte(nil), descriptor...)
	keyboard[1] = 1
	keyboard[2] = 0
	l, e = Descriptor(keyboard)
	if e != nil || len(l) != 0 {
		t.Fatal("non-FIDO monitored", l, e)
	}
	for _, d := range [][]byte{descriptor[:len(descriptor)-1], {0x06, 0xd0}, {0xb4}, {0xc0}, {0xfe, 0, 0}, append([]byte{0x85, 0}, descriptor...)} {
		if _, e := Descriptor(d); e == nil {
			t.Fatal("malformed descriptor accepted", d)
		}
	}
	// A report ID shared with a non-FIDO application must not be interpreted.
	mixed := append(append([]byte(nil), descriptor...), keyboard...)
	l, e = Descriptor(mixed)
	if e != nil || len(l) != 0 {
		t.Fatal("ambiguous report accepted", l, e)
	}
}
func TestTrackerChannelsAndCompletion(t *testing.T) {
	l, _ := Descriptor(descriptor)
	var tr Tracker
	now := time.Unix(0, 0)
	tr.Report(l, packet(1, keepalive, 1, upNeeded), now)
	if !tr.Active() {
		t.Fatal("touch not detected")
	}
	for _, p := range [][]byte{packet(2, cborResponse, 1, 0), packet(1, 0x81, 1, 0), packet(1, 0, 1, 0), packet(1, keepalive, 2, processing), packet(1, errorResponse, 2, 0)} {
		tr.Report(l, p, now)
	}
	if !tr.Active() {
		t.Fatal("unrelated or malformed report dismissed operation")
	}
	tr.Report(l, packet(2, keepalive, 1, upNeeded), now)
	tr.Report(l, packet(1, cborResponse, 100, 0), now) // response begins; payload not decoded
	if !tr.Active() {
		t.Fatal("other active channel discarded")
	}
	tr.Report(l, packet(2, errorResponse, 1, 0x2d), now)
	if tr.Active() {
		t.Fatal("error completion left popup")
	}
	tr.Report(l, packet(3, keepalive, 1, processing), now)
	if tr.Active() {
		t.Fatal("processing without presence started popup")
	}
	tr.Report(l, packet(3, keepalive, 1, upNeeded), now)
	tr.Report(l, packet(3, initResponse, 17, 0), now)
	if tr.Active() {
		t.Fatal("channel reset left popup")
	}
}
func TestExpiryAndBounds(t *testing.T) {
	var tr Tracker
	l := Layout{0: 64}
	now := time.Unix(0, 0)
	for cid := uint32(0); cid < 1000; cid++ {
		tr.Report(l, packet(cid, keepalive, 1, upNeeded), now)
	}
	if len(tr.pending) != MaxChannels {
		t.Fatal(len(tr.pending))
	}
	tr.Report(l, packet(1, keepalive, 1, processing), now.Add(time.Second))
	tr.Expire(now.Add(StaleAfter))
	if len(tr.pending) != 1 {
		t.Fatal("processing didn't extend active channel", len(tr.pending))
	}
	tr.Expire(now.Add(StaleAfter + time.Second))
	if tr.Active() {
		t.Fatal("stale popup")
	}
	tr.Report(l, packet(0xffffffff, keepalive, 1, upNeeded), now)
	tr.Report(l, packet(1, keepalive, 1, upNeeded)[:8], now)
	if tr.Active() {
		t.Fatal("broadcast/short packet accepted")
	}
}
func TestNumberedReport(t *testing.T) {
	var tr Tracker
	l := Layout{7: 65}
	now := time.Now()
	p := append([]byte{7}, packet(42, keepalive, 1, upNeeded)...)
	tr.Report(l, p, now)
	if !tr.Active() {
		t.Fatal("numbered report missed")
	}
	p[0] = 8
	p[5] = errorResponse
	tr.Report(l, p, now)
	if !tr.Active() {
		t.Fatal("different report ID affected tracker")
	}
}
func FuzzDescriptor(f *testing.F) {
	f.Add(descriptor)
	f.Add([]byte{0xfe})
	f.Add([]byte{0x85, 7})
	f.Fuzz(func(t *testing.T, b []byte) {
		l, _ := Descriptor(b)
		for _, size := range l {
			if size < 8 || size > 1025 {
				t.Fatal(size)
			}
		}
	})
}
func FuzzReports(f *testing.F) {
	f.Add(packet(1, keepalive, 1, upNeeded))
	f.Fuzz(func(t *testing.T, b []byte) {
		var tr Tracker
		tr.Report(Layout{0: 64}, b, time.Unix(0, 0))
		tr.Expire(time.Unix(10, 0))
		if tr.Active() {
			t.Fatal("stale state")
		}
	})
}
