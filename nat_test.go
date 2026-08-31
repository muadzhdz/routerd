package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// --- MockRunner --------------------------------------------------------------

// cmdCall records a single invocation of Run or RunDir.
type cmdCall struct {
	dir  string // empty for Run, non-empty for RunDir
	name string
	args []string
}

// MockRunner records every command invocation and returns configurable outputs.
// It is NOT thread-safe; it is intended for single-goroutine unit tests only.
type MockRunner struct {
	calls   []cmdCall
	outputs map[string]string // key: joined "name arg0 arg1 …" → output
	errors  map[string]error  // key: joined "name arg0 arg1 …" → error
}

func newMockRunner() *MockRunner {
	return &MockRunner{
		outputs: make(map[string]string),
		errors:  make(map[string]error),
	}
}

// key builds a look-up key from name+args (dir is ignored for simplicity).
func (m *MockRunner) key(name string, args []string) string {
	return strings.Join(append([]string{name}, args...), " ")
}

// stub registers a canned output/error for a specific command invocation.
func (m *MockRunner) stub(output string, err error, name string, args ...string) {
	k := m.key(name, args)
	m.outputs[k] = output
	if err != nil {
		m.errors[k] = err
	}
}

// Run records the call and returns the canned response (or "" / nil).
func (m *MockRunner) Run(name string, args ...string) (string, error) {
	m.calls = append(m.calls, cmdCall{name: name, args: args})
	k := m.key(name, args)
	return m.outputs[k], m.errors[k]
}

// RunDir records the call (including dir) and returns the canned response.
func (m *MockRunner) RunDir(dir, name string, args ...string) (string, error) {
	m.calls = append(m.calls, cmdCall{dir: dir, name: name, args: args})
	k := m.key(name, args)
	return m.outputs[k], m.errors[k]
}

