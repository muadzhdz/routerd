package main

import (
	"net"
	"strings"
	"testing"
)

// --- gatewayIP ---------------------------------------------------------------

func TestGatewayIP(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"192.168.50.0/24", "192.168.50.1"},
		{"10.0.0.0/8", "10.0.0.1"},
		{"172.16.0.0/12", "172.16.0.1"},
		{"10.42.7.0/24", "10.42.7.1"},
	}
	for _, tt := range tests {
		got, err := gatewayIP(tt.cidr)
		if err != nil {
			t.Errorf("gatewayIP(%q) error: %v", tt.cidr, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("gatewayIP(%q) = %s, want %s", tt.cidr, got, tt.want)
		}
	}
}

func TestGatewayIPInvalid(t *testing.T) {
	if _, err := gatewayIP("not-a-cidr"); err == nil {
		t.Error("expected error for invalid CIDR, got nil")
	}
}

// --- gatewayCIDR -------------------------------------------------------------

func TestGatewayCIDR(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"192.168.50.0/24", "192.168.50.1/24"},
		{"10.0.0.0/8", "10.0.0.1/8"},
		{"172.20.5.0/24", "172.20.5.1/24"},
	}
	for _, tt := range tests {
		got, err := gatewayCIDR(tt.cidr)
		if err != nil {
			t.Errorf("gatewayCIDR(%q) error: %v", tt.cidr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("gatewayCIDR(%q) = %s, want %s", tt.cidr, got, tt.want)
		}
	}
}

// --- dhcpRange ---------------------------------------------------------------

func TestDhcpRange(t *testing.T) {
	tests := []struct {
		cidr      string
		wantStart string
		wantEnd   string
	}{
		// /24: start = base+10 = .10, end = broadcast-1 = .254
		{"192.168.50.0/24", "192.168.50.10", "192.168.50.254"},
		{"10.42.7.0/24", "10.42.7.10", "10.42.7.254"},
		// /16: start = x.y.0.10, end = x.y.255.254
		{"172.16.0.0/16", "172.16.0.10", "172.16.255.254"},
	}
	for _, tt := range tests {
		start, end, err := dhcpRange(tt.cidr)
		if err != nil {
			t.Errorf("dhcpRange(%q) error: %v", tt.cidr, err)
			continue
		}
		if start.String() != tt.wantStart {
			t.Errorf("dhcpRange(%q) start = %s, want %s", tt.cidr, start, tt.wantStart)
		}
		if end.String() != tt.wantEnd {
			t.Errorf("dhcpRange(%q) end = %s, want %s", tt.cidr, end, tt.wantEnd)
		}
	}
}

func TestDhcpRangeInvalid(t *testing.T) {
	if _, _, err := dhcpRange("not-valid"); err == nil {
		t.Error("expected error for invalid CIDR")
	}
}

// --- freqToChannel -----------------------------------------------------------

func TestFreqToChannel(t *testing.T) {
	tests := []struct {
		freq     int
		wantCh   int
		wantBand string
	}{
		{2412, 1, "g"},   // 2.4 GHz ch 1
		{2437, 6, "g"},   // 2.4 GHz ch 6
		{2462, 11, "g"},  // 2.4 GHz ch 11
		{5180, 36, "a"},  // 5 GHz ch 36
		{5500, 100, "a"}, // 5 GHz ch 100
		{5745, 149, "a"}, // 5 GHz ch 149
		{9999, 0, ""},    // unsupported
		{0, 0, ""},       // zero
	}
	for _, tt := range tests {
		ch, band := freqToChannel(tt.freq)
		if ch != tt.wantCh || band != tt.wantBand {
			t.Errorf("freqToChannel(%d) = (%d,%q), want (%d,%q)",
				tt.freq, ch, band, tt.wantCh, tt.wantBand)
		}
	}
}

// --- randomMAC ---------------------------------------------------------------

func TestRandomMAC(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		mac := randomMAC()
		// Must be a valid MAC format.
		if _, err := net.ParseMAC(mac); err != nil {
			t.Errorf("randomMAC() = %q is not a valid MAC: %v", mac, err)
			continue
		}
		// Parse first byte to check LAA and multicast bits.
		parts := strings.Split(mac, ":")
		if len(parts) != 6 {
			t.Errorf("randomMAC() = %q does not have 6 octets", mac)
			continue
		}
		var b byte
		if _, err := parseHexByte(parts[0], &b); err != nil {
			t.Errorf("randomMAC() first octet %q parse error: %v", parts[0], err)
			continue
		}
		// LAA bit (bit 1) must be set.
		if b&0x02 == 0 {
			t.Errorf("randomMAC() = %q: LAA bit not set in first octet 0x%02x", mac, b)
		}
		// Multicast bit (bit 0) must be clear.
		if b&0x01 != 0 {
			t.Errorf("randomMAC() = %q: multicast bit set in first octet 0x%02x", mac, b)
		}
		seen[mac] = true
	}
	// All 10 MACs should be unique (probability of collision is astronomically small).
	if len(seen) < 9 {
		t.Errorf("randomMAC() produced too many duplicates: %d unique out of 10", len(seen))
	}
}

// parseHexByte is a test helper.
func parseHexByte(s string, b *byte) (int, error) {
	var v uint64
	_, err := parseUint(s, 16, 8, &v)
	if err != nil {
		return 0, err
	}
	*b = byte(v)
	return 1, nil
}

func parseUint(s string, base, bitSize int, v *uint64) (int, error) {
	val := uint64(0)
	for _, c := range s {
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return 0, &net.AddrError{Err: "invalid hex digit", Addr: s}
		}
		val = val*uint64(base) + d
	}
	*v = val
	return len(s), nil
}

// --- generateRandomSubnet ----------------------------------------------------

func TestGenerateRandomSubnet(t *testing.T) {
	for i := 0; i < 50; i++ {
		subnet := generateRandomSubnet()
		// Must be parseable as a CIDR.
		_, ipnet, err := net.ParseCIDR(subnet)
		if err != nil {
			t.Errorf("generateRandomSubnet() = %q is not valid CIDR: %v", subnet, err)
			continue
		}
		// Must be a /24.
		ones, _ := ipnet.Mask.Size()
		if ones != 24 {
			t.Errorf("generateRandomSubnet() = %q: want /24, got /%d", subnet, ones)
		}
		// Must be in an RFC1918 range.
		ip := ipnet.IP
		if !isRFC1918(ip) {
			t.Errorf("generateRandomSubnet() = %q is not in RFC1918 range", subnet)
		}
	}
}

func isRFC1918(ip net.IP) bool {
	// 10.0.0.0/8
	if ip[0] == 10 {
		return true
	}
	// 172.16.0.0/12
	if ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31 {
		return true
	}
	// 192.168.0.0/16
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	return false
}
