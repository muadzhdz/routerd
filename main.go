package main

import (
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/cilium/ebpf/link"

)

// Directive bpf2go untuk compile otomatis
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target bpf bpf bpf/xdp_prog.c

func main()  {
	// 1. Siapkan wadah untuk menampung program eBPF
	objs := bpfObjects{}
	if err := loadBpfObjects(&objs, nil); err != nil {
		log.Fatalf("Gagal me-load program eBPF ke kernel: %v", err)
	}
	defer objs.Close() // Pastikan resource kernel dibersihkan saat program selesai

	// 2. Tentukan interface target (lo)
	ifaceName := "lo"
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		log.Fatalf("Interface %s tidak ditemukan: %v", ifaceName, err)
	}

	// 3.Pasang (Attach) program XDP ke Kartu Jringan
	l, err := link.AttachXDP(link.XDPOptions{
		Program:		objs.XdpRouterFunc,
		Interface: 	iface.Index,
	})
	if err != nil {
		log.Fatalf("Gagal menempelkan XDP ke interfacce %s: %v", ifaceName, err)
	}
	defer l.Close() // Lepaskan XDP saat aplikasi berhenti

	log.Printf("SUCCESS: Program eBPF aktif di interface [%s]!", ifaceName)
	log.Println("Tekan [Ctrl + C] untuk menghentikan program...")

	// 4. Tunggu sinyal interupsi (Ctrl+C) agar program tidak langsung keluar
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("\nMemebersihkan XDP dari kernel dan keluar dengan aman. Sampai jumpa!")
}


