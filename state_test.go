package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- TestWriteStateReadState_RoundTrip ---------------------------------------

func TestWriteStateReadState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := State{
		SSID:         "TestNet",
		InterfaceAP:  "ap0",
		InterfaceSTA: "wlan0",
		Uplink:       "wlan0",
		Channel:      6,
		Band:         "g",
		Subnet:       "192.168.50.0/24",
		VPNMode:      "wireguard",
		VPNActive:    true,
		StartTime:    1234567890,
	}

	if err := writeStateTo(s, dir); err != nil {
		t.Fatalf("writeStateTo: %v", err)
	}

	got, ok := readStateFrom(dir)
	if !ok {
		t.Fatal("readStateFrom returned ok=false")
	}

	if got.SSID != s.SSID {
		t.Errorf("SSID = %q, want %q", got.SSID, s.SSID)
	}
	if got.InterfaceAP != s.InterfaceAP {
		t.Errorf("InterfaceAP = %q, want %q", got.InterfaceAP, s.InterfaceAP)
	}
	if got.InterfaceSTA != s.InterfaceSTA {
		t.Errorf("InterfaceSTA = %q, want %q", got.InterfaceSTA, s.InterfaceSTA)
	}
	if got.Uplink != s.Uplink {
		t.Errorf("Uplink = %q, want %q", got.Uplink, s.Uplink)
	}
	if got.Channel != s.Channel {
		t.Errorf("Channel = %d, want %d", got.Channel, s.Channel)
	}
	if got.Band != s.Band {
		t.Errorf("Band = %q, want %q", got.Band, s.Band)
	}
	if got.Subnet != s.Subnet {
		t.Errorf("Subnet = %q, want %q", got.Subnet, s.Subnet)
	}
	if got.VPNMode != s.VPNMode {
		t.Errorf("VPNMode = %q, want %q", got.VPNMode, s.VPNMode)
	}
	if got.VPNActive != s.VPNActive {
		t.Errorf("VPNActive = %v, want %v", got.VPNActive, s.VPNActive)
	}
	if got.StartTime != s.StartTime {
		t.Errorf("StartTime = %d, want %d", got.StartTime, s.StartTime)
	}
}

// --- TestReadState_Missing ---------------------------------------------------

func TestReadState_Missing(t *testing.T) {
	dir := t.TempDir()
	_, ok := readStateFrom(dir)
	if ok {
		t.Error("readStateFrom on empty dir should return ok=false")
	}
}

// --- TestReadState_Malformed -------------------------------------------------

func TestReadState_Malformed(t *testing.T) {
	dir := t.TempDir()
	// Write garbage data — should not panic, just return ok=false (no InterfaceAP).
	if err := os.WriteFile(filepath.Join(dir, "state"), []byte("not=valid\ngarbage"), 0644); err != nil {
		t.Fatal(err)
	}
	// Should not panic.
	_, ok := readStateFrom(dir)
	// ok=false because InterfaceAP will be empty for garbage data
	if ok {
		t.Error("expected ok=false for malformed state file with no INTERFACE_AP")
	}
}

// --- TestWriteState_AllFields ------------------------------------------------

func TestWriteState_AllFields(t *testing.T) {
	dir := t.TempDir()
	s := State{
		SSID:         "myap",
		InterfaceAP:  "ap0",
		InterfaceSTA: "wlan0",
		Uplink:       "wg0",
		Channel:      36,
		Band:         "a",
		Subnet:       "10.99.0.0/24",
		VPNMode:      "wireguard",
		VPNActive:    true,
		StartTime:    9999999,
	}

	if err := writeStateTo(s, dir); err != nil {
		t.Fatalf("writeStateTo: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	content := string(data)

	checks := []string{
		"SSID=myap",
		"INTERFACE_AP=ap0",
		"INTERFACE_STA=wlan0",
		"UPLINK=wg0",
		"CHANNEL=36",
		"BAND=a",
		"SUBNET=10.99.0.0/24",
		"VPN_MODE=wireguard",
		"VPN_ACTIVE=true",
		"START_TIME=9999999",
	}
	for _, want := range checks {
		if !strings.Contains(content, want) {
			t.Errorf("state file missing %q\n--- content ---\n%s", want, content)
		}
	}
}

// --- TestWriteClients_Basic --------------------------------------------------

func TestWriteClients_Basic(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	// Provide a station dump with one station.
	stationDump := `Station aa:bb:cc:dd:ee:ff (on ap0)
	signal:  -50 dBm
	tx bitrate: 54.0 MBit/s
`
	mock.stub(stationDump, nil, "iw", "dev", "ap0", "station", "dump")

	dir := t.TempDir()
	writeClients("ap0", dir)

	data, err := os.ReadFile(filepath.Join(dir, "clients.json"))
	if err != nil {
		t.Fatalf("clients.json not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "aa:bb:cc:dd:ee:ff") {
		t.Errorf("clients.json missing MAC; content: %s", content)
	}
}

// --- TestWriteClients_NoStations --------------------------------------------

func TestWriteClients_NoStations(t *testing.T) {
	mock := newMockRunner()
	restore := installMockRunner(mock)
	defer restore()

	// Empty station dump.
	mock.stub("", nil, "iw", "dev", "ap0", "station", "dump")

	dir := t.TempDir()
	writeClients("ap0", dir)

	data, err := os.ReadFile(filepath.Join(dir, "clients.json"))
	if err != nil {
		t.Fatalf("clients.json not written: %v", err)
	}
	content := strings.TrimSpace(string(data))
	if content != "[]" {
		t.Errorf("expected empty JSON array [], got: %s", content)
	}
}
