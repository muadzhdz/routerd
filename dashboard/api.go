package dashboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// --- JSON response types -----------------------------------------------------

// StatusResponse is the payload for GET /api/status.
type StatusResponse struct {
	Running     bool   `json:"running"`
	SSID        string `json:"ssid"`
	Channel     int    `json:"channel"`
	Band        string `json:"band"`
	APInterface string `json:"ap_interface"`
	STAIface    string `json:"sta_interface"`
	Uplink      string `json:"uplink"`
	Subnet      string `json:"subnet"`
	VPNActive   bool   `json:"vpn_active"`
	VPNMode     string `json:"vpn_mode"`
	VPNEndpoint string `json:"vpn_endpoint,omitempty"`
	VPNLatency  string `json:"vpn_latency,omitempty"`
	Uptime      string `json:"uptime"`
	UptimeSecs  int64  `json:"uptime_secs"`
	Version     string `json:"version"`
}

// ClientInfo represents one connected AP station.
type ClientInfo struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname,omitempty"`
	TxBytes  int64  `json:"tx_bytes"`
	RxBytes  int64  `json:"rx_bytes"`
	TxRate   string `json:"tx_rate,omitempty"`
	Signal   int    `json:"signal,omitempty"`
}

// ClientsResponse is the payload for GET /api/clients.
type ClientsResponse struct {
	Count   int          `json:"count"`
	Clients []ClientInfo `json:"clients"`
}

// ConfigResponse is the payload for GET /api/config.
type ConfigResponse struct {
	Raw    string            `json:"raw"`
	Parsed map[string]string `json:"parsed"`
}

