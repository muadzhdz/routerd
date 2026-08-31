BIN       := routerd
PREFIX    := /usr/local
VERSION   := 0.1.0

GO        ?= go
LDFLAGS   := -trimpath -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all build install uninstall clean test coverage vet fmt

all: build

build:
	$(GO) build $(LDFLAGS) -o $(BIN) .

install: build
	install -Dm755 $(BIN)               $(DESTDIR)$(PREFIX)/bin/$(BIN)
	install -Dm644 routerd.service      $(DESTDIR)/etc/systemd/system/routerd.service
	install -Dm644 routerd.conf.example $(DESTDIR)/etc/routerd.conf.example
	install -Dm644 vpn.conf.example     $(DESTDIR)/etc/routerd/vpn.conf.example
	install -Dm644 90-routerd.conf      $(DESTDIR)/etc/NetworkManager/conf.d/90-routerd.conf
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
	rm -f $(BIN) coverage.out

# Run tests with race detector
test:
	$(GO) test -race -count=1 ./...

# Run tests with race detector + generate coverage report
coverage:
	$(GO) test -race -count=1 -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -func=coverage.out | tail -5

# Run go vet
vet:
	$(GO) vet ./...

# Check formatting (exits non-zero if any file is unformatted)
fmt:
	@UNFORMATTED=$$(gofmt -l .); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "Unformatted files:"; echo "$$UNFORMATTED"; exit 1; \
	fi
	@echo "gofmt: all files formatted"
