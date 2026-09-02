package main

import (
	"fmt"
	"regexp"
	"strings"
)

// findSTAInterface resolves the wireless client interface that carries the
// internet connection. If not forced by config, it tries three strategies:
//  1. iw dev <iface> link  — looks for "Connected to" (classic wpa_supplicant)
//  2. iw dev global dump   — looks for an interface with a non-zero channel
//     (works on wpa_supplicant -u / DBus mode used by systemd-networkd setups)
//  3. ip route show default — picks the wireless interface that holds the
//     default route (catches any other managed-uplink scenario)
func findSTAInterface(cfg *Config) (string, error) {
	if cfg.InterfaceSTA != "" && cfg.InterfaceSTA != "auto" {
		if !isWireless(cfg.InterfaceSTA) {
			return "", fmt.Errorf("configured INTERFACE_STA %q is not a wireless interface", cfg.InterfaceSTA)
		}
		return cfg.InterfaceSTA, nil
	}

	out, err := runCmd("ls", "/sys/class/net/")
	if err != nil {
		return "", err
	}
	var wirelessIfaces []string
	for _, iface := range strings.Fields(out) {
		if iface == "lo" || iface == cfg.InterfaceAP {
			continue
		}
		if isWireless(iface) {
			wirelessIfaces = append(wirelessIfaces, iface)
		}
	}
	if len(wirelessIfaces) == 0 {
		return "", fmt.Errorf("no wireless interfaces found (check: iw dev)")
	}

	// --- Strategy 1: iw dev <iface> link — "Connected to" ------------------
	for _, iface := range wirelessIfaces {
		link, err := runCmd(iwBin(), "dev", iface, "link")
		if err != nil {
			continue
		}
		if strings.Contains(link, "Connected to") {
			return iface, nil
		}
	}

	// --- Strategy 2: iw dev global dump — interface has an active channel ---
	// Useful for wpa_supplicant -u (DBus) mode used by systemd-networkd setups
	// where iw dev <iface> link may not show "Connected to" even when connected.
	if devOut, devErr := runCmd(iwBin(), "dev"); devErr == nil && devOut != "" {
		chanLineRe := regexp.MustCompile(`channel\s+\d+\s+\(\d+\s+MHz\)`)
		currentIface := ""
		for _, line := range strings.Split(devOut, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Interface ") {
				currentIface = strings.TrimPrefix(trimmed, "Interface ")
				continue
			}
			if currentIface == "" || currentIface == cfg.InterfaceAP {
				continue
			}
			if chanLineRe.MatchString(trimmed) && isWireless(currentIface) {
				return currentIface, nil
			}
		}
	}

	// --- Strategy 3: iw dev <iface> info — check each wireless iface -------
	for _, iface := range wirelessIfaces {
		out, err := runCmd(iwBin(), "dev", iface, "info")
		if err != nil {
			continue
		}
		// If the interface has a channel, it's active/associated.
		if regexp.MustCompile(`channel\s+\d+`).MatchString(out) {
			return iface, nil
		}
	}

	// --- Strategy 3: ip route show default — wireless iface on default route -
	if routeOut, routeErr := runCmd("ip", "route", "show", "default"); routeErr == nil {
		re := regexp.MustCompile(`dev\s+(\S+)`)
		for _, m := range re.FindAllStringSubmatch(routeOut, -1) {
			if len(m) >= 2 && isWireless(m[1]) && m[1] != cfg.InterfaceAP {
				return m[1], nil
			}
		}
	}

	return "", fmt.Errorf("no associated wireless client interface found (check: iw dev)")
}

// detectUplink resolves the interface used for NAT masquerading.
func detectUplink(cfg *Config, sta string) (string, error) {
	if cfg.Uplink != "" && cfg.Uplink != "auto" {
		return cfg.Uplink, nil
	}
	out, err := runCmd("ip", "route", "show", "default")
	if err == nil {
		re := regexp.MustCompile(`dev (\S+)`)
		if m := re.FindStringSubmatch(out); len(m) > 1 {
			return m[1], nil
		}
	}
	if sta != "" {
		return sta, nil
	}
	return "", fmt.Errorf("cannot determine uplink interface (set UPLINK in config)")
}

