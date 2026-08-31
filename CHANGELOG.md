# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- **`CommandRunner` interface** (`runner.go`) — abstracts all `os/exec` calls behind an interface; `OSRunner` is the production implementation; tests inject `MockRunner` to capture calls without spawning real processes
- **Injectable `runDir`** — `writeStateTo()`/`readStateFrom()` accept a directory parameter, making state functions testable without `/run/routerd` hardcoded
- **159 unit tests** — covers ap, nat, vpn, state, clients, config, services, dashboard; race-detector clean
- **`doc.go`** — single authoritative package-level Godoc comment
- **`--enable` flag** in `install.sh` — runs `systemctl enable --now routerd` automatically
- **Root check** in `install.sh` — exits with clear error if not run as root
- **`/run/routerd` pre-creation** in installer + `tmpfiles.d` rule for reboot persistence
- **`/etc/routerd` directory** created with `700` permissions during install
- **`PersistentKeepalive`** added to `vpn.conf.example`
- **Systemd hardening** in `routerd.service` — `ProtectHome`, `PrivateTmp`, `CapabilityBoundingSet`, `KillMode=mixed`, `RuntimeDirectory`
- **`make coverage`** target — runs tests with `-coverprofile` and prints summary
- **`make vet` / `make fmt`** targets
- **CI pipeline** (`ci.yml`) — single job: checkout → go mod verify → build → vet → gofmt check → test -race -coverprofile → coverage summary → upload artifact
- **`-X main.version`** linker flag in Makefile injects version at build time

### Fixed
- `_isNFT` init-time `runCmd` call — converted to lazy `sync.Once` singleton (`isNFT()`) so tests can swap `defaultRunner` before detection runs
- `ipNetmask()` nil-pointer panic on invalid CIDR — added nil guard with `255.255.255.0` fallback
- `createAPInterface` `if/else` with `:=` scope — refactored to clean sequential `if err != nil` pattern
- Duplicate `// Package main` godoc comment across 10 source files — consolidated into `doc.go`
- `coverage.out` accidentally committed — added to `.gitignore`, removed from tracking
- `install.sh` missing root check — would silently fail halfway through on non-root
- `install.sh` no `--noconfirm` / `DEBIAN_FRONTEND=noninteractive` — package install would hang in CI/non-interactive shells
- `Makefile test` target missing race detector
- `vpn.conf.example` minimal — added `PersistentKeepalive`, better comments, all setup options

---

## [0.1.0] — 2025-08-28

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
- **Full `reload`** with rollback — re-applies all NAT/VPN/isolation rules; rolls back to old config on failure
- **Context-based cancellation** — `signal.NotifyContext` replaces manual signal channel
- **`log.Fatalf` → `logWarn + return`** in `cmdStart` — deferred cleanup always runs
- **Enhanced `status`** — shows uptime, VPN endpoint, per-client MAC+IP from lease file
- **`sanitizeSSID()`** — strips `\n`, `\r`, `\x00`, `=` to prevent hostapd config injection
- **`rp_filter` restore** — saves/restores original values on VPN start/cleanup
- **`isAlreadyExists()`** helper — standardizes iptables idempotency error handling
- **`enableNAT` refactored** — split into 6 clean sub-functions with named chain constants
- **dnsmasq readiness polling** — replaced `time.Sleep(500ms)` with log polling
- **GitHub Actions CI** — build, vet, gofmt, test -race, coverage artifact
- **`CHANGELOG.md`**, **`CONTRIBUTING.md`**, **`README.md`** comprehensive docs

### Fixed
- `go.mod` declaring `go 1.26` (non-existent) → `go 1.22`
- `cmdReload` skipped NAT/VPN changes — now full reload with atomic rollback
- Forced DNS hardcoded to `1.1.1.1` → configurable via `VPN_DNS`
- `rp_filter` set to `0` but never restored on stop
- `dpibypass` mode incorrectly set `EnableVPN=true` → kill-switch blocked all traffic
- `cmdLogs` busy-spin loop → proper `io.EOF` sleep polling
- Secondary DNS hardcoded to `1.0.0.1` regardless of `VPN_DNS`
- CORS wildcard `Access-Control-Allow-Origin: *` → same-origin only
- WebSocket `CheckOrigin` accepting all origins → host validation
- `clients.json` permission `0644` → `0600`
- Signal channel capacity `1` → `2` (prevent watchdog deadlock)
- **`mangle PREROUTING: exit status 4`** — detect `iptables-nft` and strip `-w` flag
- Session cookie `Secure` flag set conditionally (only over HTTPS)


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
