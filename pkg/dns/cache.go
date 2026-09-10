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

// Cache menyimpan respons DNS di memori RAM dengan penegakan TTL dan penulisan ulang Transaction ID (TID).
type Cache struct {
	mu         sync.RWMutex
	entries    map[string]cacheEntry
	maxEntries int
}

// NewCache membuat instance cache berbatas.
func NewCache(maxEntries int) *Cache {
	if maxEntries <= 0 {
		maxEntries = 2048
	}
	return &Cache{
		entries:    make(map[string]cacheEntry, maxEntries),
		maxEntries: maxEntries,
	}
}

// CacheKey menghasilkan kunci cache dari domain dan QType.
func CacheKey(domain string, qtype uint16) string {
	return fmt.Sprintf("%s:%d", domain, qtype)
}

// Get mengambil respons dari cache jika masih berlaku, dan menulis ulang TID client baru ke offset 0-1.
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

	// Invariant RFC 1035: Duplikasi respons dan tulis ulang TID baru agar client menerima TID yang sesuai
	respCopy := make([]byte, len(entry.rawResponse))
	copy(respCopy, entry.rawResponse)
	binary.BigEndian.PutUint16(respCopy[0:2], newTID)

	return respCopy, true
}

// Set menyimpan respons DNS ke dalam cache dengan TTL tertentu.
func (c *Cache) Set(key string, rawResponse []byte, ttl time.Duration) {
	if len(rawResponse) < 12 || ttl <= 0 {
		return
	}

	respCopy := make([]byte, len(rawResponse))
	copy(respCopy, rawResponse)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Jika kapasitas penuh, buang entri kedaluwarsa atau entri acak
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
			// Jika belum ada yang kedaluwarsa, buang satu entri sembarang
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

// Len mengembalikan jumlah entri aktif di cache.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Prune membersihkan semua entri yang sudah lewat masa berlakunya.
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
