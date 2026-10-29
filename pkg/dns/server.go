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

// DNSEvent represents a single DNS resolution activity for dashboard telemetry.
type DNSEvent struct {
	ClientIP string
	Domain   string
	Latency  time.Duration
	Success  bool
}

// ServerConfig stores configuration parameters for the DNS Resolver Server.
type ServerConfig struct {
	ListenAddr    string
	ListenAddrs   []string
	Upstreams     []string
	QueryTimeout  time.Duration
	CacheEntries  int
	BlockAds      bool
	BlocklistPath string
}

// Server manages the UDP listener sockets, cache, ad filter, and upstream DoH/DoT resolution.
type Server struct {
	cfg       ServerConfig
	pcs       []net.PacketConn
	pc        net.PacketConn // Primary connection pointer for backwards compatibility
	cache     *Cache
	filter    *Filter
	resolver  *Resolver
	events    chan DNSEvent
	closeOnce sync.Once
	stopChan  chan struct{}
	wg        sync.WaitGroup
}

// NewServer creates a new DNS Resolver Server instance.
func NewServer(cfg ServerConfig) (*Server, error) {
	if len(cfg.ListenAddrs) == 0 {
		if cfg.ListenAddr != "" {
			cfg.ListenAddrs = []string{cfg.ListenAddr}
		} else {
			cfg.ListenAddrs = []string{"127.0.0.1:53"}
		}
	}
	if cfg.ListenAddr == "" && len(cfg.ListenAddrs) > 0 {
		cfg.ListenAddr = cfg.ListenAddrs[0]
	}

	if cfg.QueryTimeout <= 0 {
		cfg.QueryTimeout = 2 * time.Second
	}
	if cfg.CacheEntries <= 0 {
		cfg.CacheEntries = 2048
	}

	var flt *Filter
	if cfg.BlockAds {
		flt = NewFilter()
		if cfg.BlocklistPath != "" {
			count, err := flt.LoadFile(cfg.BlocklistPath)
			if err != nil {
				log.Printf("Warning: error loading DNS blocklist from %s: %v", cfg.BlocklistPath, err)
			} else {
				log.Printf("SUCCESS: DNS Ad & Malware Blocker ACTIVE (%d rules loaded from %s)", count, cfg.BlocklistPath)
			}
		}
	}

	return &Server{
		cfg:      cfg,
		cache:    NewCache(cfg.CacheEntries),
		filter:   flt,
		resolver: NewResolver(cfg.Upstreams, cfg.QueryTimeout),
		events:   make(chan DNSEvent, 100),
		stopChan: make(chan struct{}),
	}, nil
}

// Events returns a receive-only channel for DNS telemetry events.
func (s *Server) Events() <-chan DNSEvent {
	return s.events
}

// Cache returns a pointer to the in-memory cache for inspection or testing.
func (s *Server) Cache() *Cache {
	return s.cache
}

// Filter returns a pointer to the ad filter for inspection or testing.
func (s *Server) Filter() *Filter {
	return s.filter
}

// Start opens the UDP listener sockets and processes incoming queries asynchronously.
func (s *Server) Start() error {
	for _, addr := range s.cfg.ListenAddrs {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			// Clean up already opened connections if failure occurs
			for _, opened := range s.pcs {
				_ = opened.Close()
			}
			return fmt.Errorf("failed to bind UDP DNS listener on %s: %w", addr, err)
		}
		s.pcs = append(s.pcs, pc)
		log.Printf("SUCCESS: Stealth DoH/DoT DNS Resolver ACTIVE on [%s] (Cache: %d entries)", addr, s.cfg.CacheEntries)
	}

	if len(s.pcs) > 0 {
		s.pc = s.pcs[0]
	}

	// Goroutine for periodic cache pruning every 1 minute
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

	for _, pc := range s.pcs {
		s.wg.Add(1)
		go s.serveConn(pc)
	}

	return nil
}

func (s *Server) serveConn(pc net.PacketConn) {
	defer s.wg.Done()
	buf := make([]byte, 4096)

	for {
		n, clientAddr, err := pc.ReadFrom(buf)
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

		go s.handleQuery(pc, clientAddr, queryData)
	}
}

func (s *Server) handleQuery(pc net.PacketConn, clientAddr net.Addr, queryData []byte) {
	if len(queryData) < 12 {
		return
	}

	start := time.Now()
	clientTID := binary.BigEndian.Uint16(queryData[0:2])
	domain, qtype, err := ExtractQuestion(queryData)
	if err != nil {
		domain = "malformed"
	}

	// 1. Check Ad & Malware Filter (Sinkhole 0.0.0.0 / ::)
	if s.filter != nil && s.filter.IsBlocked(domain) {
		sinkhole := BuildSinkholeResponse(queryData, clientTID, qtype)
		if sinkhole != nil {
			_, _ = pc.WriteTo(sinkhole, clientAddr)
			s.emitEvent(clientAddr.String(), domain+" (blocked)", time.Since(start), true)
			return
		}
	}

	// 2. Check in-memory cache
	cacheKey := CacheKey(domain, qtype)
	if cachedResp, hit := s.cache.Get(cacheKey, clientTID); hit {
		_, _ = pc.WriteTo(cachedResp, clientAddr)
		s.emitEvent(clientAddr.String(), domain+" (cache)", time.Since(start), true)
		return
	}

	// 3. Query upstream DoH/DoT with fallback
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.QueryTimeout)
	defer cancel()

	resp, _, err := s.resolver.Query(ctx, queryData)
	if err != nil {
		if failResp := BuildServFailResponse(queryData, clientTID); failResp != nil {
			_, _ = pc.WriteTo(failResp, clientAddr)
		}
		s.emitEvent(clientAddr.String(), domain, time.Since(start), false)
		return
	}

	// 4. Store in cache if response is valid (including NXDOMAIN negative caching, RFC 2308)
	ttl := ExtractTTL(resp)
	if len(resp) >= 4 && (resp[3]&0x0F == 3) { // RCODE == 3 (NXDOMAIN)
		if ttl == 0 || ttl > 60*time.Second {
			ttl = 60 * time.Second
		}
	}
	s.cache.Set(cacheKey, resp, ttl)

	// 5. Send response back to client
	_, _ = pc.WriteTo(resp, clientAddr)
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

// Close terminates all UDP sockets and stops all resolver goroutines.
func (s *Server) Close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		close(s.stopChan)
		for _, pc := range s.pcs {
			if pc != nil {
				if err := pc.Close(); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		s.wg.Wait()
	})
	return firstErr
}

// StartDoHServer is a backward-compatible adapter for the legacy entry point.
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
