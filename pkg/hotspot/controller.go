package hotspot

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	defaultSubnet    = "10.42.0.0/24"
	defaultIP        = "10.42.0.1"
	defaultInterface = "ap0"
	hostapdConf      = "/tmp/routerd-hostapd.conf"
	hostapdLogPath   = "/tmp/routerd-hostapd.log"
	dnsmasqLeasePath = "/tmp/routerd-dnsmasq.leases"
)

// Config membawa konfigurasi untuk inisialisasi AP Controller
type Config struct {
	ParentIface string
	SSID        string
	Password    string
	GatewayIP   string // Default "10.42.0.1"
	SubnetCIDR  string // Default "10.42.0.0/24"
	APInterface string // Default "ap0"
}

// Controller mengelola lifecycle hostapd, dnsmasq, dan iptables Stealth NAT dengan rollback LIFO.
type Controller struct {
	cfg        Config
	mu         sync.Mutex
	running    bool
	cleanups   []func()
	hostapdCmd *exec.Cmd
	dnsmasqCmd *exec.Cmd
}

// NewController membuat instance AP Controller baru dengan parameter default.
func NewController(cfg Config) *Controller {
	if cfg.GatewayIP == "" {
		cfg.GatewayIP = defaultIP
	}
	if cfg.SubnetCIDR == "" {
		cfg.SubnetCIDR = defaultSubnet
	}
	if cfg.APInterface == "" {
		cfg.APInterface = defaultInterface
	}
	return &Controller{
		cfg: cfg,
	}
}

func (c *Controller) addCleanup(fn func()) {
	c.cleanups = append(c.cleanups, fn)
}

func (c *Controller) rollback() {
	for i := len(c.cleanups) - 1; i >= 0; i-- {
		c.cleanups[i]()
	}
	c.cleanups = nil
}

