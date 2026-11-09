package dns

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterExactAndSuffixMatching(t *testing.T) {
	f := NewFilter()
	f.Add("doubleclick.net")
	f.Add("analytics.google.com")
	f.Add("adservice.google.com.") // Trailing dot should be stripped

	// 1. Exact matches
	if !f.IsBlocked("doubleclick.net") {
		t.Error("expected doubleclick.net to be blocked")
	}
	if !f.IsBlocked("analytics.google.com") {
		t.Error("expected analytics.google.com to be blocked")
	}
	if !f.IsBlocked("adservice.google.com") {
		t.Error("expected adservice.google.com to be blocked")
	}

	// 2. Subdomain suffix matches
	if !f.IsBlocked("ad.doubleclick.net") {
		t.Error("expected ad.doubleclick.net to be blocked (suffix match)")
	}
	if !f.IsBlocked("secure.page.ad.doubleclick.net") {
		t.Error("expected secure.page.ad.doubleclick.net to be blocked (deep suffix match)")
	}

	// 3. Non-blocked domains
	if f.IsBlocked("google.com") {
		t.Error("google.com should NOT be blocked")
	}
	if f.IsBlocked("doubleclick.com") {
		t.Error("doubleclick.com should NOT be blocked")
	}
	if f.IsBlocked("notdoubleclick.net") {
		t.Error("notdoubleclick.net should NOT be blocked")
	}
}

func TestFilterLoadFile(t *testing.T) {
	content := `# Ad and Tracker Blocklist
# Format 1: /etc/hosts style
0.0.0.0 adservice.google.com
127.0.0.1 tracker.facebook.com # inline comment
::1 telemetry.microsoft.com

# Format 2: raw domain list
criteo.com
*.taboola.com
outbrain.com.

# Ignored lines
# comment
; another comment
! adblock style comment
127.0.0.1 localhost
0.0.0.0 broadcasthost
`
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "blocklist.txt")
	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp blocklist: %v", err)
	}

	f := NewFilter()
	count, err := f.LoadFile(confPath)
	if err != nil {
		t.Fatalf("unexpected error loading blocklist: %v", err)
	}

	if count != 6 {
		t.Errorf("expected 6 unique rules loaded, got %d", count)
	}

	// Check /etc/hosts entries
	if !f.IsBlocked("adservice.google.com") {
		t.Error("expected adservice.google.com to be blocked")
	}
	if !f.IsBlocked("sub.tracker.facebook.com") {
		t.Error("expected sub.tracker.facebook.com to be blocked")
	}
	if !f.IsBlocked("telemetry.microsoft.com") {
		t.Error("expected telemetry.microsoft.com to be blocked")
	}

	// Check raw domain entries
	if !f.IsBlocked("criteo.com") {
		t.Error("expected criteo.com to be blocked")
	}
	if !f.IsBlocked("widget.taboola.com") {
		t.Error("expected widget.taboola.com to be blocked")
	}
	if !f.IsBlocked("outbrain.com") {
		t.Error("expected outbrain.com to be blocked")
	}

	// Check localhost is not blocked
	if f.IsBlocked("localhost") {
		t.Error("localhost should never be blocked")
	}
}

func TestFilterLoadProductionBlocklist(t *testing.T) {
	blocklistPath := filepath.Join("..", "..", "blocklist.txt")
	if _, err := os.Stat(blocklistPath); os.IsNotExist(err) {
		t.Skip("production blocklist.txt not found, skipping")
	}

	f := NewFilter()
	count, err := f.LoadFile(blocklistPath)
	if err != nil {
		t.Fatalf("failed to load production blocklist: %v", err)
	}

	if count < 80000 {
		t.Errorf("expected at least 80000 rules, got %d", count)
	}

	testCases := []struct {
		domain  string
		blocked bool
	}{
		{"doubleclick.net", true},
		{"adservice.google.com", true},
		{"admob.com", true},
		{"google.com", false},
		{"github.com", false},
		{"archlinux.org", false},
	}

	for _, tc := range testCases {
		if got := f.IsBlocked(tc.domain); got != tc.blocked {
			t.Errorf("domain %s: expected blocked=%v, got %v", tc.domain, tc.blocked, got)
		}
	}
}

func BenchmarkFilterLookup(b *testing.B) {
	blocklistPath := filepath.Join("..", "..", "blocklist.txt")
	f := NewFilter()
	_, _ = f.LoadFile(blocklistPath)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = f.IsBlocked("pagead2.googlesyndication.com")
		_ = f.IsBlocked("google.com")
	}
}
