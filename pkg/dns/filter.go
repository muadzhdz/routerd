package dns

import (
	"bufio"
	"os"
	"strings"
	"sync"
)

// Filter provides in-memory domain blacklisting with fast subdomain suffix matching.
type Filter struct {
	mu      sync.RWMutex
	blocked map[string]struct{}
}

// NewFilter creates an empty domain filter instance.
func NewFilter() *Filter {
	return &Filter{
		blocked: make(map[string]struct{}),
	}
}

// NormalizeDomain cleans up a domain name for consistent lookup.
func NormalizeDomain(domain string) string {
	d := strings.ToLower(strings.TrimSpace(domain))
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimSuffix(d, ".")
	return d
}

// Add inserts a domain into the blacklist.
func (f *Filter) Add(domain string) {
	d := NormalizeDomain(domain)
	if d == "" || strings.HasPrefix(d, "#") || strings.HasPrefix(d, ";") || strings.HasPrefix(d, "!") {
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocked[d] = struct{}{}
}

// LoadFile reads domains from a file supporting both /etc/hosts format
// (e.g. '0.0.0.0 ad.com') and raw domain list format (e.g. 'ad.com').
func (f *Filter) LoadFile(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0

	f.mu.Lock()
	defer f.mu.Unlock()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "!") {
			continue
		}

		// Strip inline comments
		if idx := strings.IndexAny(line, "#;"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		var targetDomain string
		// /etc/hosts format: IP followed by domain (e.g. '0.0.0.0 doubleclick.net')
		if len(fields) >= 2 && (fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1" || fields[0] == "::" || fields[0] == "::1") {
			targetDomain = fields[1]
		} else {
			// Plain domain list (e.g. 'doubleclick.net')
			targetDomain = fields[0]
		}

		d := NormalizeDomain(targetDomain)
		if d != "" && d != "localhost" && d != "broadcasthost" && d != "local" {
			if _, exists := f.blocked[d]; !exists {
				f.blocked[d] = struct{}{}
				count++
			}
		}
	}

	return count, scanner.Err()
}

// IsBlocked checks whether a domain or any of its parent domain suffixes
// matches an entry in the blacklist.
func (f *Filter) IsBlocked(domain string) bool {
	d := NormalizeDomain(domain)
	if d == "" {
		return false
	}

	f.mu.RLock()
	defer f.mu.RUnlock()

	// 1. Exact match check (O(1))
	if _, found := f.blocked[d]; found {
		return true
	}

	// 2. Subdomain suffix matching (e.g. 'ad.doubleclick.net' -> 'doubleclick.net')
	for {
		idx := strings.Index(d, ".")
		if idx == -1 {
			break
		}
		d = d[idx+1:]
		if _, found := f.blocked[d]; found {
			return true
		}
	}

	return false
}

// Count returns the total number of blocked domain rules in memory.
func (f *Filter) Count() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.blocked)
}
