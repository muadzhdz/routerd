package hotspot

import (
	"regexp"
	"sort"
	"strings"
)

// StationStats stores Wi-Fi signal and bitrate information parsed from station dump.
type StationStats struct {
	Signal    string
	TxBitrate string
}

// ParseDHCPLeases parses dnsmasq lease file content and returns a MAC -> ConnectedClient map.
// Standard dnsmasq lease format: <timestamp> <mac> <ip> <hostname> <client-id>
func ParseDHCPLeases(content string) map[string]ConnectedClient {
	clients := make(map[string]ConnectedClient)
	if strings.TrimSpace(content) == "" {
		return clients
	}

	lines := regexp.MustCompile(`\r?\n`).Split(content, -1)
	for _, line := range lines {
		fields := regexp.MustCompile(`\s+`).Split(strings.TrimSpace(line), -1)
		if len(fields) >= 4 {
			mac := strings.ToLower(fields[1])
			ip := fields[2]
			hostname := fields[3]
			if hostname == "*" || hostname == "" {
				hostname = "Unknown Device"
			}
			clients[mac] = ConnectedClient{
				MAC:       mac,
				IP:        ip,
				Hostname:  hostname,
				Signal:    "N/A",
				TxBitrate: "N/A",
			}
		}
	}
	return clients
}

// ParseStationDump parses the text output of 'iw dev <iface> station dump'.
func ParseStationDump(output string) map[string]StationStats {
	stations := make(map[string]StationStats)
	if strings.TrimSpace(output) == "" {
		return stations
	}

	currentMAC := ""
	currentStats := StationStats{Signal: "N/A", TxBitrate: "N/A"}

	lines := regexp.MustCompile(`\r?\n`).Split(output, -1)
	for _, line := range lines {
		if matches := regexp.MustCompile(`Station\s+([0-9a-fA-F:]+)`).FindStringSubmatch(line); len(matches) > 1 {
			if currentMAC != "" {
				stations[currentMAC] = currentStats
			}
			currentMAC = strings.ToLower(matches[1])
			currentStats = StationStats{Signal: "N/A", TxBitrate: "N/A"}
		} else if currentMAC != "" {
			if sigMatches := regexp.MustCompile(`signal:\s+([-\d]+)\s+dBm`).FindStringSubmatch(line); len(sigMatches) > 1 {
				currentStats.Signal = sigMatches[1] + " dBm"
			}
			if txMatches := regexp.MustCompile(`tx bitrate:\s+([^\n]+)`).FindStringSubmatch(line); len(txMatches) > 1 {
				currentStats.TxBitrate = strings.TrimSpace(txMatches[1])
			}
		}
	}

	if currentMAC != "" {
		stations[currentMAC] = currentStats
	}

	return stations
}

// MergeClientStats merges DHCP lease data with Wi-Fi signal telemetry from station dump.
func MergeClientStats(leases map[string]ConnectedClient, stats map[string]StationStats) []ConnectedClient {
	merged := make(map[string]ConnectedClient)

	for mac, client := range leases {
		if st, found := stats[mac]; found {
			client.Signal = st.Signal
			client.TxBitrate = st.TxBitrate
		}
		merged[mac] = client
	}

	for mac, st := range stats {
		if _, found := merged[mac]; !found {
			merged[mac] = ConnectedClient{
				MAC:       mac,
				IP:        "Dynamic",
				Hostname:  "Wi-Fi Station",
				Signal:    st.Signal,
				TxBitrate: st.TxBitrate,
			}
		}
	}

	var result []ConnectedClient
	for _, c := range merged {
		result = append(result, c)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].IP < result[j].IP
	})

	return result
}
