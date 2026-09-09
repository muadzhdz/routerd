package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target bpf bpf bpf/xdp_prog.c

func main()  {
	// 1. Load eBPF objects (program-program + maps) ke kernel
	objs := bpfObjects{}
	if err := loadBpfObjects(&objs, nil); err != nil {
		log.Fatalf("Gagal me-load eBPF objects ke kernel: %v", err)
	}
	defer objs.Close()

	// 2. Tentukan interface target
	ifaceName := "lo"
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		log.Fatalf("Interface %s tidak ditemukan: %v", ifaceName, err)
	}

	// 3. Pasang INGRESS HOOK (XDP - Pintu Masuk)
	lXdp, err := link.AttachXDP(link.XDPOptions{
		Program:		objs.XdpRouterFunc,
		Interface: 	iface.Index,
	})
	if err != nil {
		log.Fatalf("Gagal Attach XDP ke %s: %v", ifaceName, err)
	}
	defer lXdp.Close() 

	// 4. Pasang EGRESS HOOK (TCX - Pintu Masuk / DPI Monitor)
	lTc, err := link.AttachTCX(link.TCXOptions{
		Program: objs.TcEgressFunc,
		Attach: ebpf.AttachTCXEgress,
		Interface: iface.Index,
	})
	if err != nil {
		log.Fatalf("Gagal Attach TCX Egress ke %s: %v", ifaceName, err)
	}
	defer lTc.Close()

	log.Printf("SUCCESS: Ingress (XDP) & Egress (TCX) AKTIF di interface [%s]!", ifaceName)
	log.Println("Monitoring dua arah berjalan... Tekan [Ctrl + C] untuk keluar.")

	// 5. Goroutine Monitoring Dua Arah
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	stopChan := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				var dropCount, httpsCount uint64
				key := uint32(0)

				// Baca data Ingress (Port 8080 Blocked)
				_ = objs.TcpDropCount.Lookup(key, &dropCount)

				// baca data Egress (HTTPS Port 443 Keluar)
				_ = objs.EgressHttpsCount.Lookup(key, &httpsCount)

				log.Printf("[HUD Jaringan] INGRESS DROP (Port 8080): %d | EGRESS HTTPS (Port 443): %d", dropCount, httpsCount)

			case <-stopChan:
				return
			}
		}
	}()

	// 6. Tunggu sinyal Ctrl+C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	close(stopChan)
	log.Println("\nMembersihkan XDP & TCX dari kernel. Keluar dengan aman!")
}
