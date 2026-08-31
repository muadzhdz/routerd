// Package dashboard provides the routerd web dashboard — an HTTP server that
// exposes REST/JSON API endpoints and a WebSocket push channel for real-time
// status updates, bundled with a dark-mode single-page frontend.
package dashboard

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// --- Session store ----------------------------------------------------------

const sessionCookieName = "routerd_session"
const sessionDuration   = 24 * time.Hour

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]time.Time // token → expiry
}

func newSessionStore() *sessionStore {
	s := &sessionStore{sessions: make(map[string]time.Time)}
	go s.cleanup()
	return s
}

func (s *sessionStore) create() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(sessionDuration)
	s.mu.Unlock()
	return token
}

func (s *sessionStore) valid(token string) bool {
	s.mu.RLock()
	exp, ok := s.sessions[token]
	s.mu.RUnlock()
	return ok && time.Now().Before(exp)
}

func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *sessionStore) cleanup() {
	t := time.NewTicker(10 * time.Minute)
	for range t.C {
		now := time.Now()
		s.mu.Lock()
		for tok, exp := range s.sessions {
			if now.After(exp) {
				delete(s.sessions, tok)
			}
		}
		s.mu.Unlock()
	}
}

// Server is the dashboard HTTP server.
type Server struct {
	configPath  string
	vpnConfPath string
	runDir      string
	version     string
	password    string // empty = no auth
	bind        string
	port        int

	hub      *wsHub
	sessions *sessionStore
}

// NewServer constructs a Server from the provided parameters.
func NewServer(configPath, vpnConfPath, runDir, version, bind, password string, port int) *Server {
	return &Server{
		configPath:  configPath,
		vpnConfPath: vpnConfPath,
		runDir:      runDir,
		version:     version,
		password:    password,
		bind:        bind,
		port:        port,
		hub:         newWSHub(),
		sessions:    newSessionStore(),
	}
}

