package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/muadzhdz/routerd/pkg/dns"
	"github.com/muadzhdz/routerd/pkg/netutil"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target bpf bpf bpf/xdp_prog.c

func main()  {
	ifaceFlag := flag.String("iface", "", "Interface jaringan target (kosongkan untuk auto-detect)")
	flag.Parse()

	// 1. Load eBPF objects ke kernel
	objs := bpfObjects{}
	if err := loadBpfObjects(&objs, nil); err != nil {
		log.Fatalf("Gagal me-load eBPF objects: %v", err)
	}
	defer objs.Close()

	// 2. Cari interface target
	var iface *net.Interface
  var err error

  if *ifaceFlag == "" {
      iface, err = netutil.GetDefaultInterface()
      if err != nil {
          log.Fatalf("Gagal auto-detect interface: %v", err)
      }
      log.Printf("AUTO-DETECT: Interface aktif terdeteksi -> [%s]", iface.Name)
  } else {
      iface, err = net.InterfaceByName(*ifaceFlag)
      if err != nil {
          log.Fatalf("Interface %s tidak ditemukan: %v", *ifaceFlag, err)
      }
  }

	// 3. Pasang INGRESS HOOK (Hanya pasang XDP jika di 'lo' agar kartu Wi-Fi tidak kaget/link flap)
	if iface.Name == "lo" {
		lXdp, err := link.AttachXDP(link.XDPOptions{
			Program:		objs.XdpRouterFunc,
			Interface: 	iface.Index,
		})
		if err != nil {
			log.Printf("Peringatan: Gagal attach XDP ke %s: %v", iface.Name, err)
		} else {
			defer lXdp.Close()
			log.Printf("Ingress XDP aktif di [%s]", iface.Name)
		}
	} else {
		log.Println("Mode Wi-Fi fisik: Melewati XDP agar kartu jaringan tetap 100% stabil")
	}
	// 4. Pasang EGRESS HOOK (TCX Egress - DPI Hunter)
	lTc, err := link.AttachTCX(link.TCXOptions{
		Program: objs.TcEgressFunc,
		Attach: ebpf.AttachTCXEgress,
		Interface: iface.Index,
	})

	if err != nil {
		log.Fatalf("Gagal Attach TCX Egress ke %s: %v", iface.Name, err)
	}
	defer lTc.Close()

	log.Printf("SUCCESS: Engine TCX eBPF AKTIF di interface [%s]!", iface.Name)
	log.Println("Memburu paket TLS ClientHello... Tekan [Ctrl + C] untuk keluar.")

	// 4b. Pasang INGRESS HOOK (TCX Ingress - The TCP Window Clamper)
  lTcIngress, err := link.AttachTCX(link.TCXOptions{
    Program:   objs.TcIngressFunc,
    Attach:    ebpf.AttachTCXIngress,
    Interface: iface.Index,
  })

  if err != nil {
  	log.Fatalf("Gagal Attach TCX Ingress ke %s: %v", iface.Name, err)
  }

  defer lTcIngress.Close()
  log.Printf("SUCCESS: Engine TCX Ingress & Egress AKTIF di interface [%s]!", iface.Name)	
	// 4c. Jalankan Stealth DoH DNS Proxy di background
  stopChan := make(chan struct{})
  go func() {
      if err := dns.StartDoHServer("127.0.0.1:53", stopChan); err != nil {
          log.Printf("Peringatan: DoH Server error: %v", err)
      }
  }()

	// Alihkan DNS wlp2s0 ke 127.0.0.1 via systemd-resolved
  _ = exec.Command("resolvectl", "dns", iface.Name, "127.0.0.1").Run()
  _ = exec.Command("resolvectl", "flush-caches").Run()
  log.Printf("SUCCESS: System DNS [%s] dialihkan ke 127.0.0.1 (DoH Cloudflare)!", iface.Name)
	// 5. Goroutine Monitoring
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ticker.C:
				var helloCount uint64
				var clampCount uint64
				key := uint32(0)

				_ = objs.ClientHelloCount.Lookup(key, &helloCount)
				_ = objs.SynackClampCount.Lookup(key, &clampCount)

				log.Printf("[HUD Jaringan] INGRESS SYN/ACK CLAMP: %d | EGRESS TLS CLIENTHELLO: %d", clampCount, helloCount)

			case <-stopChan:
				return
			}
		}
	}()

	// 6. Tunggu Ctrl+C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	close(stopChan)

	// Kembalikan DNS ke default DHCP
	_ = exec.Command("resolvectl", "revert", iface.Name).Run()
	_ = exec.Command("resolvectl", "flush-caches").Run()

	log.Println("\nMembersihkan Engine & Mengembalikan DNS. Keluar dengan aman!")
}
