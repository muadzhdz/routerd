package hotspot

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

const (
	hotspotSubnet    = "10.42.0.0/24"
	hotspotIP        = "10.42.0.1"
	apInterface      = "ap0"
	hostapdConf      = "/tmp/routerd-hostapd.conf"
	hostapdLogPath   = "/tmp/routerd-hostapd.log"
	dnsmasqLeasePath = "/tmp/routerd-dnsmasq.leases"
)

var (
	hostapdCmd  *exec.Cmd
	dnsmasqCmd  *exec.Cmd
	parentIface string
)

// ConnectedClient merepresentasikan metadata perangkat yang terhubung ke hotspot
type ConnectedClient struct {
	MAC       string
	IP        string
	Hostname  string
	Signal    string
	TxBitrate string
}

// getWifiChannel mendeteksi channel dan mode frekuensi Wi-Fi dari interface induk
func getWifiChannel(iface string) (int, string) {
	out, err := exec.Command("iw", "dev", iface, "info").Output()
	if err != nil {
		return 36, "a"
	}
	re := regexp.MustCompile(`channel\s+(\d+)`)
	matches := re.FindStringSubmatch(string(out))
	if len(matches) > 1 {
		ch, err := strconv.Atoi(matches[1])
		if err == nil {
			if ch <= 14 {
				return ch, "g"
			}
			return ch, "a"
		}
	}
	return 36, "a"
}

// getVirtualMAC membuat locally administered MAC address agar tidak bentrok dengan BSSID fisik
func getVirtualMAC(iface string) string {
	netIface, err := net.InterfaceByName(iface)
	if err != nil || len(netIface.HardwareAddr) != 6 {
		return ""
	}
	mac := make([]byte, 6)
	copy(mac, netIface.HardwareAddr)
	mac[0] ^= 0x02 // locally administered unicast bit
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}