// Run starts the HTTP server and blocks until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// Auth endpoints (always public).
	mux.HandleFunc("/auth/login",  s.handleAuthLogin)
	mux.HandleFunc("/auth/logout", s.handleAuthLogout)

	// Static files (embedded).
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))

	// API routes.
	s.registerAPIRoutes(mux)

	// WebSocket endpoint.
	mux.HandleFunc("/ws", s.handleWS)

	// Wrap everything with auth middleware (login page is exempted inside).
	handler := s.authMiddleware(corsMiddleware(mux))

	addr := fmt.Sprintf("%s:%d", s.bind, s.port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start WebSocket broadcast loop.
	go s.hub.run()
	go s.broadcastLoop(ctx)

	log.Printf("[dashboard] listening on http://%s", addr)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// --- Auth middleware ---------------------------------------------------------

// publicPaths are always accessible without authentication.
var publicPaths = map[string]bool{
	"/login.html":   true,
	"/auth/login":   true,
	"/style.css":    true,
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No password set — allow everything.
		if s.password == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Always allow public paths.
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// Check session cookie.
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if s.sessions.valid(cookie.Value) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// API requests get 401 JSON, not a redirect.
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"message":"Unauthorized"}`))
			return
		}

		// Everything else → redirect to login.
		http.Redirect(w, r, "/login.html", http.StatusFound)
	})
}

// handleAuthLogin validates the password and issues a session cookie.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.password)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"message":"Invalid password"}`))
		return
	}
	token := s.sessions.create()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionDuration.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"message":"Authenticated"}`))
}

// handleAuthLogout revokes the session cookie.
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.revoke(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:   sessionCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/login.html", http.StatusFound)
}

// --- CORS middleware (for dev/API access) ------------------------------------

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- WebSocket hub -----------------------------------------------------------

// wsHub manages connected WebSocket clients and fan-out broadcasts.
type wsHub struct {
	mu      sync.RWMutex
	clients map[*websocket.Conn]struct{}
	bcast   chan []byte
}

func newWSHub() *wsHub {
	return &wsHub{
		clients: make(map[*websocket.Conn]struct{}),
		bcast:   make(chan []byte, 64),
	}
}

func (h *wsHub) register(c *websocket.Conn) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *wsHub) unregister(c *websocket.Conn) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.Close()
}

func (h *wsHub) run() {
	for msg := range h.bcast {
		h.mu.RLock()
		for c := range h.clients {
			_ = c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				// Queue removal; can't modify map under RLock.
				go h.unregister(c)
			}
		}
		h.mu.RUnlock()
	}
}

func (h *wsHub) broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case h.bcast <- b:
	default:
		// Drop if channel full (slow clients).
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.hub.register(conn)

	// Send current status immediately on connect.
	if st, ok := readState(s.runDir); ok {
		s.hub.broadcast(buildPushPayload(st, s.runDir, s.version))
	}

	// Read loop — needed to detect client disconnect.
	go func() {
		defer s.hub.unregister(conn)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
	}()
}

// broadcastLoop pushes a status snapshot to all WebSocket clients every 2s.
func (s *Server) broadcastLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if st, ok := readState(s.runDir); ok {
				s.hub.broadcast(buildPushPayload(st, s.runDir, s.version))
			} else {
				s.hub.broadcast(map[string]any{"running": false})
			}
		}
	}
}

// WsPushPayload is the structure sent over WebSocket every 2 seconds.
type WsPushPayload struct {
	Type       string             `json:"type"` // "status"
	Running    bool               `json:"running"`
	SSID       string             `json:"ssid"`
	Uptime     string             `json:"uptime"`
	UptimeSecs int64              `json:"uptime_secs"`
	VPNActive  bool               `json:"vpn_active"`
	VPNMode    string             `json:"vpn_mode"`
	VPNLatency string             `json:"vpn_latency,omitempty"`
	Clients    []ClientInfo       `json:"clients"`
	Bandwidth  *BandwidthSnapshot `json:"bandwidth,omitempty"`
}

func buildPushPayload(st StateSnapshot, runDir, version string) WsPushPayload {
	var uptimeSecs int64
	var uptime string
	if st.StartTime > 0 {
		uptimeSecs = time.Now().Unix() - st.StartTime
		uptime = formatUptime(uptimeSecs)
	}
	clients := listClients(st.InterfaceAP, runDir)
	bw := CollectBandwidth(st.InterfaceAP, clients)
	return WsPushPayload{
		Type:       "status",
		Running:    hostapdRunning(),
		SSID:       st.SSID,
		Uptime:     uptime,
		UptimeSecs: uptimeSecs,
		VPNActive:  st.VPNActive,
		VPNMode:    st.VPNMode,
		VPNLatency: vpnLatency(st.Uplink),
		Clients:    clients,
		Bandwidth:  bw,
	}
}

// --- State helpers (reads /run/routerd/state) --------------------------------

// StateSnapshot mirrors the subset of main.State needed by the dashboard.
type StateSnapshot struct {
	SSID         string
	InterfaceAP  string
	InterfaceSTA string
	Uplink       string
	Channel      int
	Band         string
	Subnet       string
	VPNMode      string
	VPNActive    bool
	StartTime    int64
}

func readState(runDir string) (StateSnapshot, bool) {
	var s StateSnapshot
	data, err := os.ReadFile(filepath.Join(runDir, "state"))
	if err != nil {
		return s, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "SSID":
			s.SSID = v
		case "INTERFACE_AP":
			s.InterfaceAP = v
		case "INTERFACE_STA":
			s.InterfaceSTA = v
		case "UPLINK":
			s.Uplink = v
		case "CHANNEL":
			fmt.Sscan(v, &s.Channel)
		case "BAND":
			s.Band = v
		case "SUBNET":
			s.Subnet = v
		case "VPN_MODE":
			s.VPNMode = v
		case "VPN_ACTIVE":
			s.VPNActive = v == "true"
		case "START_TIME":
			fmt.Sscan(v, &s.StartTime)
		}
	}
	return s, s.InterfaceAP != ""
}

func hostapdRunning() bool {
	out, err := runCmdOut("pgrep", "-f", "hostapd .*/routerd/hostapd.conf")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// listClients returns connected stations from iw + dnsmasq leases.
func listClients(ap, runDir string) []ClientInfo {
	out, err := runCmdOut("iw", "dev", ap, "station", "dump")
	if err != nil {
		return []ClientInfo{}
	}

	leases := parseDnsmasqLeases(runDir)
	hostnames := parseDnsmasqHostnames(runDir)

	var clients []ClientInfo
	var current *ClientInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Station ") {
			if current != nil {
				clients = append(clients, *current)
			}
			parts := strings.Fields(line)
			mac := ""
			if len(parts) >= 2 {
				mac = strings.ToLower(parts[1])
			}
			current = &ClientInfo{
				MAC:      mac,
				IP:       leases[mac],
				Hostname: hostnames[mac],
			}
			if current.IP == "" {
				current.IP = "(no lease)"
			}
		} else if current != nil {
			if strings.HasPrefix(line, "tx bitrate:") {
				current.TxRate = strings.TrimPrefix(line, "tx bitrate:")
				current.TxRate = strings.TrimSpace(current.TxRate)
			}
			if strings.HasPrefix(line, "signal:") {
				fmt.Sscan(strings.TrimPrefix(strings.TrimSpace(line), "signal:"), &current.Signal)
			}
		}
	}
	if current != nil {
		clients = append(clients, *current)
	}
	if clients == nil {
		return []ClientInfo{}
	}
	return clients
}

func parseDnsmasqLeases(runDir string) map[string]string {
	m := make(map[string]string)
	for _, path := range []string{
		filepath.Join(runDir, "dnsmasq.leases"),
		"/var/lib/misc/dnsmasq.leases",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) >= 3 {
				m[strings.ToLower(f[1])] = f[2]
			}
		}
		break
	}
	return m
}

func parseDnsmasqHostnames(runDir string) map[string]string {
	m := make(map[string]string)
	for _, path := range []string{
		filepath.Join(runDir, "dnsmasq.leases"),
		"/var/lib/misc/dnsmasq.leases",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			// Format: <expiry> <mac> <ip> <hostname> <client-id>
			if len(f) >= 4 && f[3] != "*" {
				m[strings.ToLower(f[1])] = f[3]
			}
		}
		break
	}
	return m
}

// runCmdOut runs a command and returns combined stdout+stderr.
func runCmdOut(name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runCmdDir runs a command in a specific directory.
func runCmdDir(dir, name string, args ...string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runSystemctl executes a systemctl command.
func runSystemctl(action, unit string) error {
	_, err := runCmdOut("systemctl", action, unit)
	return err
}

// --- Log tail helper ---------------------------------------------------------

func tailLogLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// --- IP / net helpers --------------------------------------------------------

// LocalSubnet returns the subnet CIDR of the given interface.
func LocalSubnet(iface string) string {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return ""
	}
	addrs, err := ifi.Addrs()
	if err != nil || len(addrs) == 0 {
		return ""
	}
	return addrs[0].String()
}
