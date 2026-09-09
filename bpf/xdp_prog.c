#include "vmlinux.h"
#include <bpf/bpf_helpers.h>

// Definisi BPF Array Map untuk menghitung paket
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} pkt_count SEC(".maps");

SEC("xdp")
int xdp_router_func(struct xdp_md *ctx) {
  __u32 key = 0;

  // Ambil pointer ke slot indeks 0 BPF Map
  __u64 *val = bpf_map_lookup_elem(&pkt_count, &key);

  // Verifier Guard: Pastikan pointer valid (tidak NULL)
  if (val) {
    // Tambah counter secara atomik (aman untuk multi-core CPU)
    __sync_fetch_and_add(val, 1);
  }

  // Izinkan paket tetap lewat
  return XDP_PASS;
}

char __licence[] SEC("licence") = "Dual MIT/GPL";
