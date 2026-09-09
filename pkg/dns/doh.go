package dns

import (
    "bytes"
    "io"
    "log"
    "net"
    "net/http"
    "time"
)

const upstreamDoH = "https://1.1.1.1/dns-query"
// StartDoHServer menjalankan UDP DNS listener lokal yang me-relay query ke Cloudflare DoH
func StartDoHServer(listenAddr string, stopChan <-chan struct{}) error {
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
        	req, err := http.NewRequest("POST", upstreamDoH, bytes.NewReader(data))
            if err != nil {
                return
            }
            req.Header.Set("Content-Type", "application/dns-message")
            req.Header.Set("Accept", "application/dns-message")

            resp, err := client.Do(req)
            if err != nil {
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
        }(clientAddr, queryData)
    }
}
