SHELL := /bin/bash
BIN := routerd
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
SYSCONFDIR ?= /etc
SYSTEMDDIR ?= $(SYSCONFDIR)/systemd/system

GO ?= go
LDFLAGS := -s -w \
	-X main.Version=$(VERSION) \
	-X main.GitCommit=$(COMMIT) \
	-X main.BuildDate=$(DATE)

.PHONY: all build clean test check-deps install uninstall status help

all: build

help:
	@echo "routerd Makefile target options:"
	@echo "  make build        - Compile optimized, stripped static binary"
	@echo "  make test         - Run entire unit test suite"
	@echo "  make check-deps   - Verify required Linux tools (hostapd, dnsmasq, iptables, etc.)"
	@echo "  make install      - Install binary, systemd unit, and config (requires sudo)"
	@echo "  make uninstall    - Remove binary and systemd service (preserves config)"
	@echo "  make clean        - Remove compiled binaries and artifacts"
	@echo ""
	@echo "Variables:"
	@echo "  PREFIX            - Installation prefix (default: /usr/local)"
	@echo "  DESTDIR           - Staging root directory for packaging (default: empty)"

build:
	@echo "==> Building $(BIN) (version: $(VERSION))..."
	CGO_ENABLED=0 $(GO) build -ldflags="$(LDFLAGS)" -trimpath -o $(BIN) .
	@echo "==> Build complete: $(BIN) ($$(ls -lh $(BIN) | awk '{print $$5}'))"

test:
	@echo "==> Running test suite..."
	$(GO) test -count=1 -v ./...

check-deps:
	@echo "==> Checking runtime dependencies on this machine..."
	@which ip >/dev/null 2>&1 && echo "  [OK] iproute2 (ip)" || echo "  [MISSING] iproute2 -> sudo pacman -S iproute2"
	@which iptables >/dev/null 2>&1 && echo "  [OK] iptables" || echo "  [MISSING] iptables -> sudo pacman -S iptables"
	@which hostapd >/dev/null 2>&1 && echo "  [OK] hostapd" || echo "  [MISSING] hostapd -> sudo pacman -S hostapd"
	@which dnsmasq >/dev/null 2>&1 && echo "  [OK] dnsmasq" || echo "  [MISSING] dnsmasq -> sudo pacman -S dnsmasq"
	@which iw >/dev/null 2>&1 && echo "  [OK] iw" || echo "  [MISSING] iw -> sudo pacman -S iw"
	@which wg-quick >/dev/null 2>&1 && echo "  [OK] wireguard-tools (wg-quick)" || echo "  [OPTIONAL] wireguard-tools -> sudo pacman -S wireguard-tools (for VPN uplink)"

install: build
	@echo "==> Installing $(BIN) to $(DESTDIR)$(BINDIR)..."
	install -d -m 755 $(DESTDIR)$(BINDIR)
	install -m 755 $(BIN) $(DESTDIR)$(BINDIR)/$(BIN)

	@echo "==> Installing systemd service to $(DESTDIR)$(SYSTEMDDIR)..."
	install -d -m 755 $(DESTDIR)$(SYSTEMDDIR)
	install -m 644 systemd/routerd.service $(DESTDIR)$(SYSTEMDDIR)/routerd.service

	@echo "==> Installing configuration template..."
	install -d -m 755 $(DESTDIR)$(SYSCONFDIR)/routerd
	@if [ ! -f $(DESTDIR)$(SYSCONFDIR)/routerd/routerd.conf ]; then \
		install -m 600 routerd.conf.example $(DESTDIR)$(SYSCONFDIR)/routerd/routerd.conf; \
		echo "  Installed new config: $(DESTDIR)$(SYSCONFDIR)/routerd/routerd.conf"; \
	else \
		install -m 644 routerd.conf.example $(DESTDIR)$(SYSCONFDIR)/routerd/routerd.conf.example; \
		echo "  Config exists: preserved $(DESTDIR)$(SYSCONFDIR)/routerd/routerd.conf"; \
		echo "  Example updated: $(DESTDIR)$(SYSCONFDIR)/routerd/routerd.conf.example"; \
	fi
	@if [ ! -f $(DESTDIR)$(SYSCONFDIR)/routerd/blocklist.txt ]; then \
		install -m 644 blocklist.txt $(DESTDIR)$(SYSCONFDIR)/routerd/blocklist.txt; \
		echo "  Installed new blocklist: $(DESTDIR)$(SYSCONFDIR)/routerd/blocklist.txt"; \
	else \
		install -m 644 blocklist.txt $(DESTDIR)$(SYSCONFDIR)/routerd/blocklist.txt.example; \
		echo "  Blocklist exists: preserved $(DESTDIR)$(SYSCONFDIR)/routerd/blocklist.txt"; \
		echo "  Example updated: $(DESTDIR)$(SYSCONFDIR)/routerd/blocklist.txt.example"; \
	fi
	@install -m 600 vpn.conf.example $(DESTDIR)$(SYSCONFDIR)/routerd/vpn.conf.example
	@echo "  Installed VPN template: $(DESTDIR)$(SYSCONFDIR)/routerd/vpn.conf.example"

	@if [ -z "$(DESTDIR)" ] && command -v systemctl >/dev/null 2>&1; then \
		echo "==> Reloading systemd daemon..."; \
		systemctl daemon-reload; \
		echo ""; \
		echo "routerd successfully installed!"; \
		echo "Next steps:"; \
		echo "  1. Edit settings : sudo nvim $(SYSCONFDIR)/routerd/routerd.conf"; \
		echo "  2. Start service : sudo systemctl enable --now routerd"; \
		echo "  3. Attach TUI    : routerd"; \
	fi

uninstall:
	@echo "==> Uninstalling $(BIN)..."
	@if [ -z "$(DESTDIR)" ] && command -v systemctl >/dev/null 2>&1; then \
		systemctl stop routerd 2>/dev/null || true; \
		systemctl disable routerd 2>/dev/null || true; \
	fi
	rm -f $(DESTDIR)$(BINDIR)/$(BIN)
	rm -f $(DESTDIR)$(SYSTEMDDIR)/routerd.service
	@if [ -z "$(DESTDIR)" ] && command -v systemctl >/dev/null 2>&1; then \
		systemctl daemon-reload 2>/dev/null || true; \
	fi
	@echo "==> routerd binary and systemd service removed."
	@echo "Note: Configuration directory $(DESTDIR)$(SYSCONFDIR)/routerd was preserved."
	@echo "To remove configuration manually, run: sudo rm -rf $(DESTDIR)$(SYSCONFDIR)/routerd"

clean:
	@echo "==> Cleaning build artifacts..."
	rm -f $(BIN) routerd-*.tar.gz routerd-*-checksums.txt
