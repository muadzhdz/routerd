// Package main implements the routerd daemon — a single-binary tool that turns
// any Linux machine with a Wi-Fi card into a stealth Wi-Fi access point, router,
// and transparent WireGuard VPN gateway.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// startVPN brings up the configured VPN tunnel and returns the active uplink interface name.
func startVPN(cfg *Config, runDir string) (string, error) {
	if !cfg.EnableVPN {
		return cfg.Uplink, nil
	}

	mode := strings.ToLower(cfg.VPNMode)
	switch mode {
	case "wireguard", "warp":
		return startWireGuard(cfg, runDir, mode)

	case "custom":
		if cfg.VPNInterface == "" || cfg.VPNInterface == "auto" {
			return "", fmt.Errorf("VPN_INTERFACE must be specified when VPN_MODE=custom (e.g. wg0, tun0)")
		}
		logInfo("using custom existing VPN interface %s as uplink", cfg.VPNInterface)
		return cfg.VPNInterface, nil

	case "dpibypass":
		// DPI bypass does not change the uplink interface — it applies
		// iptables MSS clamping and TTL normalization via enableNAT.
		// Additional fragmentation-based bypass is set up here.
		setupDPIBypass(cfg)
		return cfg.Uplink, nil

	default:
		return "", fmt.Errorf("unsupported VPN_MODE %q (use wireguard, warp, custom, or dpibypass)", cfg.VPNMode)
	}
}

// startWireGuard brings up a wg-quick managed WireGuard interface.
func startWireGuard(cfg *Config, runDir, mode string) (string, error) {
	confPath := cfg.VPNConfig

	// For warp mode, try to auto-generate the config using wgcf if available.
	if mode == "warp" && !fileExists(confPath) {
		logInfo("WARP VPN config not found at %s, attempting auto-generation...", confPath)
		if err := generateWARPConfigAuto(confPath); err != nil {
			logWarn("auto-generation failed: %v; falling back to template", err)
			if err2 := generateWARPConfig(confPath); err2 != nil {
				return "", fmt.Errorf("failed to generate WARP profile template: %w", err2)
			}
			logInfo("Generated WARP template at %s — fill in your keys", confPath)
		}
	}

	if !fileExists(confPath) {
		logWarn("VPN configuration file %s not found; falling back to direct uplink %s", confPath, cfg.Uplink)
		cfg.EnableVPN = false
		return cfg.Uplink, nil
	}

	if isConfigUnconfigured(confPath) {
		logWarn("VPN configuration %s contains unconfigured/commented keys; falling back to direct uplink %s. Edit %s or set ENABLE_VPN=false", confPath, cfg.Uplink, confPath)
		cfg.EnableVPN = false
		return cfg.Uplink, nil
	}

	logInfo("starting WireGuard VPN tunnel using %s", confPath)
	out, err := runCmd("wg-quick", "up", confPath)
	if err != nil && !strings.Contains(out, "already exists") {
		logWarn("cannot start WireGuard VPN (%s): %s; falling back to direct uplink %s", confPath, strings.TrimSpace(out), cfg.Uplink)
		cfg.EnableVPN = false
		return cfg.Uplink, nil
	}

	// Write tracking file so stopVPN knows to call wg-quick down.
	_ = os.WriteFile(filepath.Join(runDir, "vpn_started"), []byte(confPath), 0600)

	iface := deriveInterfaceFromConf(confPath, cfg.VPNInterface)
	logInfo("WireGuard VPN active on interface %s", iface)
	return iface, nil
}

// setupDPIBypass configures the Config so that enableNAT applies TCP MSS
// clamping and TTL normalization without starting an actual VPN tunnel.
// It sets a dedicated flag rather than reusing EnableVPN to avoid triggering
// the VPN kill-switch or the forced DNS DNAT rule.
func setupDPIBypass(cfg *Config) {
	logInfo("DPI Bypass mode active (TCP MSS clamping + TTL normalization)")
	// Force minimum TTL of 64 to prevent ISP DPI from counting hops.
	if cfg.SpoofTTL == 0 {
		cfg.SpoofTTL = 64
		logInfo("DPI Bypass: SpoofTTL forced to 64")
	}
	// Signal nat.go to apply MSS clamping without activating VPN-specific
	// rules (kill-switch, DNS DNAT, policy routing). We achieve this by
	// keeping EnableVPN=false and relying solely on SpoofTTL > 0 triggering
	// setupMangleRules, then adding MSS clamping unconditionally via a
	// separate call path in enableNAT.
	cfg.DPIBypass = true
}

