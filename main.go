package main

import (
	"flag"
	"log"
	"net"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muadzhdz/routerd/pkg/dashboard"
	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/engine"
	"github.com/muadzhdz/routerd/pkg/hotspot"
)

func main() {
	ifaceFlag := flag.String("iface", "", "Interface jaringan target (kosongkan untuk auto-detect)")
	hotspotFlag := flag.Bool("hotspot", false, "Aktifkan Wi-Fi Hotspot & Stealth NAT Router")
	ssidFlag := flag.String("ssid", "routerd", "Nama SSID Wi-Fi Hotspot")
	passFlag := flag.String("password", "routerd123", "Password Wi-Fi Hotspot (min 8 karakter)")
	flag.Parse()

	// 1. Inisialisasi & Start eBPF Packet Engine (Deep Module)
	eng, err := engine.Start(engine.Config{
		InterfaceName: *ifaceFlag,
		XDPMode:       engine.XDPModeAuto,
	})
	if err != nil {
		log.Fatalf("Gagal menjalankan eBPF Engine: %v", err)
	}
	defer eng.Close()

	iface := eng.Interface()
	log.Printf("SUCCESS: eBPF Engine AKTIF di interface [%s]!", iface.Name)

	// 2. Jalankan Stealth DoH DNS Resolver Engine dengan In-Memory Cache & Fallback
	dnsServer, err := dns.NewServer(dns.ServerConfig{
		ListenAddr: "127.0.0.1:53",
	})
	if err != nil {
		log.Fatalf("Gagal inisialisasi DNS Resolver: %v", err)
	}
	if err := dnsServer.Start(); err != nil {
		log.Printf("Peringatan: Gagal start DNS Server: %v", err)
	}
	defer dnsServer.Close()

	// Alihkan DNS interface target ke 127.0.0.1 via systemd-resolved
	_ = exec.Command("resolvectl", "dns", iface.Name, "127.0.0.1").Run()
	_ = exec.Command("resolvectl", "flush-caches").Run()
	log.Printf("SUCCESS: System DNS [%s] dialihkan ke 127.0.0.1 (DoH Cloudflare)!", iface.Name)

	// 3. Jika flag -hotspot aktif: Nyalakan Wi-Fi Hotspot & Stealth NAT
	if *hotspotFlag {
		if err := hotspot.StartHotspot(iface.Name, *ssidFlag, *passFlag); err != nil {
			log.Printf("Peringatan Hotspot: %v", err)
		}
	}

	// 4. Deteksi WAN IP
	var wanIP string
	if addrs, err := iface.Addrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				wanIP = ipNet.IP.String()
				break
			}
		}
	}

	// 5. Jalankan TUI Dashboard
	dashCfg := dashboard.Config{
		WANIface:      iface.Name,
		WANIP:         wanIP,
		HotspotActive: *hotspotFlag,
		SSID:          *ssidFlag,
		StatsProvider: eng,
		DNSEventChan:  dnsServer.Events(),
	}

	p := tea.NewProgram(dashboard.NewModel(dashCfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Printf("Error menjalankan dashboard: %v", err)
	}

	// 6. Cleanup saat keluar dari dashboard ([q] / Ctrl+C)
	if *hotspotFlag {
		hotspot.StopHotspot()
	}

	// Kembalikan DNS ke default DHCP
	_ = exec.Command("resolvectl", "revert", iface.Name).Run()
	_ = exec.Command("resolvectl", "flush-caches").Run()

	log.Println("\nMembersihkan Engine & Mengembalikan DNS. Keluar dengan aman!")
}