// Start menjalankan Wi-Fi Hotspot, DHCP server, dan Stealth NAT dengan garansi atomik.
func (c *Controller) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return errors.New("AP Controller sudah berjalan")
	}

	log.Printf("HOTSPOT: Menyiapkan interface virtual [%s] di atas [%s]...", c.cfg.APInterface, c.cfg.ParentIface)

	// 1. Bersihkan sisa ap0 lama jika ada
	_ = exec.Command("iw", "dev", c.cfg.APInterface, "del").Run()

	// 2. Deteksi channel interface induk
	ch, hwMode := c.getWifiChannel(c.cfg.ParentIface)
	log.Printf("HOTSPOT: Sinkronisasi frekuensi radio -> Channel %d (Band %s)", ch, hwMode)

	// 3. Buat interface virtual ap0 dengan locally administered MAC
	vmac := c.getVirtualMAC(c.cfg.ParentIface)
	var cmdAdd *exec.Cmd
	if vmac != "" {
		cmdAdd = exec.Command("iw", "dev", c.cfg.ParentIface, "interface", "add", c.cfg.APInterface, "type", "__ap", "addr", vmac)
	} else {
		cmdAdd = exec.Command("iw", "dev", c.cfg.ParentIface, "interface", "add", c.cfg.APInterface, "type", "__ap")
	}
	if out, err := cmdAdd.CombinedOutput(); err != nil {
		cmdFallback := exec.Command("iw", "dev", c.cfg.ParentIface, "interface", "add", c.cfg.APInterface, "type", "__ap")
		if out2, err2 := cmdFallback.CombinedOutput(); err2 != nil {
			return fmt.Errorf("gagal membuat virtual AP interface: %s / %s (%w)", string(out), string(out2), err2)
		}
	}
	c.addCleanup(func() {
		_ = exec.Command("iw", "dev", c.cfg.APInterface, "del").Run()
	})

	// 4. Isolasi dari NetworkManager
	_ = exec.Command("nmcli", "device", "set", c.cfg.APInterface, "managed", "no").Run()
	c.addCleanup(func() {
		_ = exec.Command("nmcli", "device", "set", c.cfg.APInterface, "managed", "yes").Run()
	})

	// 5. Tulis konfigurasi hostapd
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
`, c.cfg.APInterface, c.cfg.SSID, hwMode, ch, c.cfg.Password)

	if err := os.WriteFile(hostapdConf, []byte(confContent), 0600); err != nil {
		c.rollback()
		return fmt.Errorf("gagal menulis hostapd config: %w", err)
	}
	c.addCleanup(func() {
		_ = os.Remove(hostapdConf)
		_ = os.Remove(hostapdLogPath)
	})

	// 6. Jalankan hostapd
	hostapdLog, err := os.OpenFile(hostapdLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		c.rollback()
		return fmt.Errorf("gagal membuat file log hostapd: %w", err)
	}
	defer hostapdLog.Close()

	c.hostapdCmd = exec.Command("hostapd", "-d", hostapdConf)
	c.hostapdCmd.Stdout = hostapdLog
	c.hostapdCmd.Stderr = hostapdLog

	if err := c.hostapdCmd.Start(); err != nil {
		c.rollback()
		return fmt.Errorf("gagal menjalankan hostapd: %w", err)
	}
	c.addCleanup(func() {
		if c.hostapdCmd != nil && c.hostapdCmd.Process != nil {
			_ = c.hostapdCmd.Process.Kill()
			_ = c.hostapdCmd.Wait()
		}
	})

	// 7. Health-check hostapd
	time.Sleep(1 * time.Second)
	if err := c.hostapdCmd.Process.Signal(syscall.Signal(0)); err != nil {
		logData, _ := os.ReadFile(hostapdLogPath)
		c.rollback()
		return fmt.Errorf("hostapd CRASH saat inisialisasi driver/radio:\n%s", string(logData))
	}

	// 8. Assign IP Gateway ke ap0
	_ = exec.Command("ip", "addr", "add", c.cfg.GatewayIP+"/24", "dev", c.cfg.APInterface).Run()
	_ = exec.Command("ip", "link", "set", c.cfg.APInterface, "up").Run()

	// 9. Jalankan dnsmasq
	c.dnsmasqCmd = exec.Command("dnsmasq",
		"--no-hosts",
		"--keep-in-foreground",
		"--listen-address="+c.cfg.GatewayIP,
		"--dhcp-range=10.42.0.10,10.42.0.50,255.255.255.0,12h",
		"--dhcp-option=3,"+c.cfg.GatewayIP,
		"--dhcp-option=6,"+c.cfg.GatewayIP,
		"--dhcp-leasefile="+dnsmasqLeasePath,
		"-i", c.cfg.APInterface,
		"--bind-interfaces",
		"-p", "0",
	)
	if err := c.dnsmasqCmd.Start(); err != nil {
		c.rollback()
		return fmt.Errorf("gagal menjalankan dnsmasq: %w", err)
	}
	c.addCleanup(func() {
		if c.dnsmasqCmd != nil && c.dnsmasqCmd.Process != nil {
			_ = c.dnsmasqCmd.Process.Kill()
			_ = c.dnsmasqCmd.Wait()
		}
		_ = os.Remove(dnsmasqLeasePath)
	})

	// 10. Firewall Routing & Stealth NAT
	_ = exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
	_ = exec.Command("sysctl", "-w", "net.ipv4.conf.all.route_localnet=1").Run()
	c.addCleanup(func() {
		_ = exec.Command("sysctl", "-w", "net.ipv4.conf.all.route_localnet=0").Run()
	})

	// Redirection DNS: Port 53 -> 127.0.0.1:53
	_ = exec.Command("iptables", "-t", "nat", "-I", "PREROUTING", "1", "-i", c.cfg.APInterface, "-p", "udp", "--dport", "53", "-j", "DNAT", "--to-destination", "127.0.0.1:53").Run()
	c.addCleanup(func() {
		_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-i", c.cfg.APInterface, "-p", "udp", "--dport", "53", "-j", "DNAT", "--to-destination", "127.0.0.1:53").Run()
	})

	_ = exec.Command("iptables", "-t", "nat", "-I", "PREROUTING", "1", "-i", c.cfg.APInterface, "-p", "tcp", "--dport", "53", "-j", "DNAT", "--to-destination", "127.0.0.1:53").Run()
	c.addCleanup(func() {
		_ = exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-i", c.cfg.APInterface, "-p", "tcp", "--dport", "53", "-j", "DNAT", "--to-destination", "127.0.0.1:53").Run()
	})

	// Masquerade NAT & Forward
	_ = exec.Command("iptables", "-t", "nat", "-I", "POSTROUTING", "1", "-s", c.cfg.SubnetCIDR, "-o", c.cfg.ParentIface, "-j", "MASQUERADE").Run()
	c.addCleanup(func() {
		_ = exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", c.cfg.SubnetCIDR, "-o", c.cfg.ParentIface, "-j", "MASQUERADE").Run()
	})

	_ = exec.Command("iptables", "-I", "FORWARD", "1", "-i", c.cfg.APInterface, "-o", c.cfg.ParentIface, "-j", "ACCEPT").Run()
	c.addCleanup(func() {
		_ = exec.Command("iptables", "-D", "FORWARD", "-i", c.cfg.APInterface, "-o", c.cfg.ParentIface, "-j", "ACCEPT").Run()
	})

	_ = exec.Command("iptables", "-I", "FORWARD", "1", "-i", c.cfg.ParentIface, "-o", c.cfg.APInterface, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").Run()
	c.addCleanup(func() {
		_ = exec.Command("iptables", "-D", "FORWARD", "-i", c.cfg.ParentIface, "-o", c.cfg.APInterface, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").Run()
	})

	c.running = true
	log.Printf("SUCCESS: Wi-Fi Hotspot [%s] AKTIF di [%s]! (Password: %s)", c.cfg.SSID, c.cfg.APInterface, c.cfg.Password)
	log.Printf("SUCCESS: DHCP Server [10.42.0.10 - 10.42.0.50] siap melayani HP/Client")
	log.Printf("SUCCESS: Stealth NAT Active -> Semua DNS client disedot ke DoH 127.0.0.1:53")
	return nil
}

// GetConnectedClients membaca daftar client dari lease DHCP dan radio Wi-Fi menggunakan pure parsers.
func (c *Controller) GetConnectedClients() ([]ConnectedClient, error) {
	leases := make(map[string]ConnectedClient)
	if data, err := os.ReadFile(dnsmasqLeasePath); err == nil {
		leases = ParseDHCPLeases(string(data))
	}

	stats := make(map[string]StationStats)
	if out, err := exec.Command("iw", "dev", c.cfg.APInterface, "station", "dump").Output(); err == nil {
		stats = ParseStationDump(string(out))
	}

	return MergeClientStats(leases, stats), nil
}

// Close mematikan hostapd, dnsmasq, dan membersihkan seluruh aturan iptables secara LIFO.
func (c *Controller) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}
	c.running = false

	log.Println("HOTSPOT: Mematikan Wi-Fi Hotspot dan membersihkan NAT secara LIFO...")
	c.rollback()
	log.Println("SUCCESS: Hotspot & NAT berhasil dibersihkan dengan aman!")
	return nil
}

func (c *Controller) getWifiChannel(iface string) (int, string) {
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

func (c *Controller) getVirtualMAC(iface string) string {
	netIface, err := net.InterfaceByName(iface)
	if err != nil || len(netIface.HardwareAddr) != 6 {
		return ""
	}
	mac := make([]byte, 6)
	copy(mac, netIface.HardwareAddr)
	mac[0] ^= 0x02
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
}