// stopVPN brings down the VPN tunnel if it was started by routerd.
func stopVPN(cfg *Config, runDir string) {
	if data, err := os.ReadFile(filepath.Join(runDir, "vpn_started")); err == nil {
		confPath := strings.TrimSpace(string(data))
		if confPath != "" {
			logInfo("stopping WireGuard VPN tunnel (%s)", confPath)
			_, _ = runCmd("wg-quick", "down", confPath)
		}
		_ = os.Remove(filepath.Join(runDir, "vpn_started"))
	}
}

// deriveInterfaceFromConf extracts the interface name from a WireGuard conf
// file path (e.g. /etc/routerd/vpn.conf → "vpn"), falling back to defaultIface.
func deriveInterfaceFromConf(confPath, defaultIface string) string {
	base := filepath.Base(confPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name != "" {
		return name
	}
	if defaultIface != "" {
		return defaultIface
	}
	return "wg0"
}

// generateWARPConfigAuto attempts to use wgcf to register and generate a
// real Cloudflare WARP WireGuard profile. Returns an error if wgcf is not
// available or fails.
func generateWARPConfigAuto(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Check if wgcf is installed.
	if _, err := runCmd("which", "wgcf"); err != nil {
		return fmt.Errorf("wgcf not found in PATH (install: pacman -S wgcf or go install github.com/ViRb3/wgcf/v2@latest)")
	}

	// Run wgcf in a temp directory to avoid polluting cwd.
	tmpDir, err := os.MkdirTemp("", "routerd-wgcf-*")
	if err != nil {
		return fmt.Errorf("cannot create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	logInfo("running wgcf register...")
	if out, err := runCmdDir(tmpDir, "wgcf", "register", "--accept-tos"); err != nil {
		return fmt.Errorf("wgcf register failed: %s", strings.TrimSpace(out))
	}

	logInfo("running wgcf generate...")
	if out, err := runCmdDir(tmpDir, "wgcf", "generate"); err != nil {
		return fmt.Errorf("wgcf generate failed: %s", strings.TrimSpace(out))
	}

	// Find the generated profile (wgcf-profile.conf).
	profile := filepath.Join(tmpDir, "wgcf-profile.conf")
	if !fileExists(profile) {
		return fmt.Errorf("wgcf did not produce wgcf-profile.conf in %s", tmpDir)
	}

	data, err := os.ReadFile(profile)
	if err != nil {
		return fmt.Errorf("cannot read wgcf profile: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("cannot write VPN config: %w", err)
	}
	logInfo("wgcf auto-generated Cloudflare WARP profile at %s", path)
	return nil
}

// generateWARPConfig writes a commented-out WireGuard profile template.
// Users must fill in their keys before use.
func generateWARPConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	content := `# Cloudflare WARP / Generic WireGuard VPN Profile Template
# Generated by routerd
#
# To get a real WARP profile automatically, install wgcf and run:
#   sudo routerd warp-setup
#
# Or fill in your keys manually below and remove the leading '#' from each line.

[Interface]
# PrivateKey = YOUR_PRIVATE_KEY
# Address = 172.16.0.2/32, 2606:4700:110:8f2e:80b7:5d63:9b65:e594/128
# DNS = 1.1.1.1, 1.0.0.1

[Peer]
# PublicKey = bmXOC+F1FxEMF9dyiK2H5/1SUtzH0JuVo51h2wPfgyo=
# Endpoint = 162.159.192.1:2408
# AllowedIPs = 0.0.0.0/0, ::/0
`
	return os.WriteFile(path, []byte(content), 0600)
}

// isConfigUnconfigured returns true if the WireGuard config has no uncommented
// PrivateKey line — meaning it has not been filled in.
func isConfigUnconfigured(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "privatekey") {
			return false
		}
	}
	return true
}
