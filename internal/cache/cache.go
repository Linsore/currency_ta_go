// internal/cache/cache.go
//
// Package cache provides a tiny, generic in-memory cache with a fixed TTL
// applied per entry. It is concurrency-safe and intended for simple, small
// datasets where a distributed cache is unnecessary.
//
// Design notes:
//   - Each Set() stamps an absolute expiration time (now + ttl).
//   - Get() returns (zero, false) for missing/expired entries and lazily
//     deletes expired items to keep the map small.
//   - Delete() removes a key explicitly; Flush() clears all entries.
//   - This cache does not perform background eviction or size limiting.

package cache

import (
	"sync"
	"time"
)

// entry stores a cached value and its expiration timestamp.
type entry[V any] struct {
	value      V
	expiresAt  time.Time
}

// Cache is a generic, concurrency-safe TTL cache keyed by type K and storing V.
// The zero value of Cache is not ready to use; construct with New().
type Cache[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]entry[V]
	ttl  time.Duration
}

// New creates a new Cache with the specified TTL for all entries.
func New[K comparable, V any](ttl time.Duration) *Cache[K, V] {
	return &Cache[K, V]{
		data: make(map[K]entry[V]),
		ttl:  ttl,
	}
}

// Set stores value v under key k and sets its expiration to now + ttl.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	c.data[key] = entry[V]{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// Get returns the cached value and true if present and not expired.
// If the entry has expired, it is removed and (zero, false) is returned.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	// Fast read lock for the common path.
	c.mu.RLock()
	e, ok := c.data[key]
	c.mu.RUnlock()

	// Miss: not present.
	if !ok {
		var zero V
		return zero, false
	}

	// Expired: remove lazily and report miss.
	if time.Now().After(e.expiresAt) {
		c.Delete(key)
		var zero V
		return zero, false
	}

	return e.value, true
}

// Delete removes the key from the cache if present.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	delete(c.data, key)
	c.mu.Unlock()
}

// Flush removes all entries from the cache.
func (c *Cache[K, V]) Flush() {
	c.mu.Lock()
	c.data = make(map[K]entry[V])
	c.mu.Unlock()
}
