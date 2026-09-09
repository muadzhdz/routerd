#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#define ETH_P_IP 0x0800 // Protokol IPv4 di Ethernet Header
#define IPPROTO_TCP 6 // Protokol ICMP (Ping) di IP Header

// BPF Map untuk menghitung berapa banyak paket PING yang di DROP
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} tcp_drop_count SEC(".maps");

SEC("xdp")
int xdp_router_func(struct xdp_md *ctx) {
  void *data = (void *)(long)ctx->data;
  void *data_end = (void *)(long)ctx->data_end;

  // 1. Bedah Header Ethernet (Layer 2)
  struct ethhdr *eth = data;
  if ((void *)eth + sizeof(struct ethhdr) > data_end)
    return XDP_PASS;

  if (eth->h_proto != bpf_htons(ETH_P_IP))
    return XDP_PASS;

  // 2. Bedah Header IPv4 (Layer 3)
  struct iphdr *ip = (void *)eth + sizeof(struct ethhdr);
  if ((void *)ip + sizeof(struct iphdr) > data_end)
    return XDP_PASS;

  // Cek apakah ini paket TCP
  if (ip->protocol != IPPROTO_TCP)
    return XDP_PASS;

  // 3. Bedah TCP Header menggunakan rumus IHL (Layer 4)
  // Pastikan nilai IHl valid (mminimal 5 word = 20 byte)
  if (ip->ihl < 5)
    return XDP_PASS;

  struct tcphdr *tcp = (void *)ip + (ip->ihl * 4);

  // Boundary Guard: pastikan ukuran paket cukup menampung TCP header
  if ((void *)tcp + sizeof(struct tcphdr) > data_end)
    return XDP_PASS;

  // Baca Port Tujuan (konversi dari Network Byte Order Big-endian ke Host
  __u16 dest_port = bpf_htons(tcp->dest);

  // 4. Aturan Firewall: Blokir Port 8080 di kernel!
  if (dest_port == 8080) {
    __u32 key = 0;
    __u64 *val = bpf_map_lookup_elem(&tcp_drop_count, &key);
    if (val) {
      __sync_fetch_and_add(val, 1);
    }

    // BANTING KONEKSI WEB PORT 80801
    return XDP_DROP;
  }

  return XDP_PASS;
}

char __license[] SEC("license") = "Dual MIT/GPL";
