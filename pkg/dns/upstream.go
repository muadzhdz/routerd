package dns

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var defaultUpstreams = []string{
	"https://1.1.1.1/dns-query",      // Cloudflare Primary
	"https://dns.google/dns-query",    // Google Fallback
	"https://dns.quad9.net/dns-query", // Quad9 Fallback
}

// Resolver queries upstream DoH endpoints with sequential failover.
type Resolver struct {
	client    *http.Client
	upstreams []string
}

// NewResolver creates a new resolver with the specified DoH upstream endpoints.
func NewResolver(upstreams []string, timeout time.Duration) *Resolver {
	if len(upstreams) == 0 {
		upstreams = defaultUpstreams
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Resolver{
		client: &http.Client{
			Timeout: timeout,
		},
		upstreams: upstreams,
	}
}

// Query transmits a DNS wire message to upstream DoH providers, failing over sequentially if errors occur.
func (r *Resolver) Query(ctx context.Context, queryData []byte) ([]byte, string, error) {
	var lastErr error

	for _, endpoint := range r.upstreams {
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(queryData))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/dns-message")
		req.Header.Set("Accept", "application/dns-message")

		resp, err := r.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("upstream %s timeout/error: %w", endpoint, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("upstream %s returned HTTP status %d", endpoint, resp.StatusCode)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("failed to read response body from %s: %w", endpoint, err)
			continue
		}

		if len(body) < 12 {
			lastErr = fmt.Errorf("upstream response from %s too short (<12 bytes)", endpoint)
			continue
		}

		return body, endpoint, nil
	}

	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", errors.New("no upstream DoH endpoints available")
}
