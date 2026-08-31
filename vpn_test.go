package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --- isConfigUnconfigured ----------------------------------------------------

func TestIsConfigUnconfiguredAllCommented(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vpn.conf")
	content := `# Cloudflare WARP
# PrivateKey = XXXXXXXXXX
# Address = 172.16.0.2/32

[Peer]
# PublicKey = YYYYYY
`
	_ = os.WriteFile(path, []byte(content), 0600)
	if !isConfigUnconfigured(path) {
		t.Error("all-commented config should be considered unconfigured")
	}
}

func TestIsConfigUnconfiguredHasPrivateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vpn.conf")
	content := `[Interface]
PrivateKey = abc123realkey
Address = 10.2.0.2/32

[Peer]
PublicKey = def456
Endpoint = 1.2.3.4:51820
AllowedIPs = 0.0.0.0/0
`
	_ = os.WriteFile(path, []byte(content), 0600)
	if isConfigUnconfigured(path) {
		t.Error("config with PrivateKey should not be considered unconfigured")
	}
}

func TestIsConfigUnconfiguredMixedCase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vpn.conf")
	// Test case-insensitive match for PrivateKey.
	content := `[Interface]
PRIVATEKEY = somekey123
`
	_ = os.WriteFile(path, []byte(content), 0600)
	if isConfigUnconfigured(path) {
		t.Error("config with PRIVATEKEY (uppercase) should not be unconfigured")
	}
}

func TestIsConfigUnconfiguredNonExistent(t *testing.T) {
	if !isConfigUnconfigured("/nonexistent/path/vpn.conf") {
		t.Error("non-existent file should be considered unconfigured")
	}
}

func TestIsConfigUnconfiguredEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.conf")
	_ = os.WriteFile(path, []byte(""), 0600)
	if !isConfigUnconfigured(path) {
		t.Error("empty file should be considered unconfigured")
	}
}

// --- deriveInterfaceFromConf -------------------------------------------------

func TestDeriveInterfaceFromConf(t *testing.T) {
	tests := []struct {
		confPath     string
		defaultIface string
		want         string
	}{
		{"/etc/routerd/vpn.conf", "wg0", "vpn"},
		{"/etc/routerd/wg0.conf", "wg1", "wg0"},
		{"/etc/wireguard/mullvad-us.conf", "wg0", "mullvad-us"},
		{"vpn.conf", "wg0", "vpn"},
		// When base has no extension-less name, use default.
		{"/etc/routerd/.conf", "wg0", "wg0"},
	}
	for _, tt := range tests {
		got := deriveInterfaceFromConf(tt.confPath, tt.defaultIface)
		if got != tt.want {
			t.Errorf("deriveInterfaceFromConf(%q, %q) = %q, want %q",
				tt.confPath, tt.defaultIface, got, tt.want)
		}
	}
}

func TestDeriveInterfaceFromConfFallback(t *testing.T) {
	// No default iface provided.
	got := deriveInterfaceFromConf("/etc/routerd/vpn.conf", "")
	if got != "vpn" {
		t.Errorf("expected 'vpn', got %q", got)
	}
}
