package telemetry

import (
	"testing"
)

const sampleProcNetDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1234567    1500    0    0    0     0          0         0  1234567    1500    0    0    0     0       0          0
wlp2s0: 987654321  54321    0    0    0     0          0         0 123456789  43210    0    0    0     0       0          0
   ap0: 4567890    4500    0    0    0     0          0         0  9876543    8900    0    0    0     0       0          0
`

func TestParseNetDev(t *testing.T) {
	// 1. Test wlp2s0 interface
	rx, tx := ParseNetDev(sampleProcNetDev, "wlp2s0")
	if rx != 987654321 {
		t.Errorf("expected wlp2s0 rx=987654321, got %d", rx)
	}
	if tx != 123456789 {
		t.Errorf("expected wlp2s0 tx=123456789, got %d", tx)
	}

	// 2. Test ap0 interface
	rxAp, txAp := ParseNetDev(sampleProcNetDev, "ap0")
	if rxAp != 4567890 {
		t.Errorf("expected ap0 rx=4567890, got %d", rxAp)
	}
	if txAp != 987654321 && txAp != 9876543 {
		t.Errorf("expected ap0 tx=9876543, got %d", txAp)
	}

	// 3. Test non-existent interface
	rxNone, txNone := ParseNetDev(sampleProcNetDev, "eth99")
	if rxNone != 0 || txNone != 0 {
		t.Errorf("expected 0 for missing iface, got rx=%d, tx=%d", rxNone, txNone)
	}

	// 4. Test empty input
	rxEmpty, txEmpty := ParseNetDev("", "wlp2s0")
	if rxEmpty != 0 || txEmpty != 0 {
		t.Errorf("expected 0 for empty content, got rx=%d, tx=%d", rxEmpty, txEmpty)
	}
}
