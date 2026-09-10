package dns

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// DNSEvent merepresentasikan satu aktivitas resolusi DNS untuk telemetry dashboard.
type DNSEvent struct {
	ClientIP string
	Domain   string
	Latency  time.Duration
	Success  bool
}

// ServerConfig menyimpan parameter konfigurasi untuk DNS Resolver Server.
type ServerConfig struct {
	ListenAddr   string
	Upstreams    []string
	QueryTimeout time.Duration
	CacheEntries int
}

// Server mengelola UDP listener socket, cache, dan upstream DoH resolving.
type Server struct {
	cfg       ServerConfig
	pc        net.PacketConn
	cache     *Cache
	resolver  *Resolver
	events    chan DNSEvent
	closeOnce sync.Once
	stopChan  chan struct{}
	wg        sync.WaitGroup
}

// NewServer membuat instance DNS Resolver Server baru.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:53"
	}
	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 2 * time.Second
	}
	if cfg.CacheEntries <= 0 {
		cfg.CacheEntries = 2048
	}

	return &Server{
		cfg:      cfg,
		cache:    NewCache(cfg.CacheEntries),
		resolver: NewResolver(cfg.Upstreams, cfg.QueryTimeout),
		events:   make(chan DNSEvent, 100),
		stopChan: make(chan struct{}),
	}, nil
}

// Events mengembalikan channel receive-only untuk telemetry event DNS.
func (s *Server) Events() <-chan DNSEvent {
	return s.events
}

// Cache mengembalikan pointer ke cache in-memory untuk inspeksi atau testing.
func (s *Server) Cache() *Cache {
	return s.cache
}

// Start membuka UDP listener socket dan memproses query secara asynchronous.
func (s *Server) Start() error {
	pc, err := net.ListenPacket("udp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("gagal bind UDP DNS listener pada %s: %w", s.cfg.ListenAddr, err)
	}
	s.pc = pc

	log.Printf("SUCCESS: Stealth DoH DNS Resolver AKTIF di [%s] (Cache: %d entri)", s.cfg.ListenAddr, s.cfg.CacheEntries)

	// Goroutine untuk periodic cache pruning tiap 1 menit
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.cache.Prune()
			case <-s.stopChan:
				return
			}
		}
	}()

	s.wg.Add(1)
	go s.serve()

	return nil
}

func (s *Server) serve() {
	defer s.wg.Done()
	buf := make([]byte, 4096)

	for {
		n, clientAddr, err := s.pc.ReadFrom(buf)
		if err != nil {
			select {
			case <-s.stopChan:
				return
			default:
				continue
			}
		}

		queryData := make([]byte, n)
		copy(queryData, buf[:n])

		go s.handleQuery(clientAddr, queryData)
	}
}

func (s *Server) handleQuery(clientAddr net.Addr, queryData []byte) {
	if len(queryData) < 12 {
		return
	}

	start := time.Now()
	clientTID := binary.BigEndian.Uint16(queryData[0:2])
	domain, qtype, err := ExtractQuestion(queryData)
	if err != nil {
		domain = "malformed"
	}

	// 1. Cek in-memory cache
	cacheKey := CacheKey(domain, qtype)
	if cachedResp, hit := s.cache.Get(cacheKey, clientTID); hit {
		_, _ = s.pc.WriteTo(cachedResp, clientAddr)
		s.emitEvent(clientAddr.String(), domain+" (cache)", time.Since(start), true)
		return
	}

	// 2. Query ke upstream DoH dengan fallback
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.QueryTimeout)
	defer cancel()

	resp, _, err := s.resolver.Query(ctx, queryData)
	if err != nil {
		s.emitEvent(clientAddr.String(), domain, time.Since(start), false)
		return
	}

	// 3. Simpan ke cache jika ada respon valid
	ttl := ExtractTTL(resp)
	s.cache.Set(cacheKey, resp, ttl)

	// 4. Kirim respons ke client
	_, _ = s.pc.WriteTo(resp, clientAddr)
	s.emitEvent(clientAddr.String(), domain, time.Since(start), true)
}

func (s *Server) emitEvent(clientIP, domain string, latency time.Duration, success bool) {
	select {
	case s.events <- DNSEvent{
		ClientIP: clientIP,
		Domain:   domain,
		Latency:  latency,
		Success:  success,
	}:
	default:
	}
}

// Close menutup socket UDP dan menghentikan seluruh goroutine resolver.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.stopChan)
		if s.pc != nil {
			err = s.pc.Close()
		}
		s.wg.Wait()
	})
	return err
}

// StartDoHServer adalah backward-compatible adapter untuk fungsi lama.
func StartDoHServer(listenAddr string, stopChan <-chan struct{}, eventChan chan<- DNSEvent) error {
	srv, err := NewServer(ServerConfig{
		ListenAddr: listenAddr,
	})
	if err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return err
	}

	if eventChan != nil {
		go func() {
			for ev := range srv.Events() {
				select {
				case eventChan <- ev:
				default:
				}
			}
		}()
	}

	<-stopChan
	return srv.Close()
}
