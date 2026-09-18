package cache

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestAgentPINLifetimeAndIsolation(t *testing.T) {
	now := time.Duration(0)
	s := New(time.Hour, func() time.Duration { return now })
	k := Key{Kind: "pin", Fingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))}
	id, err := Resolve(k)
	if err != nil {
		t.Fatal(err)
	}
	first := s.Begin(id, "persistent-agent")
	if !s.Commit(first.Token, id, "persistent-agent", []byte("synthetic-pin")) {
		t.Fatal("store failed")
	}
	for i := 0; i < 3; i++ {
		r := s.Begin(id, "persistent-agent")
		if r.Reason != "hit" || string(r.Secret) != "synthetic-pin" {
			t.Fatal("new signing operation treated as retry", r.Reason)
		}
		clear(r.Secret)
	}
	if len(s.seen) != 0 {
		t.Fatal("agent operations consumed retry tracking")
	}
	other := id
	other.Fingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString([]byte("another distinct 32-byte identity"))
	if r := s.Begin(other, "persistent-agent"); len(r.Secret) != 0 {
		t.Fatal("PIN crossed key identities")
	}
	file := Identity{Key: Key{Path: "/a/key", Kind: "pin"}, Stamp: "v1"}
	if r := s.Begin(file, "persistent-agent"); len(r.Secret) != 0 {
		t.Fatal("fingerprint confused with file")
	}
	now = time.Hour
	r := s.Begin(id, "persistent-agent")
	if len(r.Secret) != 0 || r.Token == 0 {
		t.Fatal("agent hits refreshed TTL")
	}
	s.Forget(&k)
	if s.Commit(r.Token, id, "persistent-agent", []byte("stale")) {
		t.Fatal("forgotten agent PIN resurrected")
	}
}

func TestFingerprintValidation(t *testing.T) {
	fp := "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	for _, k := range []Key{
		{Kind: "pin", Fingerprint: "SHA256:short"},
		{Kind: "pin", Fingerprint: fp + "="},
		{Kind: "passphrase", Fingerprint: fp},
		{Kind: "pin", Path: "/key", Fingerprint: fp},
	} {
		if _, err := Resolve(k); err == nil {
			t.Fatal("invalid identity accepted", k)
		}
	}
}
