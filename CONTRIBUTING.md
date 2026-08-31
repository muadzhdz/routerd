# Contributing to routerd

Thanks for taking the time to contribute! This document explains how to set up the development environment, run tests, and submit changes.

---

## Requirements

- Go 1.22 or later
- Linux with a Wi-Fi card for integration testing (unit tests run on any OS)
- For full runtime testing: `hostapd`, `dnsmasq`, `iw`, `wireguard-tools`

---

## Getting Started

```sh
git clone https://github.com/muadzhdz/routerd
cd routerd
go build ./...
go test ./...
```

---

## Project Structure

| File | Purpose |
|---|---|
| `main.go` | CLI entry point, command dispatch, PID lock, state management |
| `config.go` | Config file parsing, defaults, input validation |
| `nat.go` | iptables rule management (NAT, forwarding, isolation, mangle) |
| `ap.go` | Wi-Fi AP interface creation and channel detection |
| `services.go` | hostapd/dnsmasq config generation and process management |
| `vpn.go` | WireGuard VPN bring-up and DPI bypass mode |
| `util.go` | Shared helpers (IP math, subprocess runner, MAC generation) |

---

## Running Tests

```sh
# All unit tests
go test ./...

# With race detector (recommended)
go test -race ./...

# With coverage
go test -coverprofile=cover.out ./...
go tool cover -html=cover.out
```

Tests do **not** require root or any installed system packages — they only test pure Go logic.

---

## Coding Style

- Follow standard `gofmt` formatting. Run `gofmt -w .` before committing.
- Use `go vet ./...` and `staticcheck ./...` — CI will check both.
- New public functions must have a godoc comment.
- Keep functions under ~50 lines. If a function is growing, split it.
- Use the existing `logInfo` / `logWarn` helpers rather than `fmt.Println` for daemon-visible output.

---

## Error Handling

- Prefer wrapping errors with context: `fmt.Errorf("doing X: %w", err)`.
- Use the `isAlreadyExists(output string) bool` helper when checking iptables idempotency errors.
- Do not swallow errors silently in the main paths — log with `logWarn` at minimum.

---

## Submitting Changes

1. Fork the repository and create a feature branch from `main`.
2. Write or update tests for your change.
3. Ensure `go test -race ./...` and `go vet ./...` pass locally.
4. Open a pull request with a clear title (under 70 chars) and description.
5. Reference any related issues in the PR description.

---

## Reporting Issues

Please include:
- routerd version (`routerd version`)
- Linux distribution and kernel version (`uname -r`)
- Wi-Fi card model (`lspci | grep -i wireless` or `iw list`)
- Relevant log output (`sudo routerd logs` or `journalctl -u routerd`)
