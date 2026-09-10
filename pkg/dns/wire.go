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

// ExtractQuestion mengurai nama domain (QName) dan tipe query (QType) dari payload DNS wire RFC 1035.
func ExtractQuestion(data []byte) (string, uint16, error) {
	if len(data) < 12 {
		return "", 0, errors.New("paket DNS terlalu pendek (<12 bytes)")
	}

	offset := 12
	var labels []string

	for offset < len(data) {
		length := int(data[offset])
		if length == 0 {
			offset++
			break
		}
		if length >= 192 { // Kompresi pointer 0xC0
			offset += 2
			break
		}
		if length > 63 || offset+1+length > len(data) {
			return "", 0, errors.New("format label DNS tidak valid")
		}
		labels = append(labels, string(data[offset+1:offset+1+length]))
		offset += 1 + length
	}

	if offset+4 > len(data) {
		return "", 0, errors.New("tidak ada QType/QClass di Question")
	}

	qtype := binary.BigEndian.Uint16(data[offset : offset+2])
	domain := strings.ToLower(strings.Join(labels, "."))
	if domain == "" {
		domain = "."
	}

	return domain, qtype, nil
}

// ExtractTTL membaca nilai TTL minimum dari section Answer pada DNS response.
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

	// Lewati semua record di Question section
	for i := 0; i < int(qdcount); i++ {
		for offset < len(resp) {
			length := int(resp[offset])
			if length == 0 {
				offset++
				break
			}
			if length >= 192 { // 0xC0 pointer kompresi
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

	// Baca Answer Section pertama
	if offset >= len(resp) {
		return defaultTTL
	}

	// Lewati NAME di Answer (bisa pointer atau label)
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

	offset += 4 // Lewati TYPE & CLASS
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
