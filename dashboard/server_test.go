package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- test helpers ------------------------------------------------------------

// newTestServer creates a Server backed by a temporary directory.
// password may be "" for no-auth mode.
func newTestServer(t *testing.T, password string) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	// Write a minimal config file.
	cfgPath := filepath.Join(dir, "routerd.conf")
	if err := os.WriteFile(cfgPath, []byte("SSID=test\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	vpnPath := filepath.Join(dir, "vpn.conf")
	srv := NewServer(cfgPath, vpnPath, dir, "0.0.0-test", "0.0.0.0", password, 18080)
	return srv, dir
}

// buildHandler wraps a server's full middleware stack (security headers + auth + cors).
func buildHandler(s *Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/auth/logout", s.handleAuthLogout)
	mux.Handle("/", http.FileServer(http.FS(staticFiles)))
	s.registerAPIRoutes(mux)
	return securityHeadersMiddleware(s.authMiddleware(corsMiddleware(mux)))
}

// postJSON sends a POST request with a JSON body to handler h.
func postJSON(t *testing.T, h http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// getReq sends a GET request to handler h.
func getReq(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// --- TestHandleAuthLogin_Success ---------------------------------------------

func TestHandleAuthLogin_Success(t *testing.T) {
	srv, _ := newTestServer(t, "supersecret")
	h := buildHandler(srv)

	rr := postJSON(t, h, "/auth/login", map[string]string{"password": "supersecret"})

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var resp ActionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected ok=true, got false: %s", resp.Message)
	}

	// A session cookie must be set.
	cookies := rr.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == sessionCookieName {
			found = true
			if !c.HttpOnly {
				t.Error("session cookie must be HttpOnly")
			}
			if c.Value == "" {
				t.Error("session cookie value is empty")
			}
		}
	}
	if !found {
		t.Errorf("session cookie %q not set; cookies: %v", sessionCookieName, cookies)
	}
}

// --- TestHandleAuthLogin_WrongPassword ---------------------------------------

func TestHandleAuthLogin_WrongPassword(t *testing.T) {
	srv, _ := newTestServer(t, "correcthorse")
	h := buildHandler(srv)

	rr := postJSON(t, h, "/auth/login", map[string]string{"password": "wrongpassword"})

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var resp ActionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OK {
		t.Error("expected ok=false for wrong password")
	}

	// No session cookie should be set.
	for _, c := range rr.Result().Cookies() {
		if c.Name == sessionCookieName {
			t.Errorf("session cookie must NOT be set on failed login, got: %v", c)
		}
	}
}

// --- TestHandleAuthLogin_BruteForce ------------------------------------------

// TestHandleAuthLogin_BruteForce verifies that after maxLoginAttempts failed
// attempts the server returns 429 Too Many Requests.
func TestHandleAuthLogin_BruteForce(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	h := buildHandler(srv)

	// Exhaust allowed attempts (maxLoginAttempts = 5).
	for i := 0; i < maxLoginAttempts; i++ {
		rr := postJSON(t, h, "/auth/login", map[string]string{"password": "wrong"})
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, rr.Code)
		}
	}

	// Next attempt must be blocked.
	rr := postJSON(t, h, "/auth/login", map[string]string{"password": "wrong"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after brute-force lock, got %d; body: %s", rr.Code, rr.Body.String())
	}

	// Even the correct password should be blocked while locked.
	rr = postJSON(t, h, "/auth/login", map[string]string{"password": "secret"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for locked IP even with correct password, got %d", rr.Code)
	}

	retryAfter := rr.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

// --- TestHandleStatus_NotRunning ---------------------------------------------

// TestHandleStatus_NotRunning verifies /api/status returns running=false when
// the state file does not exist.
func TestHandleStatus_NotRunning(t *testing.T) {
	srv, _ := newTestServer(t, "") // no auth
	h := buildHandler(srv)

	rr := getReq(t, h, "/api/status")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var resp StatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Running {
		t.Error("expected running=false when no state file exists")
	}
	if resp.Version == "" {
		t.Error("expected non-empty version in status response")
	}
}

// --- TestHandleConfig_Get ----------------------------------------------------

// TestHandleConfig_Get verifies GET /api/config returns the raw config content
// and a parsed key-value map.
func TestHandleConfig_Get(t *testing.T) {
	srv, dir := newTestServer(t, "")
	cfgContent := "SSID=mynet\nPASSWORD=secret12\n"
	if err := os.WriteFile(filepath.Join(dir, "routerd.conf"), []byte(cfgContent), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// Point the server at the correct file.
	srv.configPath = filepath.Join(dir, "routerd.conf")
	h := buildHandler(srv)

	rr := getReq(t, h, "/api/config")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", rr.Code, rr.Body.String())
	}

	var resp ConfigResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Raw != cfgContent {
		t.Errorf("raw config = %q, want %q", resp.Raw, cfgContent)
	}
	if resp.Parsed["SSID"] != "mynet" {
		t.Errorf("parsed SSID = %q, want 'mynet'", resp.Parsed["SSID"])
	}
	if resp.Parsed["PASSWORD"] != "secret12" {
		t.Errorf("parsed PASSWORD = %q, want 'secret12'", resp.Parsed["PASSWORD"])
	}
}

// --- TestSecurityHeaders -----------------------------------------------------

// TestSecurityHeaders verifies that every response carries the required
// Content-Security-Policy and X-Content-Type-Options headers.
func TestSecurityHeaders(t *testing.T) {
	srv, _ := newTestServer(t, "")
	h := buildHandler(srv)

	paths := []string{"/api/status", "/api/config", "/api/clients"}
	for _, path := range paths {
		rr := getReq(t, h, path)
		csp := rr.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Errorf("path %s: missing Content-Security-Policy header", path)
		}
		if !strings.Contains(csp, "default-src") {
			t.Errorf("path %s: CSP missing default-src directive: %q", path, csp)
		}
		xcto := rr.Header().Get("X-Content-Type-Options")
		if xcto != "nosniff" {
			t.Errorf("path %s: X-Content-Type-Options = %q, want 'nosniff'", path, xcto)
		}
		xfo := rr.Header().Get("X-Frame-Options")
		if xfo == "" {
			t.Errorf("path %s: missing X-Frame-Options header", path)
		}
	}
}

// --- TestHandleAuthLogin_NoPassword ------------------------------------------

// TestHandleAuthLogin_NoPassword verifies that when no password is configured
// the API endpoints are accessible without authentication.
func TestHandleAuthLogin_NoPassword(t *testing.T) {
	srv, _ := newTestServer(t, "") // no auth
	h := buildHandler(srv)

	rr := getReq(t, h, "/api/status")
	if rr.Code != http.StatusOK {
		t.Fatalf("no-auth mode: expected 200 for /api/status, got %d", rr.Code)
	}
}

// --- TestHandleAuthLogout ----------------------------------------------------

func TestHandleAuthLogout(t *testing.T) {
	srv, _ := newTestServer(t, "pass1234")
	h := buildHandler(srv)

	// First, log in to get a session cookie.
	loginRR := postJSON(t, h, "/auth/login", map[string]string{"password": "pass1234"})
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login failed: %d", loginRR.Code)
	}
	var sessionCookie *http.Cookie
	for _, c := range loginRR.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie after login")
	}

	// Verify the session is valid by accessing a protected endpoint.
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.AddCookie(sessionCookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("with session: expected 200, got %d", rr.Code)
	}

	// Logout.
	logoutReq := httptest.NewRequest(http.MethodGet, "/auth/logout", nil)
	logoutReq.AddCookie(sessionCookie)
	logoutRR := httptest.NewRecorder()
	h.ServeHTTP(logoutRR, logoutReq)
	// Logout should redirect to login page.
	if logoutRR.Code != http.StatusFound {
		t.Fatalf("logout: expected 302, got %d", logoutRR.Code)
	}

	// Session should now be invalid.
	req2 := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req2.AddCookie(sessionCookie)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("after logout: expected 401, got %d", rr2.Code)
	}
}

