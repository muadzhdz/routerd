package hotspot

import "sync"

var (
	defaultCtrl   *Controller
	defaultCtrlMu sync.Mutex
)

// ConnectedClient represents metadata for a device connected to the hotspot.
type ConnectedClient struct {
	MAC       string
	IP        string
	Hostname  string
	Signal    string
	TxBitrate string
}

// StartHotspot is a backward-compatible adapter to start the hotspot via the default Controller.
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

// StopHotspot is a backward-compatible adapter to stop the default Controller hotspot.
func StopHotspot() {
	defaultCtrlMu.Lock()
	defer defaultCtrlMu.Unlock()

	if defaultCtrl != nil {
		_ = defaultCtrl.Close()
		defaultCtrl = nil
	}
}

// GetConnectedClients retrieves the list of connected clients via the default Controller or lease file.
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
