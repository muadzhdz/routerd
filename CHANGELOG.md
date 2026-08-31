# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- **Web Dashboard** — dark-mode single-page app accessible from any AP-connected device
  - Overview: live status, connected clients (MAC/IP/hostname), bandwidth mini
  - Bandwidth: Chart.js 60s rolling graph, per-client TX/RX with progress bars
  - Logs: live tail hostapd+dnsmasq with filter/pause/resume/auto-scroll
  - Configuration: visual form editor + raw textarea, Save & Reload
  - VPN: tunnel status (endpoint/latency/handshake), conf editor, quick setup guide
- **Login page** — glassmorphism dark card, password eye toggle, spring animation
- **Session cookie auth** — `HttpOnly`, `SameSite=Lax`, 24h expiry, `Secure` flag on HTTPS
- **Brute force protection** — 5 failed attempts → 5 min lockout per IP
- **`routerd dashboard`** command — start web dashboard server
- **`routerd logs`** command — tail hostapd + dnsmasq logs simultaneously
- **PID lock file** (`/run/routerd/routerd.pid`) — prevents multiple concurrent instances
- **ProcessManager** struct with mutex — eliminates race condition on child process list
- **Child process watchdog** — goroutine monitors hostapd/dnsmasq, triggers graceful shutdown on crash
- **`VPN_DNS` config key** — replaces hardcoded `1.1.1.1` for forced DNS-over-VPN
- **`WPA3` config key** — WPA3-SAE/WPA2-PSK transition mode in hostapd
- **`DPIBypass` flag** — separates MSS clamping from VPN kill-switch logic
- **`warp-setup`** auto-generation — runs `wgcf register && generate` when wgcf installed
- **Full `reload`** — re-applies all NAT/VPN/isolation rules, not just restarts services
- **Enhanced `status`** — shows uptime, VPN endpoint, per-client MAC+IP from lease file
- **`sanitizeSSID()`** — strips `\n`, `\r`, `\x00`, `=` to prevent hostapd config injection
- **`rp_filter` restore** — saves/restores original values on VPN start/cleanup
- **`isAlreadyExists()`** helper — standardizes iptables idempotency error handling
- **`enableNAT` refactored** — split into 6 clean sub-functions with named constants
- **dnsmasq readiness polling** — replaced `time.Sleep(500ms)` with log polling
- **38 unit tests** — util, config, services, vpn packages — race detector clean
- **GitHub Actions CI** — `go vet`, `go test -race`, `staticcheck`, build artifact
- **`CHANGELOG.md`**, **`CONTRIBUTING.md`**, updated `routerd.conf.example`

### Fixed
- `go.mod` declaring `go 1.26` (non-existent) → `go 1.22`
- `cmdReload` skipped NAT/VPN changes — now full reload
- Forced DNS hardcoded to `1.1.1.1` → configurable via `VPN_DNS`
- `rp_filter` set to `0` but never restored on stop
- `dpibypass` mode incorrectly set `EnableVPN=true` → kill-switch blocked all traffic
- `cmdLogs` busy-spin loop → proper `io.EOF` sleep polling
- Secondary DNS hardcoded to `1.0.0.1` regardless of `VPN_DNS`
- CORS wildcard `Access-Control-Allow-Origin: *` → same-origin only
- WebSocket `CheckOrigin` accepting all origins → host validation
- `clients.json` permission `0644` → `0600`
- Signal channel capacity `1` → `2` (prevent watchdog deadlock)
- Stray `}, 3800);` JS syntax error in index.html
- Layout: cards outside `page-content` wrapper on config/vpn/logs/bandwidth pages
- Login page: brand not centered
- Sidebar: footer version text removed from all pages
- **`mangle PREROUTING: exit status 4`** — detect `iptables-nft` and strip `-w` flag
- Session cookie now sets `Secure: true` conditionally when request is HTTPS

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
