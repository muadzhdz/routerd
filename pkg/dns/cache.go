package dns

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
)

type cacheEntry struct {
	rawResponse []byte
	expiresAt   time.Time
}

// Cache stores DNS responses in memory with TTL enforcement and Transaction ID (TID) rewriting.
type Cache struct {
	mu         sync.RWMutex
	entries    map[string]cacheEntry
	maxEntries int
}

// NewCache creates a bounded cache instance.
func NewCache(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = 2048
	}
	return &Cache{
		entries:    make(map[string]cacheEntry, maxEntries),
		maxEntries: maxEntries,
	}
}

// CacheKey generates a cache key from domain and QType.
func CacheKey(domain string, qtype uint16) string {
	return fmt.Sprintf("%s:%d", domain, qtype)
}

// Get retrieves a response from cache if not expired, rewriting the client's new TID at offset 0-1.
func (c *Cache) Get(key string, newTID uint16) ([]byte, bool) {
	c.mu.RLock()
	entry, found := c.entries[key]
	c.mu.RUnlock()

	if !found {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false
	}

	// RFC 1035 Invariant: Duplicate response and rewrite the client's TID so client accepts the answer
	respCopy := make([]byte, len(entry.rawResponse))
	copy(respCopy, entry.rawResponse)
	binary.BigEndian.PutUint16(respCopy[0:2], newTID)

	return respCopy, true
}

// Set stores a DNS response into cache with the specified TTL.
func (c *Cache) Set(key string, rawResponse []byte, ttl time.Duration) {
	if len(rawResponse) < 12 || ttl <= 0 {
		return
	}

	respCopy := make([]byte, len(rawResponse))
	copy(respCopy, rawResponse)

	c.mu.Lock()
	defer c.mu.Unlock()

	// If at capacity, evict expired entries or an arbitrary entry
	if len(c.entries) >= c.maxEntries {
		now := time.Now()
		evicted := false
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
				evicted = true
				break
			}
		}
		if !evicted {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
	}

	c.entries[key] = cacheEntry{
		rawResponse: respCopy,
		expiresAt:   time.Now().Add(ttl),
	}
}

// Len returns the count of active cache entries.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Prune removes all expired entries from cache.
func (c *Cache) Prune() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	removed := 0
	for k, v := range c.entries {
		if now.After(v.expiresAt) {
			delete(c.entries, k)
			removed++
		}
	}
	return removed
}
