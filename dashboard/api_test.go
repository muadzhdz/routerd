package dashboard

import (
	"strings"
	"testing"
)

// --- formatUptime ------------------------------------------------------------

func TestFormatUptime_Seconds(t *testing.T) {
	if got := formatUptime(45); got != "45s" {
		t.Errorf("formatUptime(45) = %q, want 45s", got)
	}
}

func TestFormatUptime_Minutes(t *testing.T) {
	if got := formatUptime(125); got != "2m 5s" {
		t.Errorf("formatUptime(125) = %q, want 2m 5s", got)
	}
}

func TestFormatUptime_Hours(t *testing.T) {
	if got := formatUptime(3723); got != "1h 2m 3s" {
		t.Errorf("formatUptime(3723) = %q, want 1h 2m 3s", got)
	}
}

func TestFormatUptime_Zero(t *testing.T) {
	if got := formatUptime(0); got != "0s" {
		t.Errorf("formatUptime(0) = %q, want 0s", got)
	}
}

func TestFormatUptime_Negative(t *testing.T) {
	// Negative input clamps to 0s.
	if got := formatUptime(-10); got != "0s" {
		t.Errorf("formatUptime(-10) = %q, want 0s", got)
	}
}

func TestFormatUptime_ExactHour(t *testing.T) {
	if got := formatUptime(3600); got != "1h 0m 0s" {
		t.Errorf("formatUptime(3600) = %q, want 1h 0m 0s", got)
	}
}

// --- redactVPNConf -----------------------------------------------------------

func TestRedactVPNConf_RedactsKey(t *testing.T) {
	input := "[Interface]\nPrivateKey = abc123secretkey\nAddress = 10.2.0.2/32\n"
	out := redactVPNConf(input)

	if strings.Contains(out, "abc123secretkey") {
		t.Errorf("redactVPNConf did not redact PrivateKey value; got:\n%s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("redactVPNConf missing [REDACTED] marker; got:\n%s", out)
	}
	// Non-sensitive lines must be preserved.
	if !strings.Contains(out, "Address = 10.2.0.2/32") {
		t.Errorf("redactVPNConf removed non-sensitive line; got:\n%s", out)
	}
}

func TestRedactVPNConf_PreservesOtherLines(t *testing.T) {
	input := "[Peer]\nPublicKey = pubkey123\nEndpoint = 1.2.3.4:51820\n"
	out := redactVPNConf(input)

	// PublicKey (peer side) is NOT a PrivateKey — must be kept.
	if !strings.Contains(out, "pubkey123") {
		t.Errorf("redactVPNConf incorrectly redacted PublicKey; got:\n%s", out)
	}
	if !strings.Contains(out, "Endpoint") {
		t.Errorf("redactVPNConf removed Endpoint; got:\n%s", out)
	}
}

func TestRedactVPNConf_CaseInsensitive(t *testing.T) {
	// PRIVATEKEY in uppercase must also be redacted.
	input := "PRIVATEKEY = uppercasesecret\n"
	out := redactVPNConf(input)
	if strings.Contains(out, "uppercasesecret") {
		t.Errorf("redactVPNConf did not redact uppercase PRIVATEKEY; got:\n%s", out)
	}
}

func TestRedactVPNConf_CommentedKeyNotRedacted(t *testing.T) {
	// A commented-out PrivateKey line should be preserved as-is (it's already masked).
	input := "# PrivateKey = commentedoutkey\n"
	out := redactVPNConf(input)
	// Commented lines should pass through unchanged.
	if !strings.Contains(out, "commentedoutkey") {
		t.Errorf("redactVPNConf incorrectly redacted commented key; got:\n%s", out)
	}
}

func TestRedactVPNConf_Empty(t *testing.T) {
	out := redactVPNConf("")
	if out != "" {
		t.Errorf("redactVPNConf('') = %q, want empty", out)
	}
}

// --- humanBytes --------------------------------------------------------------

func TestHumanBytes_Bps(t *testing.T) {
	if got := humanBytes(500); !strings.Contains(got, "bps") {
		t.Errorf("humanBytes(500) = %q, expected bps suffix", got)
	}
}

func TestHumanBytes_Kbps(t *testing.T) {
	got := humanBytes(1_500)
	if !strings.Contains(got, "Kbps") {
		t.Errorf("humanBytes(1500) = %q, expected Kbps", got)
	}
}

func TestHumanBytes_Mbps(t *testing.T) {
	got := humanBytes(5_000_000)
	if !strings.Contains(got, "Mbps") {
		t.Errorf("humanBytes(5000000) = %q, expected Mbps", got)
	}
}

func TestHumanBytes_Zero(t *testing.T) {
	if got := humanBytes(0); got != "0 bps" {
		t.Errorf("humanBytes(0) = %q, want '0 bps'", got)
	}
}

func TestHumanBytes_Threshold(t *testing.T) {
	// Exactly 1000 bps is the Kbps threshold.
	got := humanBytes(1_000)
	if !strings.Contains(got, "Kbps") {
		t.Errorf("humanBytes(1000) = %q, expected Kbps at boundary", got)
	}
	// Exactly 1,000,000 is the Mbps threshold.
	got = humanBytes(1_000_000)
	if !strings.Contains(got, "Mbps") {
		t.Errorf("humanBytes(1000000) = %q, expected Mbps at boundary", got)
	}
}

// --- parseConfigKV -----------------------------------------------------------

func TestParseConfigKV_Basic(t *testing.T) {
	raw := "SSID=mynet\nPASSWORD=secret\nENABLE_VPN=true\n"
	m := parseConfigKV(raw)

	if m["SSID"] != "mynet" {
		t.Errorf("SSID = %q, want mynet", m["SSID"])
	}
	if m["PASSWORD"] != "secret" {
		t.Errorf("PASSWORD = %q, want secret", m["PASSWORD"])
	}
	if m["ENABLE_VPN"] != "true" {
		t.Errorf("ENABLE_VPN = %q, want true", m["ENABLE_VPN"])
	}
}

func TestParseConfigKV_SkipsComments(t *testing.T) {
	raw := "# This is a comment\nSSID=mynet\n# PASSWORD=hidden\n"
	m := parseConfigKV(raw)

	if _, ok := m["# This is a comment"]; ok {
		t.Error("comment line should not appear as key")
	}
	if _, ok := m["# PASSWORD"]; ok {
		t.Error("commented PASSWORD should not appear as key")
	}
	if m["SSID"] != "mynet" {
		t.Errorf("SSID = %q, want mynet", m["SSID"])
	}
}

func TestParseConfigKV_SkipsBlankLines(t *testing.T) {
	raw := "\n\nSSID=net\n\n"
	m := parseConfigKV(raw)
	if len(m) != 1 {
		t.Errorf("expected 1 key, got %d: %v", len(m), m)
	}
}

func TestParseConfigKV_Empty(t *testing.T) {
	m := parseConfigKV("")
	if len(m) != 0 {
		t.Errorf("expected empty map for empty input, got %v", m)
	}
}

func TestParseConfigKV_ValueWithEquals(t *testing.T) {
	// Value containing '=' must keep everything after first '='.
	raw := "KEY=val=ue\n"
	m := parseConfigKV(raw)
	if m["KEY"] != "val=ue" {
		t.Errorf("KEY = %q, want 'val=ue'", m["KEY"])
	}
}

func TestParseConfigKV_Whitespace(t *testing.T) {
	raw := "  SSID = routerd  \n"
	m := parseConfigKV(raw)
	if m["SSID"] != "routerd" {
		t.Errorf("SSID with whitespace = %q, want 'routerd'", m["SSID"])
	}
}
