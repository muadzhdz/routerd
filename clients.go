// Package main implements the routerd daemon — a single-binary tool that turns
// any Linux machine with a Wi-Fi card into a stealth Wi-Fi access point, router,
// and transparent WireGuard VPN gateway.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client holds a connected station's MAC and IP address.
type Client struct {
	MAC string
	IP  string
}

// listStations returns connected clients from iw + dnsmasq lease file.
func listStations(ap, runDir string) []Client {
	out, err := runCmd("iw", "dev", ap, "station", "dump")
	if err != nil {
		return nil
	}
	// Build MAC list from station dump.
	var macs []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Station ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				macs = append(macs, strings.ToLower(parts[1]))
			}
		}
	}
	if len(macs) == 0 {
		return nil
	}

	// Build MAC→IP map from dnsmasq leases.
	leaseMap := parseDnsmasqLeases(runDir)

	clients := make([]Client, 0, len(macs))
	for _, mac := range macs {
		ip := leaseMap[mac]
		if ip == "" {
			ip = "(no lease)"
		}
		clients = append(clients, Client{MAC: mac, IP: ip})
	}
	return clients
}

// parseDnsmasqLeases reads /run/routerd/dnsmasq.leases (if present) and
// returns a map of lowercase MAC → IP.
func parseDnsmasqLeases(runDir string) map[string]string {
	m := make(map[string]string)
	data, err := os.ReadFile(filepath.Join(runDir, "dnsmasq.leases"))
	if err != nil {
		// Try default dnsmasq lease path.
		data, err = os.ReadFile("/var/lib/misc/dnsmasq.leases")
		if err != nil {
			return m
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			mac := strings.ToLower(fields[1])
			ip := fields[2]
			m[mac] = ip
		}
	}
	return m
}

// vpnEndpoint tries to read the WireGuard endpoint from `wg show`.
func vpnEndpoint(iface string) string {
	out, err := runCmd("wg", "show", iface, "endpoints")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] != "(none)" {
			return " " + fields[1]
		}
	}
	return ""
}

// uptimeSince formats duration since a Unix timestamp as "Xh Ym Zs".
func uptimeSince(startUnix int64) string {
	secs := time.Now().Unix() - startUnix
	if secs < 0 {
		secs = 0
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func timeNow() int64 {
	return time.Now().Unix()
}
