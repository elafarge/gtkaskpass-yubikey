package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestExpiryRetryAndGenerations(t *testing.T) {
	now := time.Duration(0)
	s := New(time.Hour, func() time.Duration { return now })
	id := Identity{Key{"/key", "pin"}, "v1"}
	l := s.Begin(id, "a")
	if !s.Commit(l.Token, id, "a", []byte("123")) {
		t.Fatal("commit")
	}
	buf := s.entries[id.Key].secret
	now = 59 * time.Minute
	if r := s.Begin(id, "b"); string(r.Secret) != "123" {
		t.Fatal(r)
	}
	now = time.Hour
	if r := s.Begin(id, "c"); r.Secret != nil {
		t.Fatal("sliding expiration", r)
	}
	for _, b := range buf {
		if b != 0 {
			t.Fatal("not wiped")
		}
	}
	l = s.Begin(id, "d")
	if !s.Commit(l.Token, id, "d", []byte("bad")) {
		t.Fatal("commit")
	}
	r := s.Begin(id, "d")
	if r.Reason != "retry" || len(r.Secret) != 0 {
		t.Fatal(r)
	}
	if !s.Commit(r.Token, id, "d", []byte("good")) {
		t.Fatal("retry commit")
	}
	if r := s.Begin(id, "e"); string(r.Secret) != "good" {
		t.Fatal(r)
	}
	// A stale caller must not erase another caller's newer answer.
	r = s.Begin(id, "d")
	if !s.Commit(r.Token, id, "d", []byte("new")) {
		t.Fatal("new commit")
	}
	s.Begin(id, "e")
	if r := s.Begin(id, "f"); string(r.Secret) != "new" {
		t.Fatal("stale retry deleted new value", r)
	}
}

func TestForgetAndConcurrentStores(t *testing.T) {
	s := New(time.Hour, func() time.Duration { return 0 })
	id := Identity{Key{"/key", "passphrase"}, "v1"}
	a, b := s.Begin(id, "a"), s.Begin(id, "b")
	if !s.Commit(a.Token, id, "a", []byte("one")) || s.Commit(b.Token, id, "b", []byte("two")) {
		t.Fatal("stale store")
	}
	s.Forget(nil)
	l := s.Begin(id, "c")
	s.Forget(&id.Key)
	if s.Commit(l.Token, id, "c", []byte("resurrect")) {
		t.Fatal("resurrection")
	}
	l = s.Begin(id, "d")
	changed := id
	changed.Stamp = "v2"
	if s.Commit(l.Token, changed, "d", []byte("changed")) {
		t.Fatal("changed file accepted")
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := s.Begin(id, fmt.Sprint(i))
			s.Commit(r.Token, id, fmt.Sprint(i), []byte("parallel"))
		}(i)
	}
	wg.Wait()
	s.Forget(nil)
}

func TestNamespacesDisableAndLimits(t *testing.T) {
	s := New(0, func() time.Duration { return 0 })
	id := Identity{Key{"/key", "pin"}, "v1"}
	if l := s.Begin(id, "a"); l.Token != 0 || l.TTL != 0 {
		t.Fatal(l)
	}
	s = New(time.Hour, func() time.Duration { return 0 })
	l := s.Begin(id, "a")
	s.Commit(l.Token, id, "a", []byte("pin"))
	id.Kind = "passphrase"
	if l := s.Begin(id, "b"); len(l.Secret) != 0 {
		t.Fatal("mixed kinds")
	}
	for i := 0; i < MaxTickets; i++ {
		s.Begin(id, fmt.Sprint(i))
	}
	if l := s.Begin(id, "overflow"); l.Reason != "capacity" {
		t.Fatal(l)
	}
	s.Sweep(func(string) bool { return false })
	if l := s.Begin(id, "fresh"); l.Token == 0 {
		t.Fatal("no capacity recovered", l)
	}
}

func TestIdentity(t *testing.T) {
	p := filepath.Join(t.TempDir(), "key with spaces")
	if err := os.WriteFile(p, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := Resolve(Key{p, "pin"})
	if err != nil {
		t.Fatal(err)
	}
	link := p + "-link"
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	b, err := Resolve(Key{link, "pin"})
	if err != nil || a != b {
		t.Fatal(a, b, err)
	}
	if err := os.WriteFile(p, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	b, _ = Resolve(Key{p, "pin"})
	if a == b {
		t.Fatal("modification missed")
	}
}
