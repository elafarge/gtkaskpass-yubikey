// SPDX-License-Identifier: Apache-2.0
package cache

import (
	"sync"
	"time"

	"github.com/elafarge/gtkaskpass-yubikey/internal/askpass"
)

const (
	MaxKeys        = 256
	MaxCallers     = 4096
	MaxTickets     = 256
	TicketLifetime = 10 * time.Minute
)

type entry struct {
	identity   Identity
	generation uint64
	secret     []byte
	expires    time.Duration
}
type ticket struct {
	identity   Identity
	generation uint64
	caller     string
	expires    time.Duration
}
type seenKey struct {
	caller string
	key    Key
}

// Store owns all retained credential buffers. All returned buffers are copies
// owned (and cleared) by the caller. Methods are safe for concurrent clients.
type Store struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Duration
	seq     uint64
	entries map[Key]*entry
	tickets map[uint64]ticket
	seen    map[seenKey]uint64
}

type Lookup struct {
	Generation uint64
	Secret     []byte
	Token      uint64
	TTL        time.Duration
	Reason     string
}

func New(ttl time.Duration, now func() time.Duration) *Store {
	return &Store{ttl: ttl, now: now, entries: make(map[Key]*entry), tickets: make(map[uint64]ticket), seen: make(map[seenKey]uint64)}
}

func (s *Store) next() uint64   { s.seq++; return s.seq }
func (s *Store) erase(e *entry) { clear(e.secret); e.secret = nil; e.generation = s.next() }

func (s *Store) Begin(id Identity, caller string) Lookup {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	r := Lookup{Reason: "disabled"}
	if s.ttl <= 0 {
		return r
	}
	sk := seenKey{caller, id.Key}
	previous, retry := s.seen[sk]
	// OpenSSH ssh-agent asks for a PIN at most once per signing operation.
	// Another fingerprint prompt from that long-lived caller is a new operation,
	// not a same-operation retry. File-based requests retain retry protection.
	trackRetry := !id.AgentPIN()
	retry = retry && trackRetry
	if (trackRetry && !retry && len(s.seen) >= MaxCallers) || len(s.tickets) >= MaxTickets {
		r.Reason = "capacity"
		return r
	}
	e := s.entries[id.Key]
	if e == nil {
		if len(s.entries) >= MaxKeys {
			r.Reason = "capacity"
			return r
		}
		e = &entry{identity: id, generation: s.next()}
		s.entries[id.Key] = e
	}
	r.Reason = "miss"
	if e.identity != id {
		s.erase(e)
		e.identity = id
		r.Reason = "file-changed"
	}
	if retry {
		if previous == e.generation {
			s.erase(e)
		}
		r.Reason = "retry"
	} else if len(e.secret) > 0 {
		if trackRetry {
			s.seen[sk] = e.generation
		}
		r.Secret = append([]byte(nil), e.secret...)
		r.Generation = e.generation
		r.Reason = "hit"
		return r
	}
	if trackRetry {
		s.seen[sk] = 0
	} // reserve retry tracking even if the dialog is cancelled
	r.Token, r.TTL = s.next(), s.ttl
	s.tickets[r.Token] = ticket{id, e.generation, caller, s.now() + TicketLifetime}
	return r
}

// Current and Reject guard in-flight verification against concurrent forgetting.
func (s *Store) Current(id Identity, generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	e := s.entries[id.Key]
	return e != nil && e.identity == id && e.generation == generation && len(e.secret) > 0
}
func (s *Store) Reject(id Identity, generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[id.Key]; e != nil && e.identity == id && e.generation == generation {
		s.erase(e)
	}
}

// Commit accepts only the current file/version and a still-current lease.
// Forget, another submission, expiry, or file changes invalidate old leases.
func (s *Store) Commit(token uint64, id Identity, caller string, secret []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	t, ok := s.tickets[token]
	if !ok || t.caller != caller {
		return false
	}
	delete(s.tickets, token)
	e := s.entries[id.Key]
	if e == nil || t.identity != id || e.identity != id || t.generation != e.generation || len(secret) == 0 || askpass.Validate(string(secret)) != nil {
		return false
	}
	s.erase(e)
	e.secret = append([]byte(nil), secret...)
	e.expires = s.now() + s.ttl
	if !id.AgentPIN() {
		s.seen[seenKey{caller, id.Key}] = e.generation
	}
	return true
}

func (s *Store) Forget(key *Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.entries {
		if key == nil || *key == k {
			s.erase(e)
			delete(s.entries, k)
		}
	}
	for tok, t := range s.tickets {
		if key == nil || *key == t.identity.Key {
			delete(s.tickets, tok)
		}
	}
}

func (s *Store) expire() {
	now := s.now()
	for _, e := range s.entries {
		if len(e.secret) > 0 && now >= e.expires {
			s.erase(e)
		}
	}
	for tok, t := range s.tickets {
		if now >= t.expires {
			delete(s.tickets, tok)
		}
	}
}

// Sweep proactively expires secrets and prunes dead callers and unused slots.
func (s *Store) Sweep(alive func(string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	for sk := range s.seen {
		if !alive(sk.caller) {
			delete(s.seen, sk)
		}
	}
	for tok, t := range s.tickets {
		if !alive(t.caller) {
			delete(s.tickets, tok)
		}
	}
	for k, e := range s.entries {
		if len(e.secret) > 0 {
			continue
		}
		pending := false
		for _, t := range s.tickets {
			if t.identity.Key == k {
				pending = true
				break
			}
		}
		if !pending {
			delete(s.entries, k)
		}
	}
}