// calledWith returns true if name+args appears anywhere in the recorded calls.
func (m *MockRunner) calledWith(name string, args ...string) bool {
	for _, c := range m.calls {
		if c.name == name && len(c.args) == len(args) {
			match := true
			for i, a := range args {
				if c.args[i] != a {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// calledWithSubstr returns true if any call whose joined "name args" string
// contains ALL of the provided substrings.
func (m *MockRunner) calledWithSubstr(substrings ...string) bool {
	for _, c := range m.calls {
		full := strings.Join(append([]string{c.name}, c.args...), " ")
		all := true
		for _, s := range substrings {
			if !strings.Contains(full, s) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// --- helpers -----------------------------------------------------------------

// installMockRunner swaps in mock as defaultRunner and returns a restore func.
func installMockRunner(mock *MockRunner) func() {
	old := defaultRunner
	defaultRunner = mock
	return func() { defaultRunner = old }
}

// natRunDir creates a temp dir with a dummy ip_forward.orig file.
func natRunDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "ip_forward.orig"), []byte("0"), 0600)
	return dir
}

// withMockNFT installs a mock runner, forces NFT detection to false (legacy
// iptables mode), and returns a cleanup function that restores both.
// It resets the lazy sync.Once so the mock's iptables --version response
// is used when iptArgs() first calls isNFT() during the test.
func withMockNFT(mock *MockRunner) func() {
	restoreRunner := installMockRunner(mock)
	// Reset the lazy singleton so isNFT() re-runs with our mock in place.
	_isNFTOnce = sync.Once{}
	_isNFT = false
	// Stub iptables --version to return legacy (non-nft) output so -w is kept.
	mock.stub("iptables v1.8 (legacy)", nil, "iptables", "--version")
	return func() {
		// Reset again on cleanup so subsequent tests start fresh.
		_isNFTOnce = sync.Once{}
		_isNFT = false
		restoreRunner()
	}
}

// dumpCalls formats recorded calls for test failure messages.
func dumpCalls(m *MockRunner) string {
	var sb strings.Builder
	for i, c := range m.calls {
		fmt.Fprintf(&sb, "  [%d] %s %s\n", i, c.name, strings.Join(c.args, " "))
	}
	return sb.String()
}

// --- TestEnableNAT_ChainNames ------------------------------------------------

// TestEnableNAT_ChainNames verifies all chain names carry the ROUTERD_ prefix.
func TestEnableNAT_ChainNames(t *testing.T) {
	chains := []string{natPostChain, natPreChain, fwdChain, inputChain, mangleChain}
	for _, ch := range chains {
		if !strings.HasPrefix(ch, "ROUTERD_") {
			t.Errorf("chain %q does not have ROUTERD_ prefix", ch)
		}
	}
}

// --- TestEnableNAT_Masquerade (setupMasquerade) ------------------------------

// TestEnableNAT_BasicMasquerade verifies setupMasquerade creates ROUTERD_POST,
// adds a MASQUERADE rule, and attaches the chain to POSTROUTING.
func TestEnableNAT_BasicMasquerade(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	if err := setupMasquerade("wlan0"); err != nil {
		t.Fatalf("setupMasquerade error: %v", err)
	}

	if !mock.calledWithSubstr("iptables", "-N", natPostChain) {
		t.Errorf("expected iptables -N %s; calls:\n%s", natPostChain, dumpCalls(mock))
	}
	if !mock.calledWithSubstr("iptables", "MASQUERADE") {
		t.Errorf("expected MASQUERADE rule; calls:\n%s", dumpCalls(mock))
	}
	if !mock.calledWithSubstr("iptables", "POSTROUTING", "-j", natPostChain) {
		t.Errorf("expected -I POSTROUTING -j %s; calls:\n%s", natPostChain, dumpCalls(mock))
	}
	// Uplink wlan0 should appear in the MASQUERADE rule
	if !mock.calledWithSubstr("iptables", "-o", "wlan0", "-j", "MASQUERADE") {
		t.Errorf("expected -o wlan0 -j MASQUERADE; calls:\n%s", dumpCalls(mock))
	}
}

// --- TestEnableNAT_ForwardRules (setupForwardRules) --------------------------

func TestEnableNAT_ForwardRules(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	if err := setupForwardRules("ap0", "wlan0", false, false); err != nil {
		t.Fatalf("setupForwardRules error: %v", err)
	}

	// ROUTERD_FWD chain must be created.
	if !mock.calledWithSubstr("iptables", "-N", fwdChain) {
		t.Errorf("expected iptables -N %s", fwdChain)
	}
	// Forward ACCEPT: ap0 → wlan0
	if !mock.calledWithSubstr(fwdChain, "-i", "ap0", "-o", "wlan0", "-j", "ACCEPT") {
		t.Errorf("expected ACCEPT forward rule ap0->wlan0; calls:\n%s", dumpCalls(mock))
	}
	// Return traffic RELATED,ESTABLISHED
	if !mock.calledWithSubstr(fwdChain, "RELATED,ESTABLISHED", "-j", "ACCEPT") {
		t.Errorf("expected RELATED,ESTABLISHED ACCEPT rule; calls:\n%s", dumpCalls(mock))
	}
	// Chain attached to FORWARD
	if !mock.calledWithSubstr("iptables", "-I", "FORWARD", "-j", fwdChain) {
		t.Errorf("expected -I FORWARD -j %s; calls:\n%s", fwdChain, dumpCalls(mock))
	}
}

// --- TestEnableNAT_KillSwitch ------------------------------------------------

func TestEnableNAT_KillSwitch(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	if err := setupForwardRules("ap0", "wg0", true /*enableVPN*/, true /*killSwitch*/); err != nil {
		t.Fatalf("setupForwardRules error: %v", err)
	}

	// Kill-switch: DROP remaining AP traffic in fwdChain
	if !mock.calledWithSubstr(fwdChain, "-i", "ap0", "-j", "DROP") {
		t.Errorf("kill-switch: expected -A %s -i ap0 -j DROP; calls:\n%s", fwdChain, dumpCalls(mock))
	}
}

// --- TestEnableNAT_NoKillSwitch ----------------------------------------------

func TestEnableNAT_NoKillSwitch(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	if err := setupForwardRules("ap0", "wg0", true /*enableVPN*/, false /*killSwitch*/); err != nil {
		t.Fatalf("setupForwardRules error: %v", err)
	}

	// No DROP rule should appear when kill-switch is disabled
	for _, c := range mock.calls {
		full := strings.Join(append([]string{c.name}, c.args...), " ")
		if strings.Contains(full, fwdChain) && strings.Contains(full, "-i ap0") && strings.Contains(full, "DROP") {
			t.Errorf("unexpected kill-switch DROP rule when VPNKillSwitch=false: %s", full)
		}
	}
}

// --- TestEnableNAT_HostIsolation (setupHostIsolation) ------------------------

func TestEnableNAT_HostIsolation(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	if err := setupHostIsolation("ap0"); err != nil {
		t.Fatalf("setupHostIsolation error: %v", err)
	}

	// ROUTERD_IN chain created
	if !mock.calledWithSubstr("iptables", "-N", inputChain) {
		t.Errorf("expected iptables -N %s; calls:\n%s", inputChain, dumpCalls(mock))
	}
	// Attached to INPUT
	if !mock.calledWithSubstr("iptables", "-I", "INPUT", "-j", inputChain) {
		t.Errorf("expected -I INPUT -j %s; calls:\n%s", inputChain, dumpCalls(mock))
	}
	// DHCP allowed
	if !mock.calledWithSubstr(inputChain, "-i", "ap0", "--dport", "67:68") {
		t.Errorf("expected DHCP allow rule; calls:\n%s", dumpCalls(mock))
	}
	// DNS allowed
	if !mock.calledWithSubstr(inputChain, "-i", "ap0", "--dport", "53") {
		t.Errorf("expected DNS allow rule; calls:\n%s", dumpCalls(mock))
	}
	// DROP at end
	if !mock.calledWithSubstr(inputChain, "-i", "ap0", "-j", "DROP") {
		t.Errorf("expected DROP rule for ap0 in %s; calls:\n%s", inputChain, dumpCalls(mock))
	}
}

// --- TestEnableNAT_TTLSpoofing (setupMangleRules) ----------------------------

func TestEnableNAT_TTLSpoofing(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	setupMangleRules("ap0", 64, false)

	// Mangle chain created
	if !mock.calledWithSubstr("iptables", "-t", "mangle", "-N", mangleChain) {
		t.Errorf("expected mangle chain %s; calls:\n%s", mangleChain, dumpCalls(mock))
	}
	// TTL rule
	if !mock.calledWithSubstr(mangleChain, "TTL", "--ttl-set", "64") {
		t.Errorf("expected TTL spoofing rule; calls:\n%s", dumpCalls(mock))
	}
}

// --- TestEnableNAT_MSSClamping (setupMangleRules) ----------------------------

func TestEnableNAT_MSSClamping(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	setupMangleRules("ap0", 0, true /*mssClamping*/)

	// MSS clamping rule
	if !mock.calledWithSubstr("--clamp-mss-to-pmtu") {
		t.Errorf("expected --clamp-mss-to-pmtu; calls:\n%s", dumpCalls(mock))
	}
}

// --- TestEnableNAT_IPv6LeakProtection (setupIPv6LeakProtection) --------------

func TestEnableNAT_IPv6LeakProtection(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	setupIPv6LeakProtection("ap0")

	// ip6tables DROP on INPUT for ap0
	if !mock.calledWithSubstr("ip6tables", "INPUT", "-i", "ap0", "-j", "DROP") {
		t.Errorf("expected ip6tables DROP INPUT ap0; calls:\n%s", dumpCalls(mock))
	}
	// ip6tables DROP on FORWARD for ap0
	if !mock.calledWithSubstr("ip6tables", "FORWARD", "-i", "ap0", "-j", "DROP") {
		t.Errorf("expected ip6tables DROP FORWARD ap0; calls:\n%s", dumpCalls(mock))
	}
}

// --- TestEnableNAT_VPNDNSForced (setupVPNDNSRules) ---------------------------

func TestEnableNAT_VPNDNSForced(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	setupVPNDNSRules("ap0", "1.1.1.1")

	// DNAT rule for DNS → VPN DNS
	if !mock.calledWithSubstr("DNAT", "--to-destination", "1.1.1.1:53") {
		t.Errorf("expected DNS DNAT to 1.1.1.1:53; calls:\n%s", dumpCalls(mock))
	}
	// Both UDP and TCP DNS should be redirected
	if !mock.calledWithSubstr("-p", "udp", "--dport", "53") {
		t.Errorf("expected UDP DNS DNAT; calls:\n%s", dumpCalls(mock))
	}
	if !mock.calledWithSubstr("-p", "tcp", "--dport", "53") {
		t.Errorf("expected TCP DNS DNAT; calls:\n%s", dumpCalls(mock))
	}
}

// --- TestEnableNAT_IPForwardSaved --------------------------------------------

// TestEnableNAT_IPForwardSaved verifies that enableNAT persists ip_forward before changing it.
// The test runs as non-root so setIPForward will fail (permission denied on /proc),
// but the persistence file must still be written before that failure.
func TestEnableNAT_IPForwardSaved(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	dir := t.TempDir()
	cfg := NATConfig{
		RunDir: dir,
		Uplink: "wlan0",
		AP:     "ap0",
		VPNDNS: "1.1.1.1",
	}
	// enableNAT writes ip_forward.orig before calling setIPForward.
	// Even if setIPForward fails (no root), the file must already exist.
	_ = enableNAT(cfg)

	data, err := os.ReadFile(filepath.Join(dir, "ip_forward.orig"))
	if err != nil {
		t.Fatalf("ip_forward.orig not written: %v", err)
	}
	v := strings.TrimSpace(string(data))
	if v != "0" && v != "1" {
		t.Errorf("ip_forward.orig = %q, want '0' or '1'", v)
	}
}

// --- TestEnableNAT_TorRules (setupTorRules) ----------------------------------

func TestEnableNAT_TorRules(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	if err := setupTorRules("ap0"); err != nil {
		t.Fatalf("setupTorRules error: %v", err)
	}

	// DNS port redirect to Tor DNS port
	if !mock.calledWithSubstr("--to-ports", torDNSPort) {
		t.Errorf("expected DNS redirect to Tor port %s; calls:\n%s", torDNSPort, dumpCalls(mock))
	}
	// TCP redirect to Tor transparent proxy port
	if !mock.calledWithSubstr("--to-ports", torTCPPort) {
		t.Errorf("expected TCP redirect to Tor port %s; calls:\n%s", torTCPPort, dumpCalls(mock))
	}
	// PREROUTING chain attached
	if !mock.calledWithSubstr("iptables", "-t", "nat", "-I", "PREROUTING", "-j", natPreChain) {
		t.Errorf("expected PREROUTING attach; calls:\n%s", dumpCalls(mock))
	}
}

// --- TestIsAlreadyExists -----------------------------------------------------

func TestIsAlreadyExists_True(t *testing.T) {
	cases := []string{
		"Chain already exists",
		"already exists",
		"Already exists: something",
	}
	for _, s := range cases {
		if !isAlreadyExists(s) {
			t.Errorf("isAlreadyExists(%q) = false, want true", s)
		}
	}
}

func TestIsAlreadyExists_False(t *testing.T) {
	cases := []string{
		"",
		"no such table",
		"Bad rule (do you need -t?)",
		"exit status 1",
	}
	for _, s := range cases {
		if isAlreadyExists(s) {
			t.Errorf("isAlreadyExists(%q) = true, want false", s)
		}
	}
}

// --- TestAtoiSafe ------------------------------------------------------------

func TestAtoiSafe_Valid(t *testing.T) {
	if got := atoiSafe("42"); got != 42 {
		t.Errorf("atoiSafe('42') = %d, want 42", got)
	}
}

func TestAtoiSafe_Invalid(t *testing.T) {
	if got := atoiSafe("abc"); got != 0 {
		t.Errorf("atoiSafe('abc') = %d, want 0", got)
	}
}

func TestAtoiSafe_Empty(t *testing.T) {
	if got := atoiSafe(""); got != 0 {
		t.Errorf("atoiSafe('') = %d, want 0", got)
	}
}

// --- TestSetupRateLimit ------------------------------------------------------

func TestSetupRateLimit_Zero(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	setupRateLimit("ap0", 0)

	for _, c := range mock.calls {
		if c.name == "tc" {
			t.Errorf("expected no tc calls for mbps=0, got: %v %v", c.name, c.args)
		}
	}
}

func TestSetupRateLimit_Positive(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	setupRateLimit("ap0", 10)

	if !mock.calledWithSubstr("tc", "qdisc", "add", "dev", "ap0") {
		t.Errorf("expected 'tc qdisc add dev ap0' call; calls:\n%s", dumpCalls(mock))
	}
	if !mock.calledWithSubstr("10mbit") {
		t.Errorf("expected '10mbit' rate in tc call; calls:\n%s", dumpCalls(mock))
	}
}

// --- TestCleanupRateLimit ----------------------------------------------------

func TestCleanupRateLimit_Empty(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	cleanupRateLimit("")

	for _, c := range mock.calls {
		if c.name == "tc" {
			t.Errorf("expected no tc calls for empty ap, got: %v %v", c.name, c.args)
		}
	}
}

func TestCleanupRateLimit_Called(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	cleanupRateLimit("ap0")

	if !mock.calledWith("tc", "qdisc", "del", "dev", "ap0", "root") {
		t.Errorf("expected 'tc qdisc del dev ap0 root'; calls:\n%s", dumpCalls(mock))
	}
}

// --- TestDisableNAT_CallsAllChains -------------------------------------------

func TestDisableNAT_CallsAllChains(t *testing.T) {
	mock := newMockRunner()
	restore := withMockNFT(mock)
	defer restore()

	dir := natRunDir(t)
	disableNAT(dir, "ap0")

	// Verify flush/delete of all custom chains.
	chains := []struct {
		table string
		chain string
	}{
		{"nat", natPreChain},
		{"nat", natPostChain},
		{"", fwdChain},
		{"", inputChain},
		{"mangle", mangleChain},
	}
	for _, ch := range chains {
		if ch.table != "" {
			if !mock.calledWithSubstr("iptables", "-t", ch.table, "-F", ch.chain) {
				t.Errorf("expected iptables -t %s -F %s; calls:\n%s", ch.table, ch.chain, dumpCalls(mock))
			}
			if !mock.calledWithSubstr("iptables", "-t", ch.table, "-X", ch.chain) {
				t.Errorf("expected iptables -t %s -X %s; calls:\n%s", ch.table, ch.chain, dumpCalls(mock))
			}
		} else {
			if !mock.calledWithSubstr("iptables", "-F", ch.chain) {
				t.Errorf("expected iptables -F %s; calls:\n%s", ch.chain, dumpCalls(mock))
			}
			if !mock.calledWithSubstr("iptables", "-X", ch.chain) {
				t.Errorf("expected iptables -X %s; calls:\n%s", ch.chain, dumpCalls(mock))
			}
		}
	}
}
