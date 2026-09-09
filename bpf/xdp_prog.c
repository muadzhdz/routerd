#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#define TC_ACT_OK 0
#define TC_ACT_SHOT 2
#define ETH_P_IP 0x0800
#define IPPROTO_TCP 6

// BPF MAP 1: Ingress DROP (Port 8080)
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} tcp_drop_count SEC(".maps");

// BPF MAP 2: Egress SCRAMBLED (Port 443 Window Modified)
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} scramble_count SEC(".maps");

// Helper matematika RFC 1624: Update Checksum instan
static inline void update_csum16(__u16 *csum, __u16 old_val, __u16 new_val) {
  __u32 sum = (~(*csum) & 0xffff) + (~old_val & 0xffff) + new_val;
  sum = (sum >> 16) + (sum & 0xffff);
  sum += (sum >> 16);
  *csum = ~sum;
}

//======================================
// 1. INGRESS HOOK (XDP - Pintu Masuk)
//======================================
SEC("xdp")
int xdp_router_func(struct xdp_md *ctx) {
  void *data = (void *)(long)ctx->data;
  void *data_end = (void *)(long)ctx->data_end;

  struct ethhdr *eth = data;
  if ((void *)eth + sizeof(struct ethhdr) > data_end)
    return XDP_PASS;

  if (eth->h_proto != bpf_htons(ETH_P_IP))
    return XDP_PASS;

  struct iphdr *ip = (void *)eth + sizeof(struct ethhdr);
  if ((void *)ip + sizeof(struct iphdr) > data_end)
    return XDP_PASS;

  if (ip->protocol != IPPROTO_TCP || ip->ihl < 5)
    return XDP_PASS;

  struct tcphdr *tcp = (void *)ip + (ip->ihl * 4);
  if ((void *)tcp + sizeof(struct tcphdr) > data_end)
    return XDP_PASS;

  if (bpf_ntohs(tcp->dest) == 8080) {
    __u32 key = 0;
    __u64 *val = bpf_map_lookup_elem(&tcp_drop_count, &key);
    if (val) {
      __sync_fetch_and_add(val, 1);
    }
    return XDP_DROP;
  }

  return XDP_PASS;
}

//=================================================
// 2. EGRESS HOOK (TC - Pintu Keluar / DPI Guard)
//=================================================
SEC("tc")
int tc_egress_func(struct __sk_buff *skb) {
  void *data = (void *)(long)skb->data;
  void *data_end = (void *)(long)skb->data_end;

  struct ethhdr *eth = data;
  if ((void *)eth + sizeof(struct ethhdr) > data_end)
    return TC_ACT_OK;

  if (eth->h_proto != bpf_htons(ETH_P_IP))
    return TC_ACT_OK;

  struct iphdr *ip = (void *)eth + sizeof(struct ethhdr);
  if ((void *)ip + sizeof(struct iphdr) > data_end)
    return TC_ACT_OK;

  if (ip->protocol != IPPROTO_TCP || ip->ihl < 5)
    return TC_ACT_OK;

  struct tcphdr *tcp = (void *)ip + (ip->ihl * 4);
  if ((void *)tcp + sizeof(struct tcphdr) > data_end)
    return TC_ACT_OK;

  // JIKA MENUJU PORT 443 (HTTPS):MODIKASI PAKETNYA!
  if (bpf_ntohs(tcp->dest) == 443) {
    __u16 old_win = tcp->window;
    __u16 new_win = bpf_htons(1460); // Paksa ukuran window jadi 1460 byte

    //Perbaiki checksum terlebih dahulu
    update_csum16(&tcp->check, old_win, new_win);

    // Timpa nilai window lama dengan nilai baru di memori kernel!
    tcp->window = new_win;

    // Catat ke BPF Map bahwa sukses mengacak 1 paket
    __u32 key = 0;
    __u64 *val = bpf_map_lookup_elem(&scramble_count, &key);
    if (val) {
      __sync_fetch_and_add(val, 1);
    }
  }
    return TC_ACT_OK;
}

char __license[] SEC("license") = "Dual MIT/GPL";