// createAPInterface adds a virtual AP interface on the same phy as the STA
// interface, so the card acts as client and access point simultaneously.
func createAPInterface(sta, ap string, useRandomMAC bool) error {
	_ = deleteAPInterface(ap)
	var mac string
	if useRandomMAC {
		mac = randomMAC()
		logInfo("generated random MAC for %s: %s", ap, mac)
	} else {
		mac = localAdminMAC(sta)
		logInfo("using deterministic LAA MAC for %s: %s", ap, mac)
	}

	// Preferred: create the interface directly in AP mode.
	out, err := runCmd("iw", "dev", sta, "interface", "add", ap, "type", "__ap", "addr", mac)
	if err == nil {
		return nil
	}
	lastErr := strings.TrimSpace(out)

	// Fallback: create managed, bring it up, then switch to AP.
	if out2, err2 := runCmd("iw", "dev", sta, "interface", "add", ap, "type", "managed", "addr", mac); err2 != nil {
		return fmt.Errorf("cannot create AP interface %q on %q: %s / %s", ap, sta, lastErr, strings.TrimSpace(out2))
	}
	if out2, err2 := runCmd("ip", "link", "set", ap, "up"); err2 != nil {
		return fmt.Errorf("cannot bring up %q: %s", ap, strings.TrimSpace(out2))
	}
	if out2, err2 := runCmd("iw", "dev", ap, "set", "type", "ap"); err2 != nil {
		return fmt.Errorf("cannot switch %q to AP mode: %s", ap, strings.TrimSpace(out2))
	}
	return nil
}

func deleteAPInterface(ap string) error {
	out, err := runCmd("iw", "dev", ap, "del")
	if err != nil {
		if strings.Contains(out, "No such device") || strings.Contains(out, "not found") {
			return nil
		}
		return fmt.Errorf("cannot delete AP interface %q: %s", ap, strings.TrimSpace(out))
	}
	return nil
}

func interfaceUp(iface string) error {
	out, err := runCmd("ip", "link", "set", iface, "up")
	if err != nil {
		return fmt.Errorf("cannot bring up %q: %s", iface, strings.TrimSpace(out))
	}
	return nil
}

func addrAdd(iface, cidr string) error {
	out, err := runCmd("ip", "addr", "add", cidr, "dev", iface)
	if err != nil {
		if strings.Contains(out, "File exists") {
			return nil
		}
		return fmt.Errorf("cannot assign %s to %q: %s", cidr, iface, strings.TrimSpace(out))
	}
	return nil
}

func addrDel(iface, cidr string) {
	out, err := runCmd("ip", "addr", "del", cidr, "dev", iface)
	if err != nil && !strings.Contains(out, "Cannot assign requested address") {
		logWarn("cleanup: %v (%s)", err, strings.TrimSpace(out))
	}
}

// setUnmanaged tells NetworkManager to leave the interface alone.
// On systems using systemd-networkd instead of NetworkManager (e.g. Omarchy,
// Arch with networkd), nmcli is not available — the warning is expected and
// harmless. The interface is protected from networkd via a .network file drop-in
// (see install.sh for the systemd-networkd unmanaged rule).
func setUnmanaged(iface string) {
	// Only attempt if nmcli is available.
	nmcli := "/usr/bin/nmcli"
	if !fileExists(nmcli) {
		nmcli = "nmcli"
	}
	out, err := runCmd(nmcli, "device", "set", iface, "managed", "no")
	if err != nil {
		// Suppress the warning on non-NM systems — not a real error.
		if !strings.Contains(out, "not found") && !strings.Contains(err.Error(), "not found") &&
			!strings.Contains(err.Error(), "executable") {
			logWarn("could not mark %q unmanaged in NetworkManager: %s", iface, strings.TrimSpace(out))
		}
	}
}

func countStations(ap string) int {
	out, err := runCmd("iw", "dev", ap, "station", "dump")
	if err != nil {
		return -1
	}
	return strings.Count(out, "Station ")
}
