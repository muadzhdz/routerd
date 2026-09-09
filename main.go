package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cilium/ebpf/link"

)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target bpf bpf bpf/xdp_prog.c

func main()  {
	// 1. Load eBPF objects (program + map) ke kernel
	objs := bpfObjects{}
	if err := loadBpfObjects(&objs, nil); err != nil {
		log.Fatalf("Gagal me-load program eBPF ke kernel: %v", err)
	}
	defer objs.Close()

	// 2. Tentukan interface target
	ifaceName := "lo"
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		log.Fatalf("Interface %s tidak ditemukan: %v", ifaceName, err)
	}

	// 2. Attach program XDP ke interface
	l, err := link.AttachXDP(link.XDPOptions{
		Program:		objs.XdpRouterFunc,
		Interface: 	iface.Index,
	})
	if err != nil {
		log.Fatalf("Gagal menempelkan XDP ke interface %s: %v", ifaceName, err)
	}
	defer l.Close() 

	log.Printf("SUCCESS: Program eBPF aktif di interface [%s]!", ifaceName)
	log.Println("Monitoring traffic... Kirim paket (misal ping localhost) untuk melihat counter!")
	log.Println("Tekan [Ctrl + C] untuk menghentikan program...")

	// 4. Goroutine untuk membaca BPF Map setia 1 detik
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	stopChan := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				var count uint64
				key := uint32(0)

				// 4. Membaca data dari BPF Array Map di Ring 0
				if err := objs.TcpDropCount.Lookup(key, &count); err != nil {
					log.Printf("Error membaca map: %v", err)
					continue
				}
				log.Printf("[TCP Firewall] Percobaan Akses Port 8080 di-DROP: %d", count)

			case <-stopChan:
				return
			}
		}
	}()

	// 5. Tunggu snyal Ctrl+C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	close(stopChan)
	log.Println("\nMemebersihkan XDP dari kernel dan keluar dengan aman...")
}