// VPNStatusResponse is the payload for GET /api/vpn.
type VPNStatusResponse struct {
	Active      bool   `json:"active"`
	Mode        string `json:"mode"`
	Interface   string `json:"interface"`
	Endpoint    string `json:"endpoint,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	LastHS      string `json:"last_handshake,omitempty"`
	Latency     string `json:"latency,omitempty"`
	ConfContent string `json:"conf_content,omitempty"`
}

// ActionResponse is a generic success/error wrapper.
type ActionResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// --- Handler registration ----------------------------------------------------

// registerAPIRoutes attaches all /api/* handlers to the given mux.
func (s *Server) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/clients", s.handleClients)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/vpn", s.handleVPN)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/action", s.handleAction)
	mux.HandleFunc("/api/client/kick", s.handleKickClient)
}

// --- /api/status -------------------------------------------------------------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	st, ok := readState(s.runDir)
	if !ok {
		writeJSON(w, StatusResponse{Running: false, Version: s.version})
		return
	}

	running := hostapdRunning()
	uptime := ""
	var uptimeSecs int64
	if st.StartTime > 0 {
		uptimeSecs = time.Now().Unix() - st.StartTime
		uptime = formatUptime(uptimeSecs)
	}

	resp := StatusResponse{
		Running:     running,
		SSID:        st.SSID,
		Channel:     st.Channel,
		Band:        st.Band,
		APInterface: st.InterfaceAP,
		STAIface:    st.InterfaceSTA,
		Uplink:      st.Uplink,
		Subnet:      st.Subnet,
		VPNActive:   st.VPNActive,
		VPNMode:     st.VPNMode,
		Uptime:      uptime,
		UptimeSecs:  uptimeSecs,
		Version:     s.version,
	}

	if st.VPNActive {
		resp.VPNEndpoint = vpnEndpoint(st.Uplink)
		resp.VPNLatency = vpnLatency(st.Uplink)
	}

	writeJSON(w, resp)
}

// --- /api/clients ------------------------------------------------------------

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, ok := readState(s.runDir)
	if !ok {
		writeJSON(w, ClientsResponse{Count: 0, Clients: []ClientInfo{}})
		return
	}

	clients := listClients(st.InterfaceAP, s.runDir)
	writeJSON(w, ClientsResponse{Count: len(clients), Clients: clients})
}

// --- /api/config -------------------------------------------------------------

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleConfigGet(w, r)
	case http.MethodPost:
		s.handleConfigPost(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	raw, err := os.ReadFile(s.configPath)
	if err != nil {
		jsonError(w, fmt.Sprintf("cannot read config: %v", err), http.StatusInternalServerError)
		return
	}
	parsed := parseConfigKV(string(raw))
	writeJSON(w, ConfigResponse{Raw: string(raw), Parsed: parsed})
}

func (s *Server) handleConfigPost(w http.ResponseWriter, r *http.Request) {
	// Rate limit: reject saves within 2 seconds of the last save.
	s.muConfigSave.Lock()
	if !s.lastConfigSave.IsZero() && time.Since(s.lastConfigSave) < 2*time.Second {
		s.muConfigSave.Unlock()
		w.Header().Set("Retry-After", "2")
		jsonError(w, "too many requests — wait 2 seconds between saves", http.StatusTooManyRequests)
		return
	}
	s.muConfigSave.Unlock()

	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		jsonError(w, "config content must not be empty", http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(s.configPath, []byte(body.Content), 0600); err != nil {
		jsonError(w, fmt.Sprintf("cannot write config: %v", err), http.StatusInternalServerError)
		return
	}
	s.muConfigSave.Lock()
	s.lastConfigSave = time.Now()
	s.muConfigSave.Unlock()
	writeJSON(w, ActionResponse{OK: true, Message: "Config saved. Use Reload to apply changes."})
}

// --- /api/vpn ----------------------------------------------------------------

func (s *Server) handleVPN(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleVPNGet(w, r)
	case http.MethodPost:
		s.handleVPNPost(w, r)
	default:
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleVPNGet(w http.ResponseWriter, _ *http.Request) {
	st, ok := readState(s.runDir)
	resp := VPNStatusResponse{}

	if ok && st.VPNActive {
		resp.Active = true
		resp.Mode = st.VPNMode
		resp.Interface = st.Uplink
		resp.Endpoint = vpnEndpoint(st.Uplink)
		resp.Latency = vpnLatency(st.Uplink)
		resp.PublicKey, resp.LastHS = vpnWGInfo(st.Uplink)
	}

	// Always try to read conf file content (redacted keys for security).
	if data, err := os.ReadFile(s.vpnConfPath); err == nil {
		resp.ConfContent = redactVPNConf(string(data))
	}

	writeJSON(w, resp)
}

func (s *Server) handleVPNPost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action  string `json:"action"` // "save_conf"
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	switch body.Action {
	case "save_conf":
		if err := os.WriteFile(s.vpnConfPath, []byte(body.Content), 0600); err != nil {
			jsonError(w, fmt.Sprintf("cannot write vpn conf: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, ActionResponse{OK: true, Message: "VPN config saved. Reload routerd to apply."})
	default:
		jsonError(w, fmt.Sprintf("unknown action %q", body.Action), http.StatusBadRequest)
	}
}

// --- /api/logs ---------------------------------------------------------------

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	source := r.URL.Query().Get("source") // "hostapd", "dnsmasq", or "" (both)
	lines := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 && n <= 5000 {
		lines = n
	}

	var entries []string
	if source == "" || source == "hostapd" {
		entries = append(entries, tailLog(filepath.Join(s.runDir, "hostapd.log"), "hostapd", lines)...)
	}
	if source == "" || source == "dnsmasq" {
		entries = append(entries, tailLog(filepath.Join(s.runDir, "dnsmasq.log"), "dnsmasq", lines)...)
	}

	writeJSON(w, map[string]any{"lines": entries})
}

// --- /api/action -------------------------------------------------------------

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Action string `json:"action"` // "reload", "stop", "start"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	validActions := map[string]bool{"reload": true, "stop": true, "start": true}
	if !validActions[body.Action] {
		jsonError(w, "invalid action", http.StatusBadRequest)
		return
	}
	switch body.Action {
	case "reload":
		if err := runSystemctl("reload", "routerd"); err != nil {
			jsonError(w, fmt.Sprintf("reload failed: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, ActionResponse{OK: true, Message: "Reload signal sent to routerd."})
	case "stop":
		if err := runSystemctl("stop", "routerd"); err != nil {
			jsonError(w, fmt.Sprintf("stop failed: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, ActionResponse{OK: true, Message: "Stop signal sent to routerd."})
	case "start":
		if err := runSystemctl("start", "routerd"); err != nil {
			jsonError(w, fmt.Sprintf("start failed: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, ActionResponse{OK: true, Message: "Start signal sent to routerd."})
	default:
		jsonError(w, fmt.Sprintf("unknown action %q", body.Action), http.StatusBadRequest)
	}
}

// --- /api/client/kick --------------------------------------------------------

func (s *Server) handleKickClient(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MAC == "" {
		jsonError(w, "invalid JSON body or missing mac", http.StatusBadRequest)
		return
	}
	st, ok := readState(s.runDir)
	if !ok {
		jsonError(w, "routerd not running", http.StatusServiceUnavailable)
		return
	}
	out, err := runCmdOut("hostapd_cli", "-i", st.InterfaceAP, "deauthenticate", body.MAC)
	if err != nil {
		jsonError(w, fmt.Sprintf("kick failed: %s", strings.TrimSpace(out)), http.StatusInternalServerError)
		return
	}
	writeJSON(w, ActionResponse{OK: true, Message: fmt.Sprintf("Client %s deauthenticated.", body.MAC)})
}

// --- Helpers -----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(ActionResponse{OK: false, Message: msg})
}

func parseConfigKV(raw string) map[string]string {
	m := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return m
}

func tailLog(path, prefix string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, fmt.Sprintf("[%s] %s", prefix, sc.Text()))
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func formatUptime(secs int64) string {
	if secs < 0 {
		secs = 0
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	s := secs % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// redactVPNConf returns the WireGuard conf with PrivateKey value masked.
func redactVPNConf(content string) string {
	var out strings.Builder
	for _, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(lower, "privatekey") {
			kv := strings.SplitN(line, "=", 2)
			if len(kv) == 2 {
				out.WriteString(kv[0] + "= [REDACTED]\n")
				continue
			}
		}
		out.WriteString(line + "\n")
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// vpnLatency pings the WireGuard endpoint and returns RTT string.
func vpnLatency(iface string) string {
	out, err := runCmdOut("wg", "show", iface, "endpoints")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] != "(none)" {
			// Extract just the IP part (strip port).
			host := fields[1]
			if idx := strings.LastIndex(host, ":"); idx > 0 {
				host = host[:idx]
			}
			// Remove brackets from IPv6.
			host = strings.Trim(host, "[]")
			start := time.Now()
			pingOut, _ := runCmdOut("ping", "-c", "1", "-W", "2", host)
			elapsed := time.Since(start).Milliseconds()
			if strings.Contains(pingOut, "1 received") || strings.Contains(pingOut, "1 packets received") {
				return fmt.Sprintf("%dms", elapsed)
			}
		}
	}
	return ""
}

// vpnEndpoint returns the remote endpoint of the first WireGuard peer.
func vpnEndpoint(iface string) string {
	out, err := runCmdOut("wg", "show", iface, "endpoints")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] != "(none)" {
			return fields[1]
		}
	}
	return ""
}

// vpnWGInfo returns the public key and last handshake time.
func vpnWGInfo(iface string) (pubKey, lastHS string) {
	if out, err := runCmdOut("wg", "show", iface, "public-key"); err == nil {
		pubKey = strings.TrimSpace(out)
	}
	if out, err := runCmdOut("wg", "show", iface, "latest-handshakes"); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if ts, err := strconv.ParseInt(fields[1], 10, 64); err == nil && ts > 0 {
					ago := time.Since(time.Unix(ts, 0)).Round(time.Second)
					lastHS = fmt.Sprintf("%s ago", ago)
				}
				break
			}
		}
	}
	return
}
