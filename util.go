package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func runCmd(name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

// runCmdDir runs a command in the specified working directory.
func runCmdDir(dir, name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isWireless(iface string) bool {
	return fileExists("/sys/class/net/" + iface + "/phy80211")
}

// gatewayIP returns the first usable IP (.1) inside a CIDR subnet.
func gatewayIP(cidr string) (net.IP, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet %q: %v", cidr, err)
	}
	base := ipnet.IP.To4()
	if base == nil {
		base = ip.To4()
	}
	if base == nil {
		return nil, fmt.Errorf("subnet %q is not IPv4", cidr)
	}
	out := make(net.IP, 4)
	copy(out, base)
	out[3]++
	return out, nil
}

// broadcastIP returns the broadcast address of a CIDR subnet.
func broadcastIP(cidr string) (net.IP, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	bc := make(net.IP, 4)
	copy(bc, ipnet.IP.To4())
	for i, b := range ipnet.Mask {
		bc[i] |= ^b
	}
	return bc, nil
}

// gatewayCIDR returns the gateway IP of a subnet as a CIDR, e.g.
// "192.168.50.0/24" -> "192.168.50.1/24".
func gatewayCIDR(cidr string) (string, error) {
	gw, err := gatewayIP(cidr)
	if err != nil {
		return "", err
	}
	i := strings.LastIndexByte(cidr, '/')
	if i < 0 {
		return "", fmt.Errorf("invalid subnet %q", cidr)
	}
	return gw.String() + cidr[i:], nil
}

var chanRe = regexp.MustCompile(`(?m)freq:\s*(\d+)`)

func detectChannel(sta string) (int, string, error) {
	out, err := runCmd("iw", "dev", sta, "link")
	if err != nil {
		return 0, "", fmt.Errorf("cannot read link state of %s: %s", sta, strings.TrimSpace(out))
	}
	m := chanRe.FindStringSubmatch(out)
	if len(m) < 2 {
		return 0, "", fmt.Errorf("cannot read channel of %s (not connected to any network?)", sta)
	}
	freq, _ := strconv.Atoi(m[1])
	ch, band := freqToChannel(freq)
	if ch == 0 {
		return 0, "", fmt.Errorf("unsupported frequency %d MHz", freq)
	}
	return ch, band, nil
}

func freqToChannel(freq int) (int, string) {
	if freq >= 2412 && freq <= 2484 {
		return (freq-2412)/5 + 1, "g"
	}
	if freq >= 5180 && freq <= 5825 {
		return (freq - 5000) / 5, "a"
	}
	return 0, ""
}

// localAdminMAC derives a locally-administered MAC for the virtual AP
// interface from the physical interface MAC (avoids collisions).
func localAdminMAC(phys string) string {
	data, err := os.ReadFile("/sys/class/net/" + phys + "/address")
	if err != nil {
		return randomMAC()
	}
	parts := strings.Split(strings.TrimSpace(string(data)), ":")
	if len(parts) != 6 {
		return randomMAC()
	}
	// set locally-administered bit and flip low bit of first octet
	b, _ := strconv.ParseUint(parts[0], 16, 8)
	b = (b ^ 0x02) | 0x02
	parts[0] = fmt.Sprintf("%02x", b)
	return strings.Join(parts, ":")
}

// randomMAC generates a cryptographically random locally-administered MAC address.
func randomMAC() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "02:00:00:00:00:01"
	}
	// Set LAA bit (bit 1 of byte 0) and clear multicast bit (bit 0 of byte 0)
	b[0] = (b[0] | 0x02) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

// generateRandomSubnet picks a random RFC1918 IPv4 /24 subnet.
func generateRandomSubnet() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	switch b[0] % 3 {
	case 0: // 10.X.Y.0/24
		return fmt.Sprintf("10.%d.%d.0/24", b[1], b[2])
	case 1: // 172.(16..31).X.0/24
		secondOctet := 16 + (int(b[1]) % 16)
		return fmt.Sprintf("172.%d.%d.0/24", secondOctet, b[2])
	default: // 192.168.X.0/24 (avoid 0, 1, 50)
		thirdOctet := int(b[1])
		if thirdOctet == 0 || thirdOctet == 1 || thirdOctet == 50 {
			thirdOctet += 10
		}
		return fmt.Sprintf("192.168.%d.0/24", thirdOctet)
	}
}
