# WireGuard VPN Uplink and Policy Routing

We implement an optional, robust WireGuard VPN uplink subsystem (`pkg/vpn`) with asymmetric policy routing. When enabled, all wireless client traffic routed through `ap0` is forwarded and masqueraded across an encrypted WireGuard tunnel (`wg0` / `vpn`), shielding client IP addresses and ISP inspection upstream.

Key architectural decisions:
1. **DNS Sanitization (`PrepareRuntimeConfig`)**: WireGuard profiles frequently include `DNS = ...` lines that cause `wg-quick` to invoke `resolvconf`. On modern Linux distributions running `systemd-resolved`, this causes signature conflicts or overrides routerd's local DoH sinkhole. The controller safely sanitizes these directives at runtime, preserving routerd's native DNS ad-blocking and DoH encryption.
2. **Asymmetric Routing & rp_filter**: Forwarding traffic from `ap0` through a separate WireGuard interface causes kernel reverse path drops under strict filtering. The controller sets `rp_filter=0` on `ap0` and `all` interfaces during VPN operation, restoring previous values upon exit.
3. **Policy Routing & TCP MSS Clamping**: Traffic from AP clients is directed to WireGuard routing table `51820` (`ip rule add iif ap0 table 51820`). To prevent packet drops and TCP stalling caused by the WireGuard MTU overhead (1420 vs 1500 bytes), the controller automatically applies iptables TCPMSS clamping (`--clamp-mss-to-pmtu`) on the VPN interface.
4. **Resilient Fallback & LIFO Rollback**: If VPN credentials are unconfigured or the handshake fails, routerd falls back gracefully to the direct WAN uplink without service interruption. During shutdown, VPN interfaces, policy rules, and iptables entries are safely dismantled in LIFO order prior to hotspot and engine teardown.
