#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#define ETH_P_IP 0x0800 // Protokol IPv4 di Ethernet Header
#define IPPROTO_ICMP 1 // Protokol ICMP (Ping) di IP Header

// BPF Map untuk menghitung berapa banyak paket PING yang di DROP
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} drop_count SEC(".maps");

SEC("xdp")
int xdp_router_func(struct xdp_md *ctx) {
  void *data = (void *)(long)ctx->data;
  void *data_end = (void *)(long)ctx->data_end;

  // 1. Bedah Header Ethernet (Layer 2)
  struct ethhdr *eth = data;
  // Boundary Guard: Pastikan ukuran paket cukup menampung Ethernet Header
  if ((void *)eth + sizeof(struct ethhdr) > data_end)
    return XDP_PASS;

  // Cek apakah paket ini membawa protokol IPv4
  // Catatan: Jaringan memakai Big-Endian, CPU memakai Little-Endian, maka kita pakai bpf_htons() untuk menyamakan format byte
  if (eth->h_proto != bpf_htons(ETH_P_IP))
    return XDP_PASS;

  // 2. Bedah Header IPv4 (Layer 3)
  struct iphdr *ip = (void *)eth + sizeof(struct ethhdr);
  // Boundary Guard: Pastikan ukuran paket cukupe menampung IP header
  if ((void *)ip + sizeof(struct iphdr) > data_end)
    return XDP_PASS;

  // 3. Eksekusi Firewall: Jika ini paket Ping (ICMP), DROP!
  if (ip->protocol == IPPROTO_ICMP) {
    __u32 key = 0;
    __u64 *val = bpf_map_lookup_elem(&drop_count, &key);
    if (val) {
      __sync_fetch_and_add(val, 1);
    }

    // BANTING DAN BUANG PAKET INI DI KERNEL!
    return XDP_DROP;
  }

  // Untuk traffic lain, izinkan lewat normal
  return XDP_PASS;
}

char __license[] SEC("license") = "Dual MIT/GPL";
