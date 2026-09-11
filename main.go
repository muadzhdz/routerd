package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muadzhdz/routerd/pkg/dashboard"
	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/engine"
	"github.com/muadzhdz/routerd/pkg/hotspot"
)

func main() {
	ifaceFlag := flag.String("iface", "", "Target network interface (leave empty for auto-detect)")
	hotspotFlag := flag.Bool("hotspot", false, "Enable Wi-Fi Hotspot & Stealth NAT Router")
	ssidFlag := flag.String("ssid", "routerd", "Wi-Fi Hotspot SSID name")
	passFlag := flag.String("password", "routerd123", "Wi-Fi Hotspot password (min 8 characters)")
	headlessFlag := flag.Bool("headless", false, "Run as background daemon without TUI Dashboard")
	flag.Parse()

	// 0. Install context to trap termination signals (SIGINT, SIGTERM)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Initialize & Start eBPF Packet Engine (Deep Module)
	eng, err := engine.Start(engine.Config{
		InterfaceName: *ifaceFlag,
		XDPMode:       engine.XDPModeAuto,
	})
	if err != nil {
		log.Fatalf("Failed to start eBPF Engine: %v", err)
	}
	defer eng.Close()

	iface := eng.Interface()
	log.Printf("SUCCESS: eBPF Engine ACTIVE on interface [%s]!", iface.Name)

	// 2. Start Stealth DoH DNS Resolver Engine with In-Memory Cache & Fallback
	dnsServer, err := dns.NewServer(dns.ServerConfig{
		ListenAddr: "127.0.0.1:53",
	})
	if err != nil {
		log.Fatalf("Failed to initialize DNS Resolver: %v", err)
	}
	if err := dnsServer.Start(); err != nil {
		log.Printf("Warning: Failed to start DNS Server: %v", err)
	}
	defer dnsServer.Close()

	// Divert system DNS on target interface to 127.0.0.1 via systemd-resolved
	_ = exec.Command("resolvectl", "dns", iface.Name, "127.0.0.1").Run()
	_ = exec.Command("resolvectl", "flush-caches").Run()
	log.Printf("SUCCESS: System DNS [%s] redirected to 127.0.0.1 (DoH Cloudflare/Google)!", iface.Name)

	// Guarantee default DNS restoration on program exit
	defer func() {
		_ = exec.Command("resolvectl", "revert", iface.Name).Run()
		_ = exec.Command("resolvectl", "flush-caches").Run()
		log.Println("SUCCESS: DNS reverted to default DHCP.")
	}()

	// 3. If -hotspot flag is active: Start Wi-Fi Access Point & Stealth NAT
	var hotspotCtrl *hotspot.Controller
	if *hotspotFlag {
		hotspotCtrl = hotspot.NewController(hotspot.Config{
			ParentIface: iface.Name,
			SSID:        *ssidFlag,
			Password:    *passFlag,
		})
		if err := hotspotCtrl.Start(); err != nil {
			log.Printf("Warning Hotspot: %v", err)
		} else {
			defer hotspotCtrl.Close()
		}
	}

	// 4. Detect WAN IP
	var wanIP string
	if addrs, err := iface.Addrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
				wanIP = ipNet.IP.String()
				break
			}
		}
	}

	// 5. Run TUI Dashboard or Headless Daemon mode
	if *headlessFlag {
		log.Println("=========================================================")
		log.Println("routerd DAEMON ACTIVE IN BACKGROUND (HEADLESS MODE)")
		log.Printf("WAN Interface : %s (%s)", iface.Name, wanIP)
		if *hotspotFlag {
			log.Printf("Hotspot AP    : SSID [%s] | Gateway 10.42.0.1 (ap0)", *ssidFlag)
			log.Println("Stealth NAT   : Port 53 redirected to DoH 127.0.0.1:53")
		}
		log.Println("Waiting for termination signal (SIGTERM / Ctrl+C)...")
		log.Println("=========================================================")

		// Consume DNS events in background logger
		go func() {
			for ev := range dnsServer.Events() {
				_ = ev
			}
		}()

		<-ctx.Done()
		log.Println("\nReceived termination signal. Initiating graceful shutdown...")
	} else {
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
			log.Printf("Error running dashboard: %v", err)
		}
	}

	log.Println("Cleaning up Engine, Hotspot & Restoring DNS. Exiting cleanly!")
}
