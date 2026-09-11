package dns

import (
	"testing"
	"time"
)

func TestNormalizeUpstream(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"1.1.1.1", "https://1.1.1.1/dns-query"},
		{"1.0.0.1", "https://1.1.1.1/dns-query"},
		{"8.8.8.8", "https://dns.google/dns-query"},
		{"8.8.4.4", "https://dns.google/dns-query"},
		{"9.9.9.9", "https://dns.quad9.net/dns-query"},
		{"149.112.112.112", "https://dns.quad9.net/dns-query"},
		{"https://adguard.com/dns-query", "https://adguard.com/dns-query"},
		{"http://local-doh:8080/dns-query", "http://local-doh:8080/dns-query"},
		{"10.0.0.53", "https://10.0.0.53/dns-query"},
		{"", ""},
	}

	for _, tc := range cases {
		got := NormalizeUpstream(tc.input)
		if got != tc.expected {
			t.Errorf("NormalizeUpstream(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestNewResolverDefaultsAndNormalization(t *testing.T) {
	// Empty upstreams -> default DoH endpoints
	r1 := NewResolver(nil, 0)
	if len(r1.upstreams) != 3 {
		t.Errorf("expected 3 default upstreams, got %d", len(r1.upstreams))
	}
	if r1.client.Timeout != 2*time.Second {
		t.Errorf("expected default timeout 2s, got %v", r1.client.Timeout)
	}

	// Raw IPs -> converted to HTTPS DoH endpoints
	r2 := NewResolver([]string{"1.1.1.1", "8.8.8.8"}, 5*time.Second)
	if len(r2.upstreams) != 2 {
		t.Fatalf("expected 2 upstreams, got %d", len(r2.upstreams))
	}
	if r2.upstreams[0] != "https://1.1.1.1/dns-query" {
		t.Errorf("expected Cloudflare DoH URL, got %s", r2.upstreams[0])
	}
	if r2.upstreams[1] != "https://dns.google/dns-query" {
		t.Errorf("expected Google DoH URL, got %s", r2.upstreams[1])
	}
}
