package main

import (
	"fmt"
	"regexp"
	"strings"
)

// findSTAInterface resolves the wireless client interface that carries the
// internet connection. If not forced by config, it picks the first wireless
// interface that is currently associated with a network.
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
	for _, iface := range strings.Fields(out) {
		if iface == "lo" || iface == cfg.InterfaceAP {
			continue
		}
		if !isWireless(iface) {
			continue
		}
		link, err := runCmd("iw", "dev", iface, "link")
		if err != nil {
			continue
		}
		if strings.Contains(link, "Connected to") {
			return iface, nil
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
	if out, err := runCmd("iw", "dev", sta, "interface", "add", ap, "type", "__ap", "addr", mac); err == nil {
		return nil
	} else {
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

// setManaged tells NetworkManager to leave the interface alone.
func setUnmanaged(iface string) {
	out, err := runCmd("nmcli", "device", "set", iface, "managed", "no")
	if err != nil {
		logWarn("could not mark %q unmanaged in NetworkManager: %s", iface, strings.TrimSpace(out))
	}
}

func countStations(ap string) int {
	out, err := runCmd("iw", "dev", ap, "station", "dump")
	if err != nil {
		return -1
	}
	return strings.Count(out, "Station ")
}
