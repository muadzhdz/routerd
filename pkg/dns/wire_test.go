package dns

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// buildMockQuery constructs a simple RFC 1035 DNS wire query for testing
func buildMockQuery(domain string, qtype uint16, tid uint16) []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint16(buf[0:2], tid)
	binary.BigEndian.PutUint16(buf[2:4], 0x0100) // Standard query, RD=1
	binary.BigEndian.PutUint16(buf[4:6], 1)      // QDCOUNT = 1

	// Encode domain labels dynamically
	labels := strings.Split(domain, ".")
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

func TestBuildSinkholeResponse(t *testing.T) {
	// 1. IPv4 Type A Sinkhole -> 0.0.0.0
	queryA := buildMockQuery("googleads.g.doubleclick.net", TypeA, 0xbeef)
	respA := BuildSinkholeResponse(queryA, 0xbeef, TypeA)
	if respA == nil {
		t.Fatal("expected non-nil response for Type A")
	}

	// Verify TID and Flags
	tid := binary.BigEndian.Uint16(respA[0:2])
	flags := binary.BigEndian.Uint16(respA[2:4])
	if tid != 0xbeef {
		t.Errorf("expected TID=0xbeef, got 0x%x", tid)
	}
	if flags&0x8000 == 0 {
		t.Errorf("expected QR=1 (Response), got flags=0x%x", flags)
	}
	if flags&0x000f != 0 {
		t.Errorf("expected RCODE=0 (NOERROR), got flags=0x%x", flags)
	}

	// Verify ANCOUNT = 1
	ancount := binary.BigEndian.Uint16(respA[6:8])
	if ancount != 1 {
		t.Errorf("expected ANCOUNT=1, got %d", ancount)
	}

	// Verify IPv4 Answer is 0.0.0.0 (last 4 bytes of 16-byte answer)
	rdataA := respA[len(respA)-4:]
	if rdataA[0] != 0 || rdataA[1] != 0 || rdataA[2] != 0 || rdataA[3] != 0 {
		t.Errorf("expected 0.0.0.0, got %v", rdataA)
	}

	// 2. IPv6 Type AAAA Sinkhole -> ::
	queryAAAA := buildMockQuery("googleads.g.doubleclick.net", TypeAAAA, 0xcafe)
	respAAAA := BuildSinkholeResponse(queryAAAA, 0xcafe, TypeAAAA)
	if respAAAA == nil {
		t.Fatal("expected non-nil response for Type AAAA")
	}
	ancountAAAA := binary.BigEndian.Uint16(respAAAA[6:8])
	if ancountAAAA != 1 {
		t.Errorf("expected ANCOUNT=1 for AAAA, got %d", ancountAAAA)
	}
	rdataAAAA := respAAAA[len(respAAAA)-16:]
	for i, b := range rdataAAAA {
		if b != 0 {
			t.Errorf("expected zero byte at %d, got %d", i, b)
		}
	}

	// 3. Other QTYPE (e.g. 65 - HTTPS) -> Empty NOERROR
	queryHTTPS := buildMockQuery("googleads.g.doubleclick.net", 65, 0x1234)
	respHTTPS := BuildSinkholeResponse(queryHTTPS, 0x1234, 65)
	if respHTTPS == nil {
		t.Fatal("expected non-nil response for HTTPS")
	}
	ancountHTTPS := binary.BigEndian.Uint16(respHTTPS[6:8])
	if ancountHTTPS != 0 {
		t.Errorf("expected ANCOUNT=0 for HTTPS query, got %d", ancountHTTPS)
	}

	// 4. Short/invalid query
	shortResp := BuildSinkholeResponse([]byte{0x01, 0x02}, 0x1111, TypeA)
	if shortResp != nil {
		t.Errorf("expected nil for short query, got %v", shortResp)
	}
}

func TestBuildServFailResponse(t *testing.T) {
	query := buildMockQuery("unreachable-upstream.example.com", TypeA, 0x55aa)
	resp := BuildServFailResponse(query, 0x55aa)
	if resp == nil {
		t.Fatal("expected non-nil response for ServFail")
	}

	tid := binary.BigEndian.Uint16(resp[0:2])
	if tid != 0x55aa {
		t.Errorf("expected TID=0x55aa, got 0x%x", tid)
	}

	flags := binary.BigEndian.Uint16(resp[2:4])
	rcode := flags & 0x000f
	if rcode != 2 { // 2 == ServFail
		t.Errorf("expected RCODE=2 (ServFail), got %d", rcode)
	}

	ancount := binary.BigEndian.Uint16(resp[6:8])
	if ancount != 0 {
		t.Errorf("expected ANCOUNT=0 for ServFail, got %d", ancount)
	}

	// Short query
	if BuildServFailResponse([]byte{1, 2}, 0x12) != nil {
		t.Error("expected nil for too short query")
	}
}


