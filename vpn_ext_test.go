package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- TestStartVPN_Disabled ---------------------------------------------------

func TestStartVPN_Disabled(t *testing.T) {
	cfg := &Config{
		EnableVPN: false,
		Uplink:    "wlan0",
	}
	dir := t.TempDir()
	iface, err := startVPN(cfg, dir)
	if err != nil {
		t.Fatalf("startVPN(disabled) error: %v", err)
	}
	if iface != "wlan0" {
		t.Errorf("startVPN(disabled) = %q, want 'wlan0'", iface)
	}
}

// --- TestStartVPN_Custom -----------------------------------------------------

func TestStartVPN_Custom(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	cfg := &Config{
		EnableVPN:    true,
		VPNMode:      "custom",
		VPNInterface: "tun0",
		Uplink:       "wlan0",
	}
	dir := t.TempDir()
	iface, err := startVPN(cfg, dir)
	if err != nil {
		t.Fatalf("startVPN(custom) error: %v", err)
	}
	if iface != "tun0" {
		t.Errorf("startVPN(custom) = %q, want 'tun0'", iface)
	}
}

// --- TestStartVPN_Custom_NoInterface -----------------------------------------

func TestStartVPN_Custom_NoInterface(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	cfg := &Config{
		EnableVPN:    true,
		VPNMode:      "custom",
		VPNInterface: "", // empty → error
		Uplink:       "wlan0",
	}
	dir := t.TempDir()
	_, err := startVPN(cfg, dir)
	if err == nil {
		t.Error("expected error for VPN_MODE=custom with empty VPN_INTERFACE, got nil")
	}
}

// --- TestStartVPN_DPIBypass --------------------------------------------------

func TestStartVPN_DPIBypass(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	cfg := &Config{
		EnableVPN: true,
		VPNMode:   "dpibypass",
		SpoofTTL:  64,
		Uplink:    "wlan0",
	}
	dir := t.TempDir()
	iface, err := startVPN(cfg, dir)
	if err != nil {
		t.Fatalf("startVPN(dpibypass) error: %v", err)
	}
	if iface != "wlan0" {
		t.Errorf("startVPN(dpibypass) = %q, want 'wlan0'", iface)
	}
	if !cfg.DPIBypass {
		t.Error("expected cfg.DPIBypass=true after dpibypass mode")
	}
}

// --- TestStartVPN_DPIBypass_ForcesSpoof --------------------------------------

func TestStartVPN_DPIBypass_ForcesSpoof(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	cfg := &Config{
		EnableVPN: true,
		VPNMode:   "dpibypass",
		SpoofTTL:  0, // zero → should be forced to 64
		Uplink:    "wlan0",
	}
	dir := t.TempDir()
	_, err := startVPN(cfg, dir)
	if err != nil {
		t.Fatalf("startVPN(dpibypass) error: %v", err)
	}
	if cfg.SpoofTTL != 64 {
		t.Errorf("expected SpoofTTL forced to 64, got %d", cfg.SpoofTTL)
	}
}

// --- TestStartVPN_Unsupported ------------------------------------------------

func TestStartVPN_Unsupported(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	cfg := &Config{
		EnableVPN: true,
		VPNMode:   "unknown_mode",
		Uplink:    "wlan0",
	}
	dir := t.TempDir()
	_, err := startVPN(cfg, dir)
	if err == nil {
		t.Error("expected error for unsupported VPN_MODE, got nil")
	}
}

// --- TestStopVPN_WithMarker --------------------------------------------------

func TestStopVPN_WithMarker(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	dir := t.TempDir()
	confPath := filepath.Join(dir, "vpn.conf")
	// Write a minimal vpn.conf so wg-quick has something to reference.
	_ = os.WriteFile(confPath, []byte("[Interface]\nPrivateKey = abc\n"), 0600)
	// Write the vpn_started marker.
	_ = os.WriteFile(filepath.Join(dir, "vpn_started"), []byte(confPath), 0600)

	// Stub wg-quick down to succeed.
	mock.stub("", nil, "wg-quick", "down", confPath)

	cfg := &Config{}
	stopVPN(cfg, dir)

	// wg-quick down must have been called.
	if !mock.calledWith("wg-quick", "down", confPath) {
		t.Errorf("expected 'wg-quick down %s' call; calls:\n%s", confPath, dumpCalls(mock))
	}

	// vpn_started file must be removed.
	if _, err := os.Stat(filepath.Join(dir, "vpn_started")); err == nil {
		t.Error("vpn_started file should have been removed after stopVPN")
	}
}

// --- TestStopVPN_NoMarker ----------------------------------------------------

func TestStopVPN_NoMarker(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	dir := t.TempDir()
	cfg := &Config{}

	// No vpn_started file — should not panic and should not call wg-quick.
	stopVPN(cfg, dir)

	for _, c := range mock.calls {
		if c.name == "wg-quick" {
			t.Errorf("expected no wg-quick calls when no vpn_started file, got: %v %v", c.name, c.args)
		}
	}
}

// --- TestGenerateWARPConfig_CreatesFile --------------------------------------

func TestGenerateWARPConfig_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	confPath := filepath.Join(dir, "vpn.conf")

	if err := generateWARPConfig(confPath); err != nil {
		t.Fatalf("generateWARPConfig error: %v", err)
	}

	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("generated file not readable: %v", err)
	}
	if !strings.Contains(string(data), "[Interface]") {
		t.Errorf("generated file missing [Interface] section; content: %s", string(data))
	}
}

// --- TestSetupDPIBypass_SetsFlag ---------------------------------------------

func TestSetupDPIBypass_SetsFlag(t *testing.T) {
	cfg := &Config{
		SpoofTTL: 64,
	}
	setupDPIBypass(cfg)
	if !cfg.DPIBypass {
		t.Error("expected cfg.DPIBypass=true after setupDPIBypass")
	}
}
