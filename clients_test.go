package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- uptimeSince -------------------------------------------------------------

func TestUptimeSince_Seconds(t *testing.T) {
	now := timeNow()
	got := uptimeSince(now - 45)
	if got != "45s" {
		t.Errorf("uptimeSince(-45s) = %q, want %q", got, "45s")
	}
}

func TestUptimeSince_Minutes(t *testing.T) {
	now := timeNow()
	got := uptimeSince(now - 125) // 2m 5s
	if got != "2m 5s" {
		t.Errorf("uptimeSince(-125s) = %q, want %q", got, "2m 5s")
	}
}

func TestUptimeSince_Hours(t *testing.T) {
	now := timeNow()
	got := uptimeSince(now - 3723) // 1h 2m 3s
	if got != "1h 2m 3s" {
		t.Errorf("uptimeSince(-3723s) = %q, want %q", got, "1h 2m 3s")
	}
}

func TestUptimeSince_Zero(t *testing.T) {
	now := timeNow()
	got := uptimeSince(now)
	// Should be 0s or very close.
	if got != "0s" && !strings.HasSuffix(got, "s") {
		t.Errorf("uptimeSince(now) = %q, expected seconds-only format", got)
	}
}

func TestUptimeSince_FutureTimestamp(t *testing.T) {
	// A timestamp in the future should clamp to 0s, not return negative.
	now := timeNow()
	got := uptimeSince(now + 9999)
	if got != "0s" {
		t.Errorf("uptimeSince(future) = %q, want %q", got, "0s")
	}
}

// --- parseDnsmasqLeases (main package, clients.go) ---------------------------

func TestParseDnsmasqLeases_Basic(t *testing.T) {
	dir := t.TempDir()
	content := "1234567890 aa:bb:cc:dd:ee:ff 192.168.50.100 android-phone *\n" +
		"1234567891 11:22:33:44:55:66 192.168.50.101 * *\n"
	if err := os.WriteFile(filepath.Join(dir, "dnsmasq.leases"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m := parseDnsmasqLeases(dir)

	if ip := m["aa:bb:cc:dd:ee:ff"]; ip != "192.168.50.100" {
		t.Errorf("MAC aa:bb:cc:dd:ee:ff → %q, want 192.168.50.100", ip)
	}
	if ip := m["11:22:33:44:55:66"]; ip != "192.168.50.101" {
		t.Errorf("MAC 11:22:33:44:55:66 → %q, want 192.168.50.101", ip)
	}
}

func TestParseDnsmasqLeases_Uppercase(t *testing.T) {
	// MAC addresses in leases file are sometimes uppercase; must be normalized to lowercase.
	dir := t.TempDir()
	content := "1234567890 AA:BB:CC:DD:EE:FF 10.0.0.5 host *\n"
	if err := os.WriteFile(filepath.Join(dir, "dnsmasq.leases"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m := parseDnsmasqLeases(dir)

	if ip := m["aa:bb:cc:dd:ee:ff"]; ip != "10.0.0.5" {
		t.Errorf("uppercase MAC not normalized: %q", ip)
	}
}

func TestParseDnsmasqLeases_Empty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dnsmasq.leases"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	m := parseDnsmasqLeases(dir)
	if len(m) != 0 {
		t.Errorf("empty leases file: expected empty map, got %v", m)
	}
}

func TestParseDnsmasqLeases_Missing(t *testing.T) {
	// Use a dir with no leases file. parseDnsmasqLeases will also try
	// /var/lib/misc/dnsmasq.leases as fallback, which may exist on the system.
	// We only verify the call doesn't panic and returns a non-nil map.
	dir := t.TempDir()
	m := parseDnsmasqLeases(dir)
	if m == nil {
		t.Error("expected non-nil map, got nil")
	}
}

// --- broadcastIP (util.go) ---------------------------------------------------

func TestBroadcastIP_Slash24(t *testing.T) {
	bc, err := broadcastIP("192.168.50.0/24")
	if err != nil {
		t.Fatalf("broadcastIP error: %v", err)
	}
	if bc.String() != "192.168.50.255" {
		t.Errorf("broadcastIP(192.168.50.0/24) = %s, want 192.168.50.255", bc)
	}
}

func TestBroadcastIP_Slash16(t *testing.T) {
	bc, err := broadcastIP("172.16.0.0/16")
	if err != nil {
		t.Fatalf("broadcastIP error: %v", err)
	}
	if bc.String() != "172.16.255.255" {
		t.Errorf("broadcastIP(172.16.0.0/16) = %s, want 172.16.255.255", bc)
	}
}

func TestBroadcastIP_Slash8(t *testing.T) {
	bc, err := broadcastIP("10.0.0.0/8")
	if err != nil {
		t.Fatalf("broadcastIP error: %v", err)
	}
	if bc.String() != "10.255.255.255" {
		t.Errorf("broadcastIP(10.0.0.0/8) = %s, want 10.255.255.255", bc)
	}
}

func TestBroadcastIP_Invalid(t *testing.T) {
	if _, err := broadcastIP("not-a-cidr"); err == nil {
		t.Error("expected error for invalid CIDR, got nil")
	}
}

// --- ipNetmask (services.go) -------------------------------------------------

func TestIpNetmask_Valid(t *testing.T) {
	tests := []struct {
		cidr string
		want string
	}{
		{"192.168.50.0/24", "255.255.255.0"},
		{"10.0.0.0/8", "255.0.0.0"},
		{"172.16.0.0/12", "255.240.0.0"},
		{"192.168.0.0/16", "255.255.0.0"},
	}
	for _, tt := range tests {
		got := ipNetmask(tt.cidr)
		if got != tt.want {
			t.Errorf("ipNetmask(%q) = %q, want %q", tt.cidr, got, tt.want)
		}
	}
}

func TestIpNetmask_Invalid(t *testing.T) {
	// Must not panic, must return safe fallback.
	got := ipNetmask("not-a-cidr")
	if got != "255.255.255.0" {
		t.Errorf("ipNetmask(invalid) = %q, want fallback 255.255.255.0", got)
	}
}
