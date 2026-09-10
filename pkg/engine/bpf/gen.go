package bpf

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target bpf -cflags "-I../../../bpf" Bpf ../../../bpf/xdp_prog.c
