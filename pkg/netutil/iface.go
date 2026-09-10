package netutil

import (
    "fmt"
    "net"
    "os"
)

// GetDefaultInterface mencari interface jaringan aktif yang memiliki rute ke internet
func GetDefaultInterface() (*net.Interface, error) {
    // 1. Pancing kernel Linux mencari rute keluar menuju internet
    conn, err := net.Dial("udp", "1.1.1.1:80")
    if err != nil {
        return nil, fmt.Errorf("tidak ada koneksi internet: %w", err)
    }
    defer conn.Close()

    localAddr := conn.LocalAddr().(*net.UDPAddr)

    // 2. Cari interface fisik mana yang memegang local IP tersebut
    ifaces, err := net.Interfaces()
    if err != nil {
        return nil, fmt.Errorf("gagal membaca interface: %w", err)
    }

    for _, iface := range ifaces {
        addrs, err := iface.Addrs()
        if err != nil {
            continue
        }
        for _, addr := range addrs {
            var ip net.IP
            switch v := addr.(type) {
            case *net.IPNet:
                ip = v.IP
            case *net.IPAddr:
                ip = v.IP
            }
            if ip != nil && ip.Equal(localAddr.IP) {
                return &iface, nil
            }
        }
    }

    return nil, fmt.Errorf("interface untuk IP %s tidak ditemukan", localAddr.IP)
}

// IsWireless memeriksa apakah sebuah interface adalah wireless device via Linux sysfs (/sys/class/net/<iface>/wireless).
func IsWireless(ifaceName string) bool {
    path := fmt.Sprintf("/sys/class/net/%s/wireless", ifaceName)
    _, err := netutilStat(path)
    return err == nil
}

var netutilStat = func(name string) (any, error) {
    return os.Stat(name)
}