// --- TestHandleClients_NotRunning --------------------------------------------

func TestHandleClients_NotRunning(t *testing.T) {
	srv, _ := newTestServer(t, "")
	h := buildHandler(srv)

	rr := getReq(t, h, "/api/clients")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp ClientsResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 0 {
		t.Errorf("expected count=0 when not running, got %d", resp.Count)
	}
}

// --- TestHandleConfig_MethodNotAllowed ---------------------------------------

func TestHandleConfig_MethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t, "")
	h := buildHandler(srv)

	req := httptest.NewRequest(http.MethodDelete, "/api/config", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for DELETE /api/config, got %d", rr.Code)
	}
}

// --- TestHandleStatus_WithState ----------------------------------------------

// TestHandleStatus_WithState writes a fake state file and verifies the
// status endpoint returns its values (running may still be false since
// hostapd isn't actually running in test environment).
func TestHandleStatus_WithState(t *testing.T) {
	srv, dir := newTestServer(t, "")
	state := "SSID=myap\nINTERFACE_AP=ap0\nINTERFACE_STA=wlan0\n" +
		"UPLINK=wlan0\nCHANNEL=6\nBAND=g\nSUBNET=192.168.50.0/24\n" +
		"VPN_MODE=wireguard\nVPN_ACTIVE=false\nSTART_TIME=1000000\n"
	if err := os.WriteFile(filepath.Join(dir, "state"), []byte(state), 0644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	h := buildHandler(srv)

	rr := getReq(t, h, "/api/status")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp StatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SSID != "myap" {
		t.Errorf("SSID = %q, want 'myap'", resp.SSID)
	}
	if resp.Channel != 6 {
		t.Errorf("Channel = %d, want 6", resp.Channel)
	}
	if resp.Subnet != "192.168.50.0/24" {
		t.Errorf("Subnet = %q, want '192.168.50.0/24'", resp.Subnet)
	}
}
