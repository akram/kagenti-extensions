// Package shared provides a generic, process-scoped, TTL key→value store
// that plugins reach via pipeline.Context.Shared. It is intentionally
// semantics-free — feature-specific conventions (e.g. credential
// placeholders) live in their own packages and namespace their keys.
package shared

import (
	"sync"
	"time"
)

type entry struct {
	val     any
	expires time.Time
}

// Store is a thread-safe TTL map. The zero value is not usable; call New.
type Store struct {
	mu    sync.RWMutex
	items map[string]entry
	now   func() time.Time // injectable for tests
}

// New returns an empty Store.
func New() *Store {
	return &Store{items: make(map[string]entry), now: time.Now}
}

// Put stores val under key with the given time-to-live.
func (s *Store) Put(key string, val any, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[key] = entry{val: val, expires: s.now().Add(ttl)}
}

// Get returns the value for key if present and unexpired. Expired entries
// are evicted lazily.
func (s *Store) Get(key string) (any, bool) {
	s.mu.RLock()
	e, ok := s.items[key]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if s.now().After(e.expires) {
		s.Delete(key)
		return nil, false
	}
	return e.val, true
}

// Delete removes key.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	delete(s.items, key)
	s.mu.Unlock()
}
