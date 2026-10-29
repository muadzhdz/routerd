package bpf

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target bpf -cflags "-I../../../bpf -I/usr/include" Bpf ../../../bpf/xdp_prog.c
