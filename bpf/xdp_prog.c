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

// BPF MAP 2: Egress TLS ClientHello Hunter!
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} client_hello_count SEC(".maps");

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

  // Periksa apakah menuju Port 443 (HTTPS)
  if (bpf_ntohs(tcp->dest) == 443) {
    // 1. Tarik 128 byte pertama ke linear buffer supaya payload TLS masuk ke skb->data
    if (bpf_skb_pull_data(skb, 128) < 0)
      return TC_ACT_OK;

    // 2. WAJIB RE-EVALUASI pointer karena memori bisa berpindah setelah pull!
    data = (void *)(long)skb->data;
    data_end = (void *)(long)skb->data_end;
    
    // Re-validate boundary IP & TCP setelah refresh pointer 
    eth = data;
    if ((void *)eth + sizeof(struct ethhdr) > data_end)
      return TC_ACT_OK;

    ip = (void *)eth + sizeof(struct ethhdr);
    if ((void *)ip + sizeof(struct iphdr) > data_end)
      return TC_ACT_OK;

    tcp = (void *)ip + (ip->ihl * 4);
    if ((void *)tcp + sizeof(struct tcphdr) > data_end)
      return TC_ACT_OK;

    // 3. Sekarang hitung payload TCP
    void *payload = (void *)tcp + (tcp->doff * 4);

    if ((void *)payload + 6 <= data_end) {
      __u8 *bytes = payload;

      if (bytes[0] == 0x16 && bytes[5] == 0x01) {
        __u32 key = 0;
        __u64 *val = bpf_map_lookup_elem(&client_hello_count, &key);
        if (val) {
          __sync_fetch_and_add(val, 1);
        }
      }
    }
  }
    return TC_ACT_OK;
}

char __license[] SEC("license") = "Dual MIT/GPL";
