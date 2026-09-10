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

// Resolver menghubungi upstream DoH dengan mekanisme failover berurutan.
type Resolver struct {
	client    *http.Client
	upstreams []string
}

// NewResolver membuat resolver baru dengan daftar endpoint upstream DoH.
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

// Query mengirim query wire DNS ke upstream DoH dan fallback ke upstream berikutnya jika gagal.
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
			lastErr = fmt.Errorf("upstream %s mengembalikan status %d", endpoint, resp.StatusCode)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("gagal membaca body %s: %w", endpoint, err)
			continue
		}

		if len(body) < 12 {
			lastErr = fmt.Errorf("jawaban upstream %s terlalu pendek (<12 bytes)", endpoint)
			continue
		}

		return body, endpoint, nil
	}

	if lastErr != nil {
		return nil, "", lastErr
	}
	return nil, "", errors.New("tidak ada upstream DoH yang tersedia")
}
