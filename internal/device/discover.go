// Package device talks to CrossPoint readers (Xteink X3/X4) over Wi-Fi.
//
// The firmware exposes a documented HTTP API while the device sits in its
// File Transfer / Calibre Wireless screen: UDP discovery on :8134, an HTTP
// server on :80 (status, file listing, delete, OPDS provisioning), and a
// WebSocket upload channel on :81. This package wraps those endpoints so the
// app can do what Calibre does — know which books are on the device and sync
// the rest — without a cable.
//
// Verified against crosspoint-reader/crosspoint-reader `develop`,
// docs/webserver-endpoints.md (2026-08-29).
package device

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DiscoveryPort is the UDP port the firmware listens on for "hello".
const DiscoveryPort = 8134

const discoveryPayload = "hello"

// Discovered is one reader that answered a discovery broadcast.
type Discovered struct {
	IP       string // device address; HTTP base is http://<IP>/ (port 80)
	WSPort   int    // WebSocket upload port (documented as 81)
	Hostname string // device-reported hostname
}

// Discover broadcasts "hello" to every IPv4 interface's broadcast address and
// collects replies until the wait elapses. Replies look like
// "crosspoint (on myx3);81". Only readers in transfer mode answer.
func Discover(wait time.Duration) ([]Discovered, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("device discovery: %w", err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return nil, err
	}

	targets := broadcastAddrs()
	if len(targets) == 0 {
		targets = []string{fmt.Sprintf("255.255.255.255:%d", DiscoveryPort)}
	}
	payload := []byte(discoveryPayload)
	for _, t := range targets {
		dst, err := net.ResolveUDPAddr("udp4", t)
		if err != nil {
			continue
		}
		if _, err := conn.WriteToUDP(payload, dst); err != nil {
			continue
		}
	}

	var mu sync.Mutex
	seen := map[string]bool{}
	out := []Discovered{}
	buf := make([]byte, 512)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline (normal) or socket error
		}
		host, wsPort, ok := parseReply(string(buf[:n]))
		if !ok {
			continue
		}
		ip := from.IP.String()
		mu.Lock()
		if !seen[ip] {
			seen[ip] = true
			out = append(out, Discovered{IP: ip, WSPort: wsPort, Hostname: host})
		}
		mu.Unlock()
	}
	return out, nil
}

// broadcastAddrs returns a.b.c.255 style addresses for every active IPv4
// interface, plus the limited broadcast. IPv6 is skipped deliberately — the
// shared HTTP client is IPv4-only by policy, and ESP32 firmware is v4 anyway.
func broadcastAddrs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.To4()
			// only the interface's own address, not its broadcast entry
			if !ip.IsGlobalUnicast() {
				continue
			}
			b := make(net.IP, len(ip))
			copy(b, ip)
			for i := range b {
				b[i] |= 0xff ^ maskByte(ipnet.Mask, i)
			}
			out = append(out, net.JoinHostPort(b.String(), strconv.Itoa(DiscoveryPort)))
		}
	}
	out = append(out, fmt.Sprintf("255.255.255.255:%d", DiscoveryPort))
	return out
}

func maskByte(mask net.IPMask, i int) byte {
	if i < len(mask) {
		return mask[i]
	}
	return 0
}

// parseReply parses "crosspoint (on myx3);81".
func parseReply(s string) (host string, wsPort int, ok bool) {
	s = strings.TrimSpace(string(s))
	if !strings.HasPrefix(s, "crosspoint") {
		return "", 0, false
	}
	i := strings.LastIndexByte(s, ';')
	if i < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(s[i+1:]))
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, false
	}
	rest := s[:i]
	open := strings.Index(rest, "(on ")
	if open >= 0 {
		host = strings.TrimSuffix(rest[open+len("(on "):], ")")
	}
	return strings.TrimSpace(host), port, true
}
