# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- **PID lock file** (`/run/routerd/routerd.pid`) — prevents multiple concurrent instances.
- **ProcessManager** struct with mutex — eliminates race condition on the child process list.
- **Child process watchdog** — goroutine monitors hostapd and dnsmasq; triggers graceful shutdown if either crashes unexpectedly.
- **`VPN_DNS` config key** — replaces the hardcoded `1.1.1.1` for forced DNS-over-VPN. Configurable per VPN provider (e.g. Mullvad `10.64.0.1`).
- **`WPA3` config key** — enables WPA3-SAE/WPA2-PSK transition mode in hostapd.
- **`routerd logs` command** — tails hostapd and dnsmasq log files simultaneously.
- **Enhanced `status` command** — shows uptime, VPN endpoint (from `wg show`), per-client MAC + IP from dnsmasq lease file.
- **DPI bypass mode** — `VPN_MODE=dpibypass` now enables TCP MSS clamping and TTL normalization without a VPN tunnel.
- **`warp-setup` auto-generation** — automatically runs `wgcf register && wgcf generate` when `wgcf` is installed.
- **Full `reload`** — `cmdReload` now re-applies all NAT/VPN/isolation rules, not just restarts hostapd/dnsmasq.
- **Unit tests** — `util_test.go`, `config_test.go`, `services_test.go`, `vpn_test.go`.
- **GitHub Actions CI** — `go vet`, `go test -race`, `staticcheck`, and binary build on every push/PR.
- **`sanitizeSSID()`** — strips newlines, null bytes, and `=` from SSID to prevent hostapd config injection.
- **`rp_filter` restore** — saves original `rp_filter` values on VPN start and restores them on cleanup.
- **`isAlreadyExists()`** helper — standardizes iptables error handling across all rule setup functions.
- **`nat.go` refactored** — `enableNAT` split into `setupTorRules`, `setupVPNDNSRules`, `setupMasquerade`, `setupForwardRules`, `setupHostIsolation`, `setupMangleRules`.
- **Startup timestamp** persisted in state file — enables uptime display in `status`.
- **`dnsmasq` readiness polling** — replaced `time.Sleep(500ms)` with log polling (like hostapd).

### Fixed
- `go.mod` was declaring `go 1.26` (a non-existent version); changed to `go 1.22`.
- `cmdReload` previously only restarted hostapd/dnsmasq but skipped NAT and VPN changes.
- Forced DNS in iptables was hardcoded to `1.1.1.1` regardless of `VPN_DNS` config.
- `rp_filter` was set to `0` on VPN start but never restored on stop, weakening host security.

---

## [0.1.0] — 2025-08-28

### Added
- Initial release.
- Hostapd + dnsmasq + WireGuard unified daemon.
- Random MAC, random subnet, TTL spoofing, IPv6 leak protection.
- VPN kill-switch, TCP MSS clamping, Tor transparent proxy mode.
- Host isolation, bandwidth rate limiting.
- `warp-setup` WireGuard template generator.
- Systemd service unit + install script.
