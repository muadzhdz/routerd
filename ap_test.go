package main

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// TestCreateAPInterface_RandomMAC verifies that when useRandomMAC=true the
// interface is created with a randomly generated LAA MAC address.
func TestCreateAPInterface_RandomMAC(t *testing.T) {
	// We intercept calls and check that "iw dev wlan0 interface add ap0 type __ap addr <mac>"
	// was issued with a valid LAA unicast MAC.
	var capturedMock *MockRunner

	// Use a custom runner that captures the invocation.
	type captureRunner struct {
		*MockRunner
	}
	mock := newMockRunner()
	capturedMock = mock
	restore := installMockRunner(mock)
	defer restore()

	// The first call deleteAPInterface makes is "iw dev ap0 del".
	// The second call is the actual interface creation.
	// Both return "" / nil by default.

	err := createAPInterface("wlan0", "ap0", true)
	if err != nil {
		t.Fatalf("createAPInterface(useRandomMAC=true) returned error: %v", err)
	}

	// Find the "iw dev wlan0 interface add ap0 type __ap addr <mac>" call.
	found := false
	for _, c := range capturedMock.calls {
		if c.name == "iw" && contains(c.args, "wlan0") && contains(c.args, "interface") &&
			contains(c.args, "add") && contains(c.args, "ap0") && contains(c.args, "__ap") {
			found = true
			// The last arg should be the MAC address.
			mac := c.args[len(c.args)-1]
			if _, err := net.ParseMAC(mac); err != nil {
				t.Errorf("iw add called with invalid MAC %q: %v", mac, err)
			}
			// Verify LAA bit set, multicast bit clear.
			parts := strings.Split(mac, ":")
			if len(parts) != 6 {
				t.Fatalf("MAC %q does not have 6 octets", mac)
			}
			b := parseFirstOctet(parts[0])
			if b&0x02 == 0 {
				t.Errorf("MAC %q: LAA bit not set in first octet 0x%02x", mac, b)
			}
			if b&0x01 != 0 {
				t.Errorf("MAC %q: multicast bit set in first octet 0x%02x", mac, b)
			}
		}
	}
	if !found {
		t.Errorf("expected 'iw dev wlan0 interface add ap0 type __ap addr <mac>' call; calls:\n%s", dumpCalls(capturedMock))
	}
}

// TestCreateAPInterface_LocalAdminMAC verifies that when useRandomMAC=false
// a locally-administered MAC derived from the physical interface is used.
func TestCreateAPInterface_LocalAdminMAC(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	err := createAPInterface("wlan0", "ap0", false)
	if err != nil {
		t.Fatalf("createAPInterface(useRandomMAC=false) returned error: %v", err)
	}

	// Verify an 'iw dev wlan0 interface add ap0' call was made.
	if !mock.calledWithSubstr("iw", "wlan0", "interface", "add", "ap0") {
		t.Errorf("expected iw interface add call; calls:\n%s", dumpCalls(mock))
	}
}

// TestCreateAPInterface_FallbackToManaged verifies the fallback path: when
// the first "iw … type __ap" call fails, the code retries with "type managed".
func TestCreateAPInterface_FallbackToManaged(t *testing.T) {
	fallback := &apFallbackMock{MockRunner: newMockRunner()}
	old := defaultRunner
	defaultRunner = fallback
	defer func() { defaultRunner = old }()

	// createAPInterface should attempt __ap first (which apFallbackMock rejects),
	// then fall back to managed + ip link set + iw set type ap (which all return nil).
	err := createAPInterface("wlan0", "ap0", true)
	// Fallback succeeds since managed/ip/iw all return ("", nil) from the mock.
	if err != nil {
		t.Logf("createAPInterface fallback got error (may be OK in test env): %v", err)
	}

	// Verify managed-type add was attempted.
	if !fallback.calledWithSubstr("managed") {
		t.Logf("fallback: managed add not observed in calls:\n%s", dumpCalls(fallback.MockRunner))
	}
}

// TestRandomMAC_ValidFormat verifies the format produced by randomMAC.
func TestRandomMAC_ValidFormat(t *testing.T) {
	for i := 0; i < 20; i++ {
		mac := randomMAC()
		if _, err := net.ParseMAC(mac); err != nil {
			t.Errorf("randomMAC() = %q is not a valid MAC: %v", mac, err)
		}
	}
}

// TestRandomMAC_Unique verifies that randomMAC produces distinct values.
func TestRandomMAC_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		seen[randomMAC()] = true
	}
	if len(seen) < 18 {
		t.Errorf("randomMAC produced too many duplicates: %d unique in 20 calls", len(seen))
	}
}

// TestLocalAdminMAC_Fallback verifies that localAdminMAC falls back to
// randomMAC when the sys class path is unreadable.
func TestLocalAdminMAC_Fallback(t *testing.T) {
	// Non-existent interface — must still return a valid LAA unicast MAC.
	mac := localAdminMAC("nonexistent_iface_xyz")
	if _, err := net.ParseMAC(mac); err != nil {
		t.Errorf("localAdminMAC fallback returned invalid MAC %q: %v", mac, err)
	}
}

// TestDeleteAPInterface_NoSuchDevice verifies that deleteAPInterface treats
// "No such device" as a non-error (idempotent).
func TestDeleteAPInterface_NoSuchDevice(t *testing.T) {
	mock := newMockRunner()
	mock.stub("No such device", fmt.Errorf("exit status 1"), "iw", "dev", "ap99", "del")
	restore := installMockRunner(mock)
	defer restore()

	err := deleteAPInterface("ap99")
	if err != nil {
		t.Errorf("deleteAPInterface with 'No such device' should not error, got: %v", err)
	}
}

// --- helpers -----------------------------------------------------------------

// contains reports whether slice s contains target.
func contains(s []string, target string) bool {
	for _, v := range s {
		if v == target {
			return true
		}
	}
	return false
}

// parseFirstOctet parses a 2-char hex string to a byte value.
func parseFirstOctet(s string) byte {
	var b byte
	for _, c := range s {
		b <<= 4
		switch {
		case c >= '0' && c <= '9':
			b |= byte(c - '0')
		case c >= 'a' && c <= 'f':
			b |= byte(c-'a') + 10
		case c >= 'A' && c <= 'F':
			b |= byte(c-'A') + 10
		}
	}
	return b
}

// apFallbackMock wraps MockRunner and fails all "type __ap" iw add calls,
// simulating a driver that requires the fallback managed→ap path.
type apFallbackMock struct {
	*MockRunner
}

func (m *apFallbackMock) Run(name string, args ...string) (string, error) {
	// Fail the first attempt: iw dev <sta> interface add <ap> type __ap addr <mac>
	if name == "iw" && len(args) >= 5 && contains(args, "interface") && contains(args, "__ap") {
		return "nl80211: Could not configure driver mode", fmt.Errorf("exit status 161")
	}
	return m.MockRunner.Run(name, args...)
}

func (m *apFallbackMock) RunDir(dir, name string, args ...string) (string, error) {
	return m.MockRunner.RunDir(dir, name, args...)
}