// StartHotspot menyalakan Wi-Fi Hotspot via hostapd & dnsmasq di interface virtual ap0
func StartHotspot(ifaceName, ssid, password string) error {
	parentIface = ifaceName
	log.Printf("HOTSPOT: Menyiapkan interface virtual [%s] di atas [%s]...", apInterface, ifaceName)

	// 1. Bersihkan sisa ap0 lama jika ada
	_ = exec.Command("iw", "dev", apInterface, "del").Run()

	// 2. Deteksi channel interface induk agar radio Wi-Fi tetap sinkron
	ch, hwMode := getWifiChannel(ifaceName)
	log.Printf("HOTSPOT: Sinkronisasi frekuensi radio -> Channel %d (Band %s)", ch, hwMode)

	// 3. Buat interface virtual ap0 bertipe __ap dengan MAC unik (locally administered)
	vmac := getVirtualMAC(ifaceName)
	var cmdAdd *exec.Cmd
	if vmac != "" {
		cmdAdd = exec.Command("iw", "dev", ifaceName, "interface", "add", apInterface, "type", "__ap", "addr", vmac)
	} else {
		cmdAdd = exec.Command("iw", "dev", ifaceName, "interface", "add", apInterface, "type", "__ap")
	}
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		// Fallback tanpa addr jika driver menolak opsi addr eksplisit
		cmdFallback := exec.Command("iw", "dev", ifaceName, "interface", "add", apInterface, "type", "__ap")
		if out2, err2 := cmdFallback.CombinedOutput(); err2 != nil {
			return fmt.Errorf("gagal membuat virtual AP interface: %s / %s (%w)", string(out), string(out2), err2)
		}
	}

	// 4. Isolasi dari NetworkManager agar ap0 tidak dibajak / dimatikan otomatis
	_ = exec.Command("nmcli", "device", "set", apInterface, "managed", "no").Run()

	// 5. Tulis konfigurasi hostapd (WMM & HT wajib untuk 802.11n)
	confContent := fmt.Sprintf(`interface=%s
driver=nl80211
ssid=%s
hw_mode=%s
channel=%d
ieee80211n=1
wmm_enabled=1
ht_capab=[HT20]
auth_algs=1
wpa=2
wpa_passphrase=%s
wpa_key_mgmt=WPA-PSK
rsn_pairwise=CCMP
`, apInterface, ssid, hwMode, ch, password)

	if err := os.WriteFile(hostapdConf, []byte(confContent), 0600); err != nil {
		StopHotspot()
		return fmt.Errorf("gagal menulis hostapd config: %w", err)
	}

	// 6. Jalankan hostapd di background dan tangkap log diagnostik
	hostapdLog, err := os.OpenFile(hostapdLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		StopHotspot()
		return fmt.Errorf("gagal membuat file log hostapd: %w", err)
	}
	defer hostapdLog.Close()

	hostapdCmd = exec.Command("hostapd", "-d", hostapdConf)
	hostapdCmd.Stdout = hostapdLog
	hostapdCmd.Stderr = hostapdLog

	if err := hostapdCmd.Start(); err != nil {
		StopHotspot()
		return fmt.Errorf("gagal menjalankan hostapd: %w", err)
	}

	// 7. Health-check: Pastikan hostapd benar-benar running dan tidak crash seketika
	time.Sleep(1 * time.Second)
	if err := hostapdCmd.Process.Signal(syscall.Signal(0)); err != nil {
		logData, _ := os.ReadFile(hostapdLogPath)
		StopHotspot()
		return fmt.Errorf("hostapd CRASH saat inisialisasi driver/radio:\n%s", string(logData))
	}

	// 8. Assign IP Gateway 10.42.0.1 ke interface ap0 setelah hostapd siap
	_ = exec.Command("ip", "addr", "add", hotspotIP+"/24", "dev", apInterface).Run()
	_ = exec.Command("ip", "link", "set", apInterface, "up").Run()

	// 9. Jalankan dnsmasq (DHCP Server only, tanpa DNS port 53 listener)
	dnsmasqCmd = exec.Command("dnsmasq",
		"--no-hosts",
		"--keep-in-foreground",
		"--listen-address="+hotspotIP,
		"--dhcp-range=10.42.0.10,10.42.0.50,255.255.255.0,12h",
		"--dhcp-option=3,"+hotspotIP,
		"--dhcp-option=6,"+hotspotIP,
		"--dhcp-leasefile="+dnsmasqLeasePath,
		"-i", apInterface,
		"--bind-interfaces",
		"-p", "0",
	)
	if err := dnsmasqCmd.Start(); err != nil {
		StopHotspot()
		return fmt.Errorf("gagal menjalankan dnsmasq: %w", err)
	}

	// 10. Firewall Routing & Stealth NAT
	_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	_ = exec.Command("sysctl", "-w", "net.ipv4.conf.all.route_localnet=1").Run()

	// Redirection DNS: Belokkan semua port 53 dari Hotspot ke Stealth DoH 127.0.0.1:53
	_ = exec.Command("iptables", "-t", "nat", "-I", "PREROUTING", "1", "-i", apInterface, "-p", "udp", "--dport", "53", "-j", "DNAT", "--to-destination", "127.0.0.1:53").Run()
	_ = exec.Command("iptables", "-t", "nat", "-I", "PREROUTING", "1", "-i", apInterface, "-p", "tcp", "--dport", "53", "-j", "DNAT", "--to-destination", "127.0.0.1:53").Run()

	// Masquerade NAT agar client hotspot bisa internetan lewat interface WAN (wlp2s0)
	_ = exec.Command("iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-s", hotspotSubnet, "-o", ifaceName, "-j", "MASQUERADE").Run()
	_ = exec.Command("iptables", "-I", "FORWARD", "1", "-i", apInterface, "-o", ifaceName, "-j", "ACCEPT").Run()
	_ = exec.Command("iptables", "-I", "FORWARD", "1", "-i", ifaceName, "-o", apInterface, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").Run()

	log.Printf("SUCCESS: Wi-Fi Hotspot [%s] AKTIF di [%s]! (Password: %s)", ssid, apInterface, password)
	log.Printf("SUCCESS: DHCP Server [10.42.0.10 - 10.42.0.50] siap melayani HP/Client")
	log.Printf("SUCCESS: Stealth NAT Active -> Semua DNS client disedot ke DoH 127.0.0.1:53")
	return nil
}

