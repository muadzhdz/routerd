package netutil

import (
	"fmt"
	"net"
	"os"
)

// GetDefaultInterface resolves the active network interface that has a route to the internet.
func GetDefaultInterface() (*net.Interface, error) {
	// 1. Probe the Linux routing table by dialing a remote address
	conn, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return nil, fmt.Errorf("no internet connection: %w", err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	// 2. Identify the physical/virtual interface bound to this local IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to read network interfaces: %w", err)
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.Equal(localAddr.IP) {
				return &iface, nil
			}
		}
	}

	return nil, fmt.Errorf("network interface for IP %s not found", localAddr.IP)
}

// IsWireless checks if an interface is a wireless device via Linux sysfs (/sys/class/net/<iface>/wireless).
func IsWireless(ifaceName string) bool {
	path := fmt.Sprintf("/sys/class/net/%s/wireless", ifaceName)
	_, err := netutilStat(path)
	return err == nil
}

var netutilStat = func(name string) (any, error) {
	return os.Stat(name)
}
