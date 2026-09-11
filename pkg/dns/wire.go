package dns

import (
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

const (
	minTTL     = 10 * time.Second
	maxTTL     = 24 * time.Hour
	defaultTTL = 60 * time.Second
)

// ExtractQuestion extracts the domain name (QName) and query type (QType) from an RFC 1035 DNS wire payload.
func ExtractQuestion(data []byte) (string, uint16, error) {
	if len(data) < 12 {
		return "", 0, errors.New("DNS packet too short (<12 bytes)")
	}

	offset := 12
	var labels []string

	for offset < len(data) {
		length := int(data[offset])
		if length == 0 {
			offset++
			break
		}
		if length >= 192 { // Compression pointer 0xC0
			offset += 2
			break
		}
		if length > 63 || offset+1+length > len(data) {
			return "", 0, errors.New("invalid DNS label format")
		}
		labels = append(labels, string(data[offset+1:offset+1+length]))
		offset += 1 + length
	}

	if offset+4 > len(data) {
		return "", 0, errors.New("missing QType/QClass in Question section")
	}

	qtype := binary.BigEndian.Uint16(data[offset : offset+2])
	domain := strings.ToLower(strings.Join(labels, "."))
	if domain == "" {
		domain = "."
	}

	return domain, qtype, nil
}

// ExtractTTL reads the minimum TTL value from the Answer section in a DNS response.
func ExtractTTL(resp []byte) time.Duration {
	if len(resp) < 12 {
		return defaultTTL
	}

	ancount := binary.BigEndian.Uint16(resp[6:8])
	if ancount == 0 {
		return defaultTTL
	}

	qdcount := binary.BigEndian.Uint16(resp[4:6])
	offset := 12

	// Skip all records in the Question section
	for i := 0; i < int(qdcount); i++ {
		for offset < len(resp) {
			length := int(resp[offset])
			if length == 0 {
				offset++
				break
			}
			if length >= 192 { // 0xC0 compression pointer
				offset += 2
				break
			}
			offset += 1 + length
		}
		offset += 4 // QType (2) + QClass (2)
		if offset > len(resp) {
			return defaultTTL
		}
	}

	// Read first Answer section record
	if offset >= len(resp) {
		return defaultTTL
	}

	// Skip NAME in Answer (compression pointer or labels)
	if resp[offset] >= 192 {
		offset += 2
	} else {
		for offset < len(resp) {
			l := int(resp[offset])
			if l == 0 {
				offset++
				break
			}
			offset += 1 + l
		}
	}

	// TYPE (2) + CLASS (2) + TTL (4)
	if offset+8 > len(resp) {
		return defaultTTL
	}

	offset += 4 // Skip TYPE & CLASS
	rawTTL := binary.BigEndian.Uint32(resp[offset : offset+4])
	ttl := time.Duration(rawTTL) * time.Second

	if ttl < minTTL {
		return minTTL
	}
	if ttl > maxTTL {
		return maxTTL
	}
	return ttl
}

// DNS Resource Record Types
const (
	TypeA    uint16 = 1
	TypeAAAA uint16 = 28
)

// BuildSinkholeResponse constructs an RFC 1035 wire response that resolves
// blocked domains to 0.0.0.0 (for IPv4 Type A) or :: (for IPv6 Type AAAA),
// or returns an empty NOERROR answer for other record types (HTTPS, TXT, etc.).
func BuildSinkholeResponse(query []byte, clientTID uint16, qtype uint16) []byte {
	if len(query) < 12 {
		return nil
	}

	// Locate the end of the Question section in the query
	offset := 12
	for offset < len(query) {
		length := int(query[offset])
		if length == 0 {
			offset++
			break
		}
		if length >= 192 { // Compression pointer
			offset += 2
			break
		}
		offset += 1 + length
	}

	// Include QTYPE (2) + QCLASS (2)
	questionEnd := offset + 4
	if questionEnd > len(query) {
		questionEnd = len(query)
	}
	questionSection := query[12:questionEnd]

	// Determine answer count and answer size based on QTYPE
	var ancount uint16
	var answerBytes []byte

	switch qtype {
	case TypeA:
		ancount = 1
		answerBytes = make([]byte, 16)
		binary.BigEndian.PutUint16(answerBytes[0:2], 0xc00c)  // Name pointer -> offset 12
		binary.BigEndian.PutUint16(answerBytes[2:4], TypeA)   // Type A
		binary.BigEndian.PutUint16(answerBytes[4:6], 1)       // Class IN
		binary.BigEndian.PutUint32(answerBytes[6:10], 60)     // TTL: 60 seconds
		binary.BigEndian.PutUint16(answerBytes[10:12], 4)     // RDLENGTH: 4 bytes
		copy(answerBytes[12:16], []byte{0, 0, 0, 0})          // IP: 0.0.0.0

	case TypeAAAA:
		ancount = 1
		answerBytes = make([]byte, 28)
		binary.BigEndian.PutUint16(answerBytes[0:2], 0xc00c)  // Name pointer -> offset 12
		binary.BigEndian.PutUint16(answerBytes[2:4], TypeAAAA)// Type AAAA
		binary.BigEndian.PutUint16(answerBytes[4:6], 1)       // Class IN
		binary.BigEndian.PutUint32(answerBytes[6:10], 60)     // TTL: 60 seconds
		binary.BigEndian.PutUint16(answerBytes[10:12], 16)    // RDLENGTH: 16 bytes
		// Bytes 12:28 are initialized to 0 (IPv6 ::)

	default:
		// For other types (e.g., HTTPS, TXT, MX), return NOERROR with 0 answers
		ancount = 0
	}

	// Construct DNS Header (12 bytes)
	// Flags: QR=1 (Response), AA=1 (Authoritative), RA=1 (Recursion Available), RCODE=0 (NOERROR)
	flags := uint16(0x8580)
	if len(query) >= 4 && (query[2]&0x01 != 0) {
		flags |= 0x0100 // Echo RD (Recursion Desired) bit from query
	}

	resp := make([]byte, 12+len(questionSection)+len(answerBytes))
	binary.BigEndian.PutUint16(resp[0:2], clientTID)
	binary.BigEndian.PutUint16(resp[2:4], flags)
	binary.BigEndian.PutUint16(resp[4:6], 1) // QDCOUNT = 1
	binary.BigEndian.PutUint16(resp[6:8], ancount)
	binary.BigEndian.PutUint16(resp[8:10], 0) // NSCOUNT = 0
	binary.BigEndian.PutUint16(resp[10:12], 0) // ARCOUNT = 0

	// Copy Question
	copy(resp[12:12+len(questionSection)], questionSection)

	// Copy Answer
	if len(answerBytes) > 0 {
		copy(resp[12+len(questionSection):], answerBytes)
	}

	return resp
}

