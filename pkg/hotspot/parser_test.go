package hotspot

import (
	"testing"
)

func TestParseDHCPLeases(t *testing.T) {
	sampleLease := `1726053000 de:47:28:4a:77:58 10.42.0.38 OPPO-A18 01:de:47:28:4a:77:58
1726053005 a4:c3:f0:11:22:33 10.42.0.42 * 01:a4:c3:f0:11:22:33
`
	clients := ParseDHCPLeases(sampleLease)
	if len(clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(clients))
	}

	oppo, ok := clients["de:47:28:4a:77:58"]
	if !ok {
		t.Fatal("OPPO client not found in map")
	}
	if oppo.Hostname != "OPPO-A18" || oppo.IP != "10.42.0.38" {
		t.Errorf("unexpected OPPO data: %+v", oppo)
	}

	anon, ok := clients["a4:c3:f0:11:22:33"]
	if !ok {
		t.Fatal("anonymous client not found in map")
	}
	if anon.Hostname != "Unknown Device" {
		t.Errorf("expected hostname 'Unknown Device' for '*', got %s", anon.Hostname)
	}

	// Empty lease
	empty := ParseDHCPLeases("")
	if len(empty) != 0 {
		t.Errorf("expected 0 clients for empty lease, got %d", len(empty))
	}
}

func TestParseStationDump(t *testing.T) {
	sampleDump := `Station de:47:28:4a:77:58 (on ap0)
	inactive time:	120 ms
	rx bytes:	145020
	rx packets:	1200
	tx bytes:	520300
	tx packets:	1800
	signal:  	-48 dBm
	tx bitrate:	58.5 MBit/s
Station c8:ff:77:11:22:33 (on ap0)
	inactive time:	30 ms
	signal:  	-72 dBm
	tx bitrate:	130.0 MBit/s VHT-MCS 5
`
	stations := ParseStationDump(sampleDump)
	if len(stations) != 2 {
		t.Fatalf("expected 2 stations, got %d", len(stations))
	}

	st1, ok := stations["de:47:28:4a:77:58"]
	if !ok {
		t.Fatal("station 1 not found")
	}
	if st1.Signal != "-48 dBm" || st1.TxBitrate != "58.5 MBit/s" {
		t.Errorf("unexpected station 1 stats: %+v", st1)
	}

	st2, ok := stations["c8:ff:77:11:22:33"]
	if !ok {
		t.Fatal("station 2 not found")
	}
	if st2.Signal != "-72 dBm" || st2.TxBitrate != "130.0 MBit/s VHT-MCS 5" {
		t.Errorf("unexpected station 2 stats: %+v", st2)
	}
}

func TestMergeClientStats(t *testing.T) {
	leases := map[string]ConnectedClient{
		"de:47:28:4a:77:58": {
			MAC:      "de:47:28:4a:77:58",
			IP:       "10.42.0.38",
			Hostname: "OPPO-A18",
		},
	}

	stats := map[string]StationStats{
		"de:47:28:4a:77:58": {
			Signal:    "-48 dBm",
			TxBitrate: "58.5 MBit/s",
		},
		"99:88:77:66:55:44": {
			Signal:    "-80 dBm",
			TxBitrate: "6.0 MBit/s",
		},
	}

	merged := MergeClientStats(leases, stats)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged clients, got %d", len(merged))
	}

	// Cek OPPO (ter-merge dari DHCP + station dump)
	foundOppo := false
	foundDynamic := false
	for _, c := range merged {
		if c.MAC == "de:47:28:4a:77:58" {
			foundOppo = true
			if c.Signal != "-48 dBm" || c.TxBitrate != "58.5 MBit/s" {
				t.Errorf("OPPO signal not merged correctly: %+v", c)
			}
		}
		if c.MAC == "99:88:77:66:55:44" {
			foundDynamic = true
			if c.IP != "Dynamic" || c.Hostname != "Wi-Fi Station" {
				t.Errorf("unassociated station data incorrect: %+v", c)
			}
		}
	}

	if !foundOppo || !foundDynamic {
		t.Errorf("missing expected merged records: foundOppo=%v, foundDynamic=%v", foundOppo, foundDynamic)
	}
}
