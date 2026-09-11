package vpn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveInterfaceName(t *testing.T) {
	cases := []struct {
		input    string
		fallback string
		expected string
	}{
		{"/etc/routerd/vpn.conf", "wg0", "vpn"},
		{"/etc/wireguard/wg0.conf", "wg0", "wg0"},
		{"/etc/wireguard/mullvad-se1.conf", "wg0", "mullvad-se1"},
		{"", "wg0", "wg0"},
		{".conf", "default0", "default0"},
	}

	for _, tc := range cases {
		got := DeriveInterfaceName(tc.input, tc.fallback)
		if got != tc.expected {
			t.Errorf("DeriveInterfaceName(%s, %s) = %s; want %s", tc.input, tc.fallback, got, tc.expected)
		}
	}
}

func TestIsConfigured(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Missing file
	if IsConfigured(filepath.Join(tmpDir, "nonexistent.conf")) {
		t.Error("expected false for nonexistent file")
	}

	// 2. Commented template
	commentedContent := `# WireGuard Config
[Interface]
# PrivateKey = YOUR_PRIVATE_KEY_HERE
Address = 172.16.0.2/32
`
	commentedPath := filepath.Join(tmpDir, "commented.conf")
	_ = os.WriteFile(commentedPath, []byte(commentedContent), 0600)
	if IsConfigured(commentedPath) {
		t.Error("expected false for commented template")
	}

	// 3. Placeholder value
	placeholderContent := `[Interface]
PrivateKey = YOUR_PRIVATE_KEY_HERE
Address = 172.16.0.2/32
`
	placeholderPath := filepath.Join(tmpDir, "placeholder.conf")
	_ = os.WriteFile(placeholderPath, []byte(placeholderContent), 0600)
	if IsConfigured(placeholderPath) {
		t.Error("expected false for placeholder value")
	}

	// 4. Real configured key
	validContent := `[Interface]
PrivateKey = aGVsbG8td2lyZWd1YXJkLXRlc3Qta2V5LTEyMzQ1Njc=
Address = 172.16.0.2/32

[Peer]
PublicKey = c2VydmVyLXB1YmxpYy1rZXktdGVzdC0xMjM0NTY3ODk=
Endpoint = 198.51.100.1:51820
AllowedIPs = 0.0.0.0/0
`
	validPath := filepath.Join(tmpDir, "valid.conf")
	_ = os.WriteFile(validPath, []byte(validContent), 0600)
	if !IsConfigured(validPath) {
		t.Error("expected true for valid configured key")
	}
}

func TestPrepareRuntimeConfigSanitizesDNS(t *testing.T) {
	content := `[Interface]
PrivateKey = aGVsbG8tdGVzdC1rZXk=
Address = 10.0.0.2/24
DNS = 1.1.1.1, 8.8.8.8

[Peer]
PublicKey = c2VydmVyLWtleQ==
Endpoint = 203.0.113.1:51820
AllowedIPs = 0.0.0.0/0
`
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "vpn.conf")
	runDir := filepath.Join(tmpDir, "run")
	_ = os.WriteFile(sourcePath, []byte(content), 0600)

	runtimePath, err := PrepareRuntimeConfig(sourcePath, runDir)
	if err != nil {
		t.Fatalf("unexpected error preparing runtime config: %v", err)
	}

	data, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatalf("failed to read runtime config: %v", err)
	}

	runtimeContent := string(data)

	// Verify DNS line is commented out
	if strings.Contains(runtimeContent, "\nDNS = 1.1.1.1") {
		t.Error("expected DNS directive to be commented out")
	}
	if !strings.Contains(runtimeContent, "# DNS = 1.1.1.1, 8.8.8.8 # commented by routerd") {
		t.Errorf("expected comment notice on DNS line, got:\n%s", runtimeContent)
	}

	// Verify other fields remain intact
	if !strings.Contains(runtimeContent, "PrivateKey = aGVsbG8tdGVzdC1rZXk=") {
		t.Error("expected PrivateKey to be preserved")
	}
	if !strings.Contains(runtimeContent, "Address = 10.0.0.2/24") {
		t.Error("expected Address to be preserved")
	}
	if !strings.Contains(runtimeContent, "Endpoint = 203.0.113.1:51820") {
		t.Error("expected Endpoint to be preserved")
	}
}

func TestControllerUnconfiguredGracefulError(t *testing.T) {
	tmpDir := t.TempDir()
	ctrl := NewController(Config{
		ProfilePath: filepath.Join(tmpDir, "empty.conf"),
		RunDir:      tmpDir,
		APInterface: "ap0",
	})

	if ctrl.IsActive() {
		t.Error("expected controller to be inactive initially")
	}

	_, err := ctrl.Start()
	if err == nil {
		t.Error("expected error starting unconfigured controller, got nil")
	}

	// Idempotent stop when inactive
	if err := ctrl.Stop(); err != nil {
		t.Errorf("expected nil error on stopping inactive controller, got: %v", err)
	}
}
