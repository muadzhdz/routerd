#include <linux/types.h>
#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/ipv6.h>
#include <linux/in.h>
#include <linux/tcp.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

#define TC_ACT_OK 0
#define TC_ACT_SHOT 2

#ifndef ETH_P_IP
#define ETH_P_IP 0x0800
#endif
#ifndef ETH_P_IPV6
#define ETH_P_IPV6 0x86DD
#endif

// BPF MAP 1: Ingress DROP Counter
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} tcp_drop_count SEC(".maps");

// BPF MAP 2: Egress TLS ClientHello Counter
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} client_hello_count SEC(".maps");

// BPF MAP 3: Ingress/Egress Clamped MSS Counter
struct {
  __uint(type, BPF_MAP_TYPE_ARRAY);
  __type(key, __u32);
  __type(value, __u64);
  __uint(max_entries, 1);
} synack_clamp_count SEC(".maps");

// BPF MAP 4: Dynamic Blocked Ports Hash Map (key: port, val: action)
struct {
  __uint(type, BPF_MAP_TYPE_HASH);
  __type(key, __u16);
  __type(value, __u8);
  __uint(max_entries, 256);
} blocked_ports SEC(".maps");

// RFC 1624 mathematical helper: Fast incremental 16-bit checksum update
static inline void update_csum16(__u16 *csum, __u16 old_val, __u16 new_val) {
  __u32 sum = (~(*csum) & 0xffff) + (~old_val & 0xffff) + new_val;
  sum = (sum >> 16) + (sum & 0xffff);
  sum += (sum >> 16);
  *csum = ~sum;
}

//==================================================
// 1. INGRESS HOOK (XDP - High Performance Filter)
//==================================================
SEC("xdp")
int xdp_router_func(struct xdp_md *ctx) {
  void *data = (void *)(long)ctx->data;
  void *data_end = (void *)(long)ctx->data_end;

  struct ethhdr *eth = data;
  if ((void *)eth + sizeof(struct ethhdr) > data_end)
    return XDP_PASS;

  // Handle IPv4
  if (eth->h_proto == bpf_htons(ETH_P_IP)) {
    struct iphdr *ip = (void *)eth + sizeof(struct ethhdr);
    if ((void *)ip + sizeof(struct iphdr) > data_end)
      return XDP_PASS;

    if (ip->protocol != IPPROTO_TCP || ip->ihl < 5)
      return XDP_PASS;

    struct tcphdr *tcp = (void *)ip + (ip->ihl * 4);
    if ((void *)tcp + sizeof(struct tcphdr) > data_end)
      return XDP_PASS;

    __u16 dport = bpf_ntohs(tcp->dest);
    __u8 *blocked = bpf_map_lookup_elem(&blocked_ports, &dport);
    if (blocked && *blocked == 1) {
      __u32 key = 0;
      __u64 *val = bpf_map_lookup_elem(&tcp_drop_count, &key);
      if (val) {
        __sync_fetch_and_add(val, 1);
      }
      return XDP_DROP;
    }
  }

  // Handle IPv6
  if (eth->h_proto == bpf_htons(ETH_P_IPV6)) {
    struct ipv6hdr *ip6 = (void *)eth + sizeof(struct ethhdr);
    if ((void *)ip6 + sizeof(struct ipv6hdr) > data_end)
      return XDP_PASS;

    if (ip6->nexthdr == IPPROTO_TCP) {
      struct tcphdr *tcp = (void *)ip6 + sizeof(struct ipv6hdr);
      if ((void *)tcp + sizeof(struct tcphdr) > data_end)
        return XDP_PASS;

      __u16 dport = bpf_ntohs(tcp->dest);
      __u8 *blocked = bpf_map_lookup_elem(&blocked_ports, &dport);
      if (blocked && *blocked == 1) {
        __u32 key = 0;
        __u64 *val = bpf_map_lookup_elem(&tcp_drop_count, &key);
        if (val) {
          __sync_fetch_and_add(val, 1);
        }
        return XDP_DROP;
      }
    }
  }

  return XDP_PASS;
}

//==================================================
// 2. EGRESS HOOK (TC - Exit Point / Telemetry Guard)
//==================================================
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

  // Inspect outbound HTTPS traffic for TLS ClientHello
  if (bpf_ntohs(tcp->dest) == 443) {
    if (bpf_skb_pull_data(skb, 128) < 0)
      return TC_ACT_OK;

    data = (void *)(long)skb->data;
    data_end = (void *)(long)skb->data_end;

    eth = data;
    if ((void *)eth + sizeof(struct ethhdr) > data_end)
      return TC_ACT_OK;

    ip = (void *)eth + sizeof(struct ethhdr);
    if ((void *)ip + sizeof(struct iphdr) > data_end)
      return TC_ACT_OK;

    tcp = (void *)ip + (ip->ihl * 4);
    if ((void *)tcp + sizeof(struct tcphdr) > data_end)
      return TC_ACT_OK;

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

//========================================================
// 3. INGRESS HOOK (TC Ingress - In-Kernel TCP MSS Clamper)
//========================================================
SEC("tc")
int tc_ingress_func(struct __sk_buff *skb) {
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

  // Apply TCP MSS clamping on SYN / SYN-ACK packets to prevent PMTU blackholes
  if (tcp->syn && tcp->doff > 5) {
    if (bpf_skb_pull_data(skb, sizeof(struct ethhdr) + sizeof(struct iphdr) + 60) < 0)
      return TC_ACT_OK;

    data = (void *)(long)skb->data;
    data_end = (void *)(long)skb->data_end;

    eth = data;
    if ((void *)eth + sizeof(struct ethhdr) > data_end)
      return TC_ACT_OK;

    ip = (void *)eth + sizeof(struct ethhdr);
    if ((void *)ip + sizeof(struct iphdr) > data_end)
      return TC_ACT_OK;

    if (ip->protocol != IPPROTO_TCP || ip->ihl < 5)
      return TC_ACT_OK;

    tcp = (void *)ip + (ip->ihl * 4);
    if ((void *)tcp + sizeof(struct tcphdr) > data_end)
      return TC_ACT_OK;

    __u8 *opt = (void *)tcp + sizeof(struct tcphdr);

    #pragma unroll
    for (int i = 0; i < 8; i++) {
      if ((void *)opt + 4 > data_end)
        break;

      __u8 kind = opt[0];
      if (kind == 0) // End of Option List
        break;
      if (kind == 1) { // No-Op
        opt += 1;
        continue;
      }
      if (kind == 2) { // MSS option
        if (opt[1] == 4) {
          __u16 *mss_ptr = (__u16 *)(opt + 2);
          __u16 old_mss = *mss_ptr;
          __u16 target_mss = bpf_htons(1360); // 1360 for WireGuard / PPPoE MTU safety
          if (bpf_ntohs(old_mss) > 1360) {
            update_csum16(&tcp->check, old_mss, target_mss);
            *mss_ptr = target_mss;
            __u32 key = 0;
            __u64 *val = bpf_map_lookup_elem(&synack_clamp_count, &key);
            if (val) {
              __sync_fetch_and_add(val, 1);
            }
          }
        }
        break;
      }

      __u8 len = opt[1];
      if (len < 2 || len > 40)
        break;
      opt += len;
    }
  }

  return TC_ACT_OK;
}

char __license[] SEC("license") = "Dual MIT/GPL";
