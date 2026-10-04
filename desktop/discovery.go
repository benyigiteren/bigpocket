package main

// ----------------- LAN Auto Discovery -----------------
// The desktop periodically broadcasts a small JSON "hello" packet over UDP so the
// Android app can find the PC automatically (KDE Connect style) without typing an IP.
// Android listens on DiscoveryPort and can also send "bigpocket_discover" to get an
// immediate unicast reply.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

const DiscoveryPort = 47800

func discoveryPayload() []byte {
	host, _ := os.Hostname()
	payload, _ := json.Marshal(map[string]interface{}{
		"app":           "bigpocket",
		"name":          host,
		"port":          8085,
		"version":       AppVersion,
		"needsPassword": loadConfig().Password != "",
	})
	return payload
}

// broadcastAddrs returns the directed broadcast address of every active IPv4 interface.
func broadcastAddrs() []net.IP {
	result := []net.IP{net.IPv4bcast}
	ifaces, err := net.Interfaces()
	if err != nil {
		return result
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.To4()
			mask := ipnet.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			b := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				b[i] = ip[i] | ^mask[i]
			}
			result = append(result, b)
		}
	}
	return result
}

func startDiscovery() {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: DiscoveryPort})
	if err != nil {
		fmt.Println("Discovery listener error:", err)
		// Still try to broadcast from an ephemeral port.
		conn, err = net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
		if err != nil {
			fmt.Println("Discovery disabled:", err)
			return
		}
	}

	// Answer explicit discovery requests immediately.
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) == "bigpocket_discover" {
				conn.WriteToUDP(discoveryPayload(), addr)
			}
		}
	}()

	fmt.Printf("Discovery broadcasting on UDP %d...\n", DiscoveryPort)
	for {
		payload := discoveryPayload()
		for _, ip := range broadcastAddrs() {
			conn.WriteToUDP(payload, &net.UDPAddr{IP: ip, Port: DiscoveryPort})
		}
		time.Sleep(2 * time.Second)
	}
}
