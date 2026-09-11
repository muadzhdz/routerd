package vpn

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Profile holds the parsed WireGuard connection metadata.
type Profile struct {
	InterfaceName string
	Address       string
	PrivateKey    string
	PublicKey     string
	Endpoint      string
	AllowedIPs    string
}

// DeriveInterfaceName extracts a clean network interface name from the
// WireGuard configuration file path (e.g. '/etc/routerd/vpn.conf' -> 'vpn').
func DeriveInterfaceName(confPath, fallback string) string {
	base := filepath.Base(confPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if name != "" && name != "." {
		return name
	}
	if fallback != "" {
		return fallback
	}
	return "wg0"
}

// IsConfigured returns true if the WireGuard configuration file exists and contains
// an active, uncommented PrivateKey with a real value (not placeholder text).
func IsConfigured(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "privatekey") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				if val != "" && !strings.Contains(strings.ToLower(val), "your_private_key") && !strings.Contains(val, "...") {
					return true
				}
			}
		}
	}

	return false
}

// PrepareRuntimeConfig copies the source WireGuard profile to the runtime directory
// and comments out any 'DNS = ...' directives. This prevents wg-quick from invoking
// resolvconf (which crashes on systemd-resolved systems with 'signature mismatch' and
// conflicts with routerd's own local DNS resolver).
func PrepareRuntimeConfig(sourcePath, runDir string) (string, error) {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("failed to read source VPN config: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var modified []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "dns") {
			rest := strings.TrimSpace(trimmed[3:])
			if strings.HasPrefix(rest, "=") {
				modified = append(modified, "# "+line+" # commented by routerd to prevent resolvconf conflicts")
				continue
			}
		}
		modified = append(modified, line)
	}

	if err := os.MkdirAll(runDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create VPN runtime directory: %w", err)
	}

	runtimePath := filepath.Join(runDir, filepath.Base(sourcePath))
	if err := os.WriteFile(runtimePath, []byte(strings.Join(modified, "\n")), 0600); err != nil {
		return "", fmt.Errorf("failed to write runtime VPN config: %w", err)
	}

	return runtimePath, nil
}
