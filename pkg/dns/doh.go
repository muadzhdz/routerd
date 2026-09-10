package dns

import (
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

const upstreamDoH = "https://1.1.1.1/dns-query"

// DNSEvent merepresentasikan satu aktivitas resolusi DNS untuk telemetry dashboard
type DNSEvent struct {
	ClientIP string
	Domain   string
	Latency  time.Duration
	Success  bool
}

// extractQName membaca nama domain dari wire format DNS (RFC 1035) tanpa library pihak ketiga
func extractQName(data []byte) string {
	if len(data) < 12 {
		return "malformed"
	}
	offset := 12
	var labels []string
	for offset < len(data) {
		length := int(data[offset])
		if length == 0 {
			break
		}
		if length > 63 || offset+1+length > len(data) {
			return "unknown"
		}
		labels = append(labels, string(data[offset+1:offset+1+length]))
		offset += 1 + length
	}
	if len(labels) == 0 {
		return "root"
	}
	return strings.Join(labels, ".")
}

// StartDoHServer menjalankan UDP DNS listener lokal yang me-relay query ke Cloudflare DoH
func StartDoHServer(listenAddr string, stopChan <-chan struct{}, eventChan chan<- DNSEvent) error {
	pc, err := net.ListenPacket("udp", listenAddr)
	if err != nil {
		return err
	}
	defer pc.Close()

	log.Printf("SUCCESS: Stealth DoH Proxy AKTIF di [%s] -> %s", listenAddr, upstreamDoH)

	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	buf := make([]byte, 4096)

	// Goroutine untuk menutup listener saat program exit
	go func() {
		<-stopChan
		pc.Close()
	}()

	for {
		n, clientAddr, err := pc.ReadFrom(buf)
		if err != nil {
			select {
			case <-stopChan:
				return nil
			default:
				continue
			}
		}

		// Duplikasi slice agar tidak tertimpa iterasi berikutnya
		queryData := make([]byte, n)
		copy(queryData, buf[:n])

		// Tangani tiap query di goroutine terpisah agar cepat (asynchronous)
		go func(addr net.Addr, data []byte) {
			start := time.Now()
			domain := extractQName(data)

			req, err := http.NewRequest("POST", upstreamDoH, bytes.NewReader(data))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/dns-message")
			req.Header.Set("Accept", "application/dns-message")

			resp, err := client.Do(req)
			if err != nil {
				if eventChan != nil {
					select {
					case eventChan <- DNSEvent{
						ClientIP: addr.String(),
						Domain:   domain,
						Latency:  time.Since(start),
						Success:  false,
					}:
					default:
					}
				}
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return
			}

			dnsResp, err := io.ReadAll(resp.Body)
			if err != nil {
				return
			}

			// Kirim kembali respons DNS asli ke client
			_, _ = pc.WriteTo(dnsResp, addr)

			// Emit event telemetry ke dashboard secara non-blocking
			if eventChan != nil {
				select {
				case eventChan <- DNSEvent{
					ClientIP: addr.String(),
					Domain:   domain,
					Latency:  time.Since(start),
					Success:  true,
				}:
				default:
				}
			}
		}(clientAddr, queryData)
	}
}
