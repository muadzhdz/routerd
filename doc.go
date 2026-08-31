// Package main implements the routerd daemon — a single-binary tool that turns
// any Linux machine with a Wi-Fi card into a stealth Wi-Fi access point,
// router, and transparent WireGuard VPN gateway.
//
// # Architecture
//
// routerd orchestrates five system-level components in a single process:
//
//   - hostapd — creates a virtual AP interface (ap0) on the same Wi-Fi radio
//     as the client uplink using mac80211's STA+AP concurrent mode.
//   - dnsmasq — DHCP server and caching DNS resolver for AP clients.
//   - wg-quick — manages the WireGuard VPN tunnel used for transparent routing.
//   - iptables/ip6tables — NAT masquerade, host isolation, kill-switch, DNS
//     forced-tunnel, TTL spoofing, MSS clamping, and IPv6 leak protection.
//   - Linux routing (ip rule/ip route) — policy routing to steer AP client
//     traffic through the WireGuard table (51820) to defeat rp_filter drops.
//
// # File layout
//
//   - main.go     — CLI entry point, cmdStart/Stop/Reload/Status/Logs.
//   - config.go   — Config struct, DefaultConfig, LoadConfig.
//   - runner.go   — CommandRunner interface and OSRunner production impl.
//   - util.go     — runCmd wrapper, IP math, MAC generation, channel detection.
//   - ap.go       — Virtual AP interface lifecycle (iw, ip link).
//   - nat.go      — iptables rule installation and teardown (enableNAT / disableNAT).
//   - services.go — hostapd/dnsmasq config generation, spawn, watchdog, ProcessManager.
//   - vpn.go      — WireGuard / WARP VPN bringup, DPI bypass, wgcf integration.
//   - state.go    — PID lock, runtime state file, connected-clients JSON writer.
//   - clients.go  — Station enumeration, lease parsing, uptime formatting.
//   - dashboard/  — Embedded dark-mode web dashboard (HTTP + WebSocket).
package main
