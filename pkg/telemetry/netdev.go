package telemetry

import (
	"os"
	"strconv"
	"strings"
)

// ParseNetDev extracts RX and TX bytes from /proc/net/dev formatted text for a specific interface.
// Linux /proc/net/dev line format:
// <iface>: <rx_bytes> <rx_packets> <rx_errs> <rx_drop> <rx_fifo> <rx_frame> <rx_compressed> <rx_multicast> <tx_bytes> <tx_packets> ...
func ParseNetDev(content string, iface string) (rx uint64, tx uint64) {
	if strings.TrimSpace(content) == "" || iface == "" {
		return 0, 0
	}

	targetPrefix := iface + ":"
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, targetPrefix) || strings.Contains(trimmed, targetPrefix) {
			sanitized := strings.ReplaceAll(trimmed, ":", " ")
			fields := strings.Fields(sanitized)
			if len(fields) >= 10 {
				r, _ := strconv.ParseUint(fields[1], 10, 64)
				t, _ := strconv.ParseUint(fields[9], 10, 64)
				return r, t
			}
		}
	}
	return 0, 0
}

// ReadNetDev reads the Linux kernel virtual file /proc/net/dev.
func ReadNetDev(iface string) (rx uint64, tx uint64) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	return ParseNetDev(string(data), iface)
}
