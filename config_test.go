package main

import (
	"os"
	"path/filepath"
	"testing"
)

// --- parseBool ---------------------------------------------------------------

func TestParseBool(t *testing.T) {
	truthy := []string{"true", "TRUE", "True", "1", "yes", "YES", "Yes", "on", "ON", "On"}
	for _, s := range truthy {
		if !parseBool(s) {
			t.Errorf("parseBool(%q) = false, want true", s)
		}
	}
	falsy := []string{"false", "FALSE", "0", "no", "NO", "off", "OFF", "", "maybe", "2"}
	for _, s := range falsy {
		if parseBool(s) {
			t.Errorf("parseBool(%q) = true, want false", s)
		}
	}
}

// --- sanitizeSSID ------------------------------------------------------------

func TestSanitizeSSID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"routerd", "routerd"},
		{"my wifi", "my wifi"},
		{"evil\nssid", "evilssid"},   // newline stripped
		{"evil\r\nssid", "evilssid"}, // CRLF stripped
		{"key=val", "keyval"},        // '=' stripped
		{"null\x00byte", "nullbyte"}, // null byte stripped
		{"こんにちは", "こんにちは"},           // unicode kept
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeSSID(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeSSID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- LoadConfig --------------------------------------------------------------

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "routerd.conf")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writeConfig: %v", err)
	}
	return path
}

func TestLoadConfigDefaults(t *testing.T) {
	// Non-existent file should return defaults without error.
	cfg, err := LoadConfig("/nonexistent/path/routerd.conf")
	if err != nil {
		t.Fatalf("LoadConfig(nonexistent) error: %v", err)
	}
	if cfg.SSID != "routerd" {
		t.Errorf("default SSID = %q, want %q", cfg.SSID, "routerd")
	}
	if cfg.VPNDNS != "1.1.1.1" {
		t.Errorf("default VPNDNS = %q, want %q", cfg.VPNDNS, "1.1.1.1")
	}
	if !cfg.RandomMAC {
		t.Error("default RandomMAC = false, want true")
	}
	if !cfg.IsolateHost {
		t.Error("default IsolateHost = false, want true")
	}
	if !cfg.DisableIPv6 {
		t.Error("default DisableIPv6 = false, want true")
	}
}

func TestLoadConfigAllKeys(t *testing.T) {
	content := `
SSID=TestNet
PASSWORD=securepass
CHANNEL=6
INTERFACE_STA=wlan0
INTERFACE_AP=ap1
UPLINK=eth0
SUBNET=10.99.0.0/24
COUNTRY=US
MAX_CLIENTS=8
DNS=8.8.8.8
RANDOM_MAC=false
ISOLATE_HOST=false
SPOOF_TTL=128
TOR_MODE=true
DISABLE_IPV6=false
HIDE_SSID=true
LIMIT_RATE_MBPS=50
ENABLE_VPN=true
VPN_MODE=wireguard
VPN_CONFIG=/etc/wg.conf
VPN_INTERFACE=wg1
VPN_KILL_SWITCH=false
VPN_DNS=10.64.0.1
WPA3=true
`
	path := writeConfig(t, content)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig error: %v", err)
	}

	check := func(name, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	checkBool := func(name string, got, want bool) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	checkInt := func(name string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}

	check("SSID", cfg.SSID, "TestNet")
	check("Password", cfg.Password, "securepass")
	check("Channel", cfg.Channel, "6")
	check("InterfaceSTA", cfg.InterfaceSTA, "wlan0")
	check("InterfaceAP", cfg.InterfaceAP, "ap1")
	check("Uplink", cfg.Uplink, "eth0")
	check("Subnet", cfg.Subnet, "10.99.0.0/24")
	check("Country", cfg.Country, "US")
	checkInt("MaxClients", cfg.MaxClients, 8)
	check("DNS", cfg.DNS, "8.8.8.8")
	checkBool("RandomMAC", cfg.RandomMAC, false)
	checkBool("IsolateHost", cfg.IsolateHost, false)
	checkInt("SpoofTTL", cfg.SpoofTTL, 128)
	checkBool("TorMode", cfg.TorMode, true)
	checkBool("DisableIPv6", cfg.DisableIPv6, false)
	checkBool("HideSSID", cfg.HideSSID, true)
	checkInt("LimitRateMbps", cfg.LimitRateMbps, 50)
	checkBool("EnableVPN", cfg.EnableVPN, true)
	check("VPNMode", cfg.VPNMode, "wireguard")
	check("VPNConfig", cfg.VPNConfig, "/etc/wg.conf")
	check("VPNInterface", cfg.VPNInterface, "wg1")
	checkBool("VPNKillSwitch", cfg.VPNKillSwitch, false)
	check("VPNDNS", cfg.VPNDNS, "10.64.0.1")
	checkBool("WPA3", cfg.WPA3, true)
}

func TestLoadConfigIgnoresComments(t *testing.T) {
	path := writeConfig(t, `
# This is a comment
SSID=CommentTest
# PASSWORD=shouldbeignored
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if cfg.SSID != "CommentTest" {
		t.Errorf("SSID = %q, want CommentTest", cfg.SSID)
	}
	if cfg.Password != "" {
		t.Errorf("Password = %q, want empty (comment should be ignored)", cfg.Password)
	}
}

// --- Password validation -----------------------------------------------------

func TestPasswordTooShort(t *testing.T) {
	path := writeConfig(t, "PASSWORD=short\n")
	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for password < 8 chars, got nil")
	}
}

func TestPasswordTooLong(t *testing.T) {
	long := "a"
	for len(long) <= 63 {
		long += "a"
	}
	path := writeConfig(t, "PASSWORD="+long+"\n")
	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for password > 63 chars, got nil")
	}
}

func TestPasswordExactly8(t *testing.T) {
	path := writeConfig(t, "PASSWORD=exactly8\n")
	_, err := LoadConfig(path)
	if err != nil {
		t.Errorf("password of exactly 8 chars should be valid, got error: %v", err)
	}
}

func TestPasswordExactly63(t *testing.T) {
	p63 := "aaaaaaaabbbbbbbbccccccccddddddddeeeeeeeeffffffffgggggggghhhhhhh"
	if len(p63) != 63 {
		t.Fatalf("test fixture has wrong length %d", len(p63))
	}
	path := writeConfig(t, "PASSWORD="+p63+"\n")
	_, err := LoadConfig(path)
	if err != nil {
		t.Errorf("password of exactly 63 chars should be valid, got error: %v", err)
	}
}

func TestPasswordEmpty(t *testing.T) {
	// Empty password = open network, must be valid.
	path := writeConfig(t, "SSID=opennet\nPASSWORD=\n")
	_, err := LoadConfig(path)
	if err != nil {
		t.Errorf("empty password (open network) should be valid, got error: %v", err)
	}
}

// --- SSID validation ---------------------------------------------------------

func TestEmptySSID(t *testing.T) {
	path := writeConfig(t, "SSID=\n")
	_, err := LoadConfig(path)
	if err == nil {
		t.Error("expected error for empty SSID, got nil")
	}
}

func TestSSIDSanitizedOnLoad(t *testing.T) {
	path := writeConfig(t, "SSID=evil\nssid\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The newline in the SSID value is actually a new line in the file,
	// so the scanner reads "SSID=evil" and cfg.SSID = "evil".
	// This tests that the value after the first line is not included.
	if cfg.SSID != "evil" {
		t.Errorf("SSID = %q, want %q", cfg.SSID, "evil")
	}
}
