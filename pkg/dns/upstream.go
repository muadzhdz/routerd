package dns

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var defaultUpstreams = []string{
	"https://1.1.1.1/dns-query",       // Cloudflare DoH
	"tls://1.1.1.1:853",               // Cloudflare DoT
	"https://dns.google/dns-query",    // Google DoH
	"tls://8.8.8.8:853",               // Google DoT
	"https://dns.quad9.net/dns-query", // Quad9 DoH
	"tls://9.9.9.9:853",               // Quad9 DoT
}

// Resolver queries upstream DoH and DoT endpoints with sequential failover.
type Resolver struct {
	client    *http.Client
	upstreams []string
	timeout   time.Duration
}

// NormalizeUpstream converts a raw IP, DoH URL, or DoT URI into a normalized endpoint.
func NormalizeUpstream(upstream string) string {
	u := strings.TrimSpace(upstream)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "tls://") {
		return u
	}
	switch u {
	case "1.1.1.1", "1.0.0.1":
		return "https://1.1.1.1/dns-query"
	case "8.8.8.8", "8.8.4.4":
		return "https://dns.google/dns-query"
	case "9.9.9.9", "149.112.112.112":
		return "https://dns.quad9.net/dns-query"
	default:
		return fmt.Sprintf("https://%s/dns-query", u)
	}
}

// NewResolver creates a new resolver with the specified DoH/DoT upstream endpoints.
func NewResolver(upstreams []string, timeout time.Duration) *Resolver {
	var normalized []string
	for _, u := range upstreams {
		if norm := NormalizeUpstream(u); norm != "" {
			normalized = append(normalized, norm)
		}
	}
	if len(normalized) == 0 {
		normalized = defaultUpstreams
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Resolver{
		client: &http.Client{
			Timeout: timeout,
		},
		upstreams: normalized,
		timeout:   timeout,
	}
}

// queryDoT queries a DNS-over-TLS (RFC 7858) upstream server over port 853.
func (r *Resolver) queryDoT(ctx context.Context, endpoint string, queryData []byte) ([]byte, error) {
	rawTarget := strings.TrimPrefix(endpoint, "tls://")
	host, port, err := net.SplitHostPort(rawTarget)
	if err != nil {
		host = rawTarget
		port = "853"
	}

	dialer := &net.Dialer{Timeout: r.timeout}
	tlsConf := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}

	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, port), tlsConf)
	if err != nil {
		return nil, fmt.Errorf("tls dial error on %s: %w", endpoint, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(r.timeout))
	}

	// RFC 7858 Section 3.3: 2-octet length prefix
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(queryData)))

	if _, err := conn.Write(lenBuf[:]); err != nil {
		return nil, fmt.Errorf("failed to write length prefix: %w", err)
	}
	if _, err := conn.Write(queryData); err != nil {
		return nil, fmt.Errorf("failed to write query wire data: %w", err)
	}

	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("failed to read response length prefix: %w", err)
	}

	respLen := int(binary.BigEndian.Uint16(lenBuf[:]))
	if respLen < 12 || respLen > 4096 {
		return nil, fmt.Errorf("invalid DNS wire response length (%d)", respLen)
	}

	respBody := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBody); err != nil {
		return nil, fmt.Errorf("failed to read complete wire response: %w", err)
	}

	return respBody, nil
}

// Query transmits a DNS wire message to upstream DoH/DoT providers, failing over sequentially if errors occur.
func (r *Resolver) Query(ctx context.Context, queryData []byte) ([]byte, string, error) {
	var lastErr error

	for _, endpoint := range r.upstreams {
		if strings.HasPrefix(endpoint, "tls://") {
			body, err := r.queryDoT(ctx, endpoint, queryData)
			if err != nil {
				lastErr = fmt.Errorf("upstream DoT %s error: %w", endpoint, err)
				continue
			}
			return body, endpoint, nil
		}

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
	return nil, "", errors.New("no upstream DoH/DoT endpoints available")
}
