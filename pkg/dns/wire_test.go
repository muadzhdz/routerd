package dns

import (
	"encoding/binary"
	"testing"
	"time"
)

// buildMockQuery constructs a simple RFC 1035 DNS wire query for testing
func buildMockQuery(domain string, qtype uint16, tid uint16) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint16(buf[0:2], tid)
	binary.BigEndian.PutUint16(buf[2:4], 0x0100) // Standard query, RD=1
	binary.BigEndian.PutUint16(buf[4:6], 1)      // QDCOUNT = 1

	// Encode domain labels: "google.com" -> \x06google\x03com\x00
	labels := []string{"google", "com"}
	if domain == "api.github.com" {
		labels = []string{"api", "github", "com"}
	}
	for _, l := range labels {
		buf = append(buf, byte(len(l)))
		buf = append(buf, []byte(l)...)
	}
	buf = append(buf, 0x00) // Root null terminator

	// QTYPE & QCLASS (IN = 1)
	typeClass := make([]byte, 4)
	binary.BigEndian.PutUint16(typeClass[0:2], qtype)
	binary.BigEndian.PutUint16(typeClass[2:4], 1)
	buf = append(buf, typeClass...)

	return buf
}

// buildMockResponse constructs an RFC 1035 DNS response with an Answer section and specified TTL
func buildMockResponse(domain string, qtype uint16, tid uint16, ttl uint32) []byte {
	query := buildMockQuery(domain, qtype, tid)
	// Set QR=1 in flags (response)
	binary.BigEndian.PutUint16(query[2:4], 0x8180)
	// Set ANCOUNT = 1
	binary.BigEndian.PutUint16(query[6:8], 1)

	// Add 1 Resource Record to Answer section:
	// Compression pointer to offset 12 (0xC00C)
	ans := []byte{0xC0, 0x0C}

	rest := make([]byte, 14)
	binary.BigEndian.PutUint16(rest[0:2], qtype) // TYPE
	binary.BigEndian.PutUint16(rest[2:4], 1)     // CLASS IN
	binary.BigEndian.PutUint32(rest[4:8], ttl)   // TTL
	binary.BigEndian.PutUint16(rest[8:10], 4)    // RDLENGTH = 4 bytes (IPv4)
	copy(rest[10:14], []byte{8, 8, 8, 8})       // RDATA

	query = append(query, ans...)
	query = append(query, rest...)
	return query
}

func TestExtractQuestion(t *testing.T) {
	pkt := buildMockQuery("google.com", 1, 0x1234)
	domain, qtype, err := ExtractQuestion(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if domain != "google.com" {
		t.Errorf("expected domain=google.com, got %s", domain)
	}
	if qtype != 1 {
		t.Errorf("expected qtype=1, got %d", qtype)
	}

	// Short packet
	_, _, err = ExtractQuestion([]byte{0x01, 0x02})
	if err == nil {
		t.Error("expected error for short packet, got nil")
	}
}

func TestExtractTTL(t *testing.T) {
	// 1. Normal TTL = 300s
	resp1 := buildMockResponse("google.com", 1, 0x1234, 300)
	ttl1 := ExtractTTL(resp1)
	if ttl1 != 300*time.Second {
		t.Errorf("expected 300s, got %v", ttl1)
	}

	// 2. Clamped Min TTL (input 2s -> clamp to 10s)
	resp2 := buildMockResponse("google.com", 1, 0x1234, 2)
	ttl2 := ExtractTTL(resp2)
	if ttl2 != minTTL {
		t.Errorf("expected minTTL (%v), got %v", minTTL, ttl2)
	}

	// 3. Clamped Max TTL (input 200,000s -> clamp to 24h)
	resp3 := buildMockResponse("google.com", 1, 0x1234, 200000)
	ttl3 := ExtractTTL(resp3)
	if ttl3 != maxTTL {
		t.Errorf("expected maxTTL (%v), got %v", maxTTL, ttl3)
	}

	// 4. No answers -> default TTL 60s
	queryOnly := buildMockQuery("google.com", 1, 0x1234)
	ttl4 := ExtractTTL(queryOnly)
	if ttl4 != defaultTTL {
		t.Errorf("expected defaultTTL (%v), got %v", defaultTTL, ttl4)
	}
}
