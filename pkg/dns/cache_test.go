package dns

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestCacheTIDRewriting(t *testing.T) {
	c := NewCache(100)

	// Store response with initial TID 0x1111
	originalResp := buildMockResponse("google.com", 1, 0x1111, 60)
	key := CacheKey("google.com", 1)

	c.Set(key, originalResp, 30*time.Second)

	// New client queries the same domain with TID 0x9999
	newTID := uint16(0x9999)
	hitResp, hit := c.Get(key, newTID)
	if !hit {
		t.Fatal("expected cache hit, got miss")
	}

	gotTID := binary.BigEndian.Uint16(hitResp[0:2])
	if gotTID != newTID {
		t.Fatalf("expected rewritten TID=0x%04X, got 0x%04X", newTID, gotTID)
	}

	// Ensure the raw response inside cache was not mutated permanently
	secondTID := uint16(0xABCD)
	secondHit, _ := c.Get(key, secondTID)
	gotSecondTID := binary.BigEndian.Uint16(secondHit[0:2])
	if gotSecondTID != secondTID {
		t.Fatalf("expected rewritten TID=0x%04X, got 0x%04X", secondTID, gotSecondTID)
	}
}

func TestCacheExpirationAndEviction(t *testing.T) {
	c := NewCache(2)

	resp := buildMockResponse("test.com", 1, 0x1234, 10)

	// Store with short TTL 50ms
	c.Set("short:1", resp, 50*time.Millisecond)

	// Verify immediate hit
	if _, hit := c.Get("short:1", 0x1234); !hit {
		t.Fatal("expected immediate hit")
	}

	// Wait 70ms until expiration
	time.Sleep(70 * time.Millisecond)

	if _, hit := c.Get("short:1", 0x1234); hit {
		t.Fatal("expected cache miss after expiration")
	}

	// Test capacity limit (maxEntries = 2)
	c.Set("key1:1", resp, 10*time.Minute)
	c.Set("key2:1", resp, 10*time.Minute)
	c.Set("key3:1", resp, 10*time.Minute)

	if c.Len() > 2 {
		t.Errorf("cache exceeded max capacity 2, got len=%d", c.Len())
	}
}
