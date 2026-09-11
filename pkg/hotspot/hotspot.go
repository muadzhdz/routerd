package hotspot

import "sync"

var (
	defaultCtrl   *Controller
	defaultCtrlMu sync.Mutex
)

// ConnectedClient merepresentasikan metadata perangkat yang terhubung ke hotspot.
type ConnectedClient struct {
	MAC       string
	IP        string
	Hostname  string
	Signal    string
	TxBitrate string
}

// StartHotspot adalah adapter backward-compatible untuk menyalakan hotspot via default Controller.
func StartHotspot(ifaceName, ssid, password string) error {
	defaultCtrlMu.Lock()
	defer defaultCtrlMu.Unlock()

	defaultCtrl = NewController(Config{
		ParentIface: ifaceName,
		SSID:        ssid,
		Password:    password,
	})
	return defaultCtrl.Start()
}

// StopHotspot adalah adapter backward-compatible untuk mematikan hotspot default Controller.
func StopHotspot() {
	defaultCtrlMu.Lock()
	defer defaultCtrlMu.Unlock()

	if defaultCtrl != nil {
		_ = defaultCtrl.Close()
		defaultCtrl = nil
	}
}

// GetConnectedClients membaca daftar client yang terhubung via default Controller atau file leases.
func GetConnectedClients() ([]ConnectedClient, error) {
	defaultCtrlMu.Lock()
	ctrl := defaultCtrl
	defaultCtrlMu.Unlock()

	if ctrl != nil {
		return ctrl.GetConnectedClients()
	}

	fallbackCtrl := NewController(Config{})
	return fallbackCtrl.GetConnectedClients()
}
