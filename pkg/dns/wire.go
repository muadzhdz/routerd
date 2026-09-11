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