// GetConnectedClients membaca daftar client dari lease DHCP dan radio Wi-Fi
func GetConnectedClients() ([]ConnectedClient, error) {
	clientsMap := make(map[string]*ConnectedClient)

	// 1. Baca DHCP lease file
	data, err := os.ReadFile(dnsmasqLeasePath)
	if err == nil {
		lines := regexp.MustCompile(`\r?\n`).Split(string(data), -1)
		for _, line := range lines {
			fields := regexp.MustCompile(`\s+`).Split(line, -1)
			if len(fields) >= 4 {
				mac := fields[1]
				ip := fields[2]
				hostname := fields[3]
				if hostname == "*" {
					hostname = "Unknown Device"
				}
				clientsMap[mac] = &ConnectedClient{
					MAC:      mac,
					IP:       ip,
					Hostname: hostname,
					Signal:   "N/A",
				}
			}
		}
	}

	// 2. Baca signal & bitrate dari iw dev ap0 station dump
	out, err := exec.Command("iw", "dev", apInterface, "station", "dump").Output()
	if err == nil {
		currentMAC := ""
		lines := regexp.MustCompile(`\r?\n`).Split(string(out), -1)
		for _, line := range lines {
			if matches := regexp.MustCompile(`Station\s+([0-9a-fA-F:]+)`).FindStringSubmatch(line); len(matches) > 1 {
				currentMAC = matches[1]
				if _, ok := clientsMap[currentMAC]; !ok {
					clientsMap[currentMAC] = &ConnectedClient{
						MAC:      currentMAC,
						IP:       "Dynamic",
						Hostname: "Wi-Fi Station",
					}
				}
			} else if currentMAC != "" {
				if sigMatches := regexp.MustCompile(`signal:\s+([-\d]+)\s+dBm`).FindStringSubmatch(line); len(sigMatches) > 1 {
					clientsMap[currentMAC].Signal = sigMatches[1] + " dBm"
				}
				if txMatches := regexp.MustCompile(`tx bitrate:\s+([^\n]+)`).FindStringSubmatch(line); len(txMatches) > 1 {
					clientsMap[currentMAC].TxBitrate = txMatches[1]
				}
			}
		}
	}

	var result []ConnectedClient
	for _, c := range clientsMap {
		result = append(result, *c)
	}
	return result, nil
}

// StopHotspot mematikan hostapd, dnsmasq, dan membersihkan NAT
func StopHotspot() {
	log.Println("HOTSPOT: Mematikan Wi-Fi Hotspot dan membersihkan NAT...")

	if hostapdCmd != nil && hostapdCmd.Process != nil {
		_ = hostapdCmd.Process.Kill()
		_ = hostapdCmd.Wait()
	}
	if dnsmasqCmd != nil && dnsmasqCmd.Process != nil {
		_ = dnsmasqCmd.Process.Kill()
		_ = dnsmasqCmd.Wait()
	}

	// Bersihkan iptables rules
	_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-i", apInterface, "-p", "udp", "--dport", "53", "-j", "DNAT", "--to-destination", "127.0.0.1:53").Run()
	_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-i", apInterface, "-p", "tcp", "--dport", "53", "-j", "DNAT", "--to-destination", "127.0.0.1:53").Run()
	if parentIface != "" {
		_ = exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", hotspotSubnet, "-o", parentIface, "-j", "MASQUERADE").Run()
		_ = exec.Command("iptables", "-D", "FORWARD", "-i", apInterface, "-o", parentIface, "-j", "ACCEPT").Run()
		_ = exec.Command("iptables", "-D", "FORWARD", "-i", parentIface, "-o", apInterface, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").Run()
	}

	_ = exec.Command("nmcli", "device", "set", apInterface, "managed", "yes").Run()
	_ = exec.Command("iw", "dev", apInterface, "del").Run()
	_ = os.Remove(hostapdConf)
	_ = os.Remove(hostapdLogPath)
	_ = os.Remove(dnsmasqLeasePath)
	_ = exec.Command("sysctl", "-w", "net.ipv4.conf.all.route_localnet=0").Run()

	log.Println("SUCCESS: Hotspot & NAT berhasil dibersihkan dengan aman!")
}
