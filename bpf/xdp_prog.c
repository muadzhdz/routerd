#include "vmlinux.h"

// Makro untuk menandai seksi ELF biner
#define SEC(name) __attribute__((section(name), used))

SEC("xdp")
int xdp_router_func(struct xdp_md *ctx) {
  // Untuk tahap awal, kita izinkan semua paket lewat (PASS)
  return XDP_PASS;
}

// Lisensi wajib untuk kernel verifier
char __licence[] SEC("licence") = "Dual MIT/GPL";
