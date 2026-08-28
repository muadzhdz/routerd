BIN       := routerd
PREFIX    := /usr/local
VERSION   := 0.1.0

GO        ?= go

.PHONY: all build install uninstall clean test

all: build

build:
	$(GO) build -trimpath -ldflags "-s -w" -o $(BIN) .

install: build
	install -Dm755 $(BIN) $(DESTDIR)$(PREFIX)/bin/$(BIN)
	install -Dm644 routerd.service $(DESTDIR)/etc/systemd/system/routerd.service
	install -Dm644 routerd.conf.example $(DESTDIR)/etc/routerd.conf.example
	install -Dm644 vpn.conf.example $(DESTDIR)/etc/routerd/vpn.conf.example
	install -Dm644 90-routerd.conf $(DESTDIR)/etc/NetworkManager/conf.d/90-routerd.conf
	@echo "installed binary + systemd unit + NetworkManager rule"

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BIN)
	rm -f $(DESTDIR)/etc/systemd/system/routerd.service
	rm -f $(DESTDIR)/etc/routerd.conf
	rm -f $(DESTDIR)/etc/routerd.conf.example
	rm -f $(DESTDIR)/etc/routerd/vpn.conf
	rm -f $(DESTDIR)/etc/routerd/vpn.conf.example
	rm -f $(DESTDIR)/etc/NetworkManager/conf.d/90-routerd.conf
	@echo "uninstalled"

clean:
	rm -f $(BIN)

test:
	$(GO) test ./...
