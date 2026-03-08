package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// PrinterInfo describes a detected printer with metadata.
type PrinterInfo struct {
	Name      string `json:"name"`
	Type      string `json:"type"`                        // "usb" | "cups" | "spooler" | "network" | "manual" | "raw"
	Address   string `json:"address,omitempty"`            // "192.168.1.50:9100" for network printers
	Model     string `json:"model,omitempty"`              // parsed from ~!I response
	Online    bool   `json:"online"`                       // true if printer is reachable/connected
	IsSelf    bool   `json:"is_self,omitempty"`            // true if this is our own shared printer
	Status    string `json:"status,omitempty"`             // "idle", "offline", "disabled", etc.
}

const (
	networkPort     = 9100
	scanTimeout     = 300 * time.Millisecond
	probeTimeout    = 500 * time.Millisecond
	printTimeout    = 10 * time.Second
	maxConcurrent   = 64
)

var (
	networkPrinters []PrinterInfo
	networkMu       sync.RWMutex
	lastScanTime    time.Time
)

// tscModelKeywords identifies TSC printers from ~!I response.
var tscModelKeywords = []string{
	"TSC", "TDP", "TE2", "TE3", "TX2", "TX3",
	"TTP", "TTP-220", "TTP-225", "TTP-244", "TTP-247",
	"DA2", "MH", "Alpha",
}

// getLocalSubnets returns all IPv4 /24 subnets on local interfaces.
func getLocalSubnets() []net.IPNet {
	var subnets []net.IPNet
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("[network] Failed to list interfaces: %v", err)
		return nil
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			// Skip link-local (169.254.x.x)
			if ip4[0] == 169 && ip4[1] == 254 {
				continue
			}
			// Normalize to /24 for scanning
			subnet := net.IPNet{
				IP:   ip4.Mask(net.CIDRMask(24, 32)),
				Mask: net.CIDRMask(24, 32),
			}
			subnets = append(subnets, subnet)
		}
	}
	return subnets
}

// probeTSCPrinter connects to addr:9100, sends ~!I, and parses the response.
// Returns the model string and whether it's a TSC printer.
// If the port is open but doesn't respond to ~!I, it tries ~!T and ~!F as fallbacks.
func probeTSCPrinter(addr string) (string, bool) {
	// Try multiple TSC probe commands in order
	probeCommands := []string{"~!I\r\n", "~!T\r\n", "~!F\r\n"}

	for _, cmd := range probeCommands {
		model, isTSC := probeTSCWithCommand(addr, cmd)
		if isTSC {
			return model, true
		}
	}

	return "", false
}

// probeRawPort checks if a TCP port is open and accepts connections.
// Returns true if the port is open (potential raw printer).
func probeRawPort(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// isNonPrinterService sends a harmless probe and checks if the response
// looks like a non-printer service (HTTP, SSH, FTP, etc).
func isNonPrinterService(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(probeTimeout))

	// Send an HTTP-like probe — real printers ignore this, HTTP servers respond
	conn.Write([]byte("GET / HTTP/1.0\r\n\r\n"))

	buf := make([]byte, 32)
	n, _ := conn.Read(buf)
	if n == 0 {
		return false // no response = likely a raw printer
	}

	resp := strings.TrimSpace(string(buf[:n]))
	for _, prefix := range falsePositivePrefixes {
		if strings.HasPrefix(resp, prefix) {
			return true
		}
	}
	return false
}

// Non-printer response prefixes to reject (HTTP servers, etc. listening on 9100)
var falsePositivePrefixes = []string{
	"HTTP/", "<!DOCTYPE", "<html", "<HTML", "SSH-", "220 ",
}

// probeTSCWithCommand sends a single TSC command and checks the response.
func probeTSCWithCommand(addr, cmd string) (string, bool) {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return "", false
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(probeTimeout))

	_, err = conn.Write([]byte(cmd))
	if err != nil {
		return "", false
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return "", false
	}

	response := string(buf[:n])

	// Reject responses from non-printer services (HTTP servers, SSH, FTP, etc.)
	trimResp := strings.TrimSpace(response)
	for _, prefix := range falsePositivePrefixes {
		if strings.HasPrefix(trimResp, prefix) {
			return "", false
		}
	}

	upper := strings.ToUpper(response)

	for _, kw := range tscModelKeywords {
		if strings.Contains(upper, kw) {
			// Extract first non-empty line as model
			model := ""
			for _, line := range strings.Split(response, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					model = line
					break
				}
			}
			return model, true
		}
	}

	return "", false
}

// scanSubnetForPrinters scans a /24 subnet for printers on port 9100.
// Detects TSC printers (via probe), raw printers (port open), and marks self-shared printers.
func scanSubnetForPrinters(subnet net.IPNet, localIPs map[string]bool) []PrinterInfo {
	var (
		found []PrinterInfo
		mu    sync.Mutex
		wg    sync.WaitGroup
		sema  = make(chan struct{}, maxConcurrent)
	)

	base := subnet.IP.To4()
	if base == nil {
		return nil
	}

	for i := 1; i <= 254; i++ {
		ip := fmt.Sprintf("%d.%d.%d.%d", base[0], base[1], base[2], i)
		addr := fmt.Sprintf("%s:%d", ip, networkPort)
		isSelf := localIPs[ip]

		wg.Add(1)
		sema <- struct{}{}
		go func(addr, ip string, isSelf bool) {
			defer wg.Done()
			defer func() { <-sema }()

			// Quick TCP connect check
			conn, err := net.DialTimeout("tcp", addr, scanTimeout)
			if err != nil {
				return
			}
			conn.Close()

			// Port 9100 is open — probe for TSC
			model, isTSC := probeTSCPrinter(addr)
			if isTSC {
				name := model
				if name == "" {
					name = "TSC"
				}
				if isSelf {
					name += " @ " + ip + " (compartida)"
				} else {
					name += " @ " + ip
				}

				mu.Lock()
				found = append(found, PrinterInfo{
					Name:    name,
					Type:    "network",
					Address: addr,
					Model:   model,
					Online:  true,
					IsSelf:  isSelf,
				})
				mu.Unlock()
				return
			}

			// Port open but no TSC response — check if it's a non-printer service
			if isNonPrinterService(addr) {
				return // HTTP server, SSH, etc. — not a printer
			}

			name := "Impresora RAW @ " + ip
			if isSelf {
				name = "Compartida @ " + ip
			}
			mu.Lock()
			found = append(found, PrinterInfo{
				Name:    name,
				Type:    "raw",
				Address: addr,
				Online:  true,
				IsSelf:  isSelf,
			})
			mu.Unlock()
		}(addr, ip, isSelf)
	}

	wg.Wait()
	return found
}

// networkRawPrint sends raw TSPL2 data to a network printer via TCP.
func networkRawPrint(addr string, data []byte) error {
	conn, err := net.DialTimeout("tcp", addr, printTimeout)
	if err != nil {
		return fmt.Errorf("cannot connect to %s: %w", addr, err)
	}
	defer conn.Close()

	conn.SetWriteDeadline(time.Now().Add(printTimeout))

	written, err := conn.Write(data)
	if err != nil {
		return fmt.Errorf("write to %s failed: %w", addr, err)
	}

	log.Printf("[print-net] Sent %d/%d bytes to %s", written, len(data), addr)
	return nil
}

// startNetworkScanner runs a background goroutine that rescans the network.
func startNetworkScanner() {
	cfg := getConfig()
	if !cfg.NetworkScanEnabled {
		log.Printf("[network] Network scanning disabled")
		return
	}

	// Initial scan
	doNetworkScan()

	interval := time.Duration(cfg.NetworkScanInterval) * time.Second
	if interval < 10*time.Second {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		cfg = getConfig()
		if !cfg.NetworkScanEnabled {
			continue
		}
		doNetworkScan()
	}
}

// doNetworkScan performs the actual subnet scan + manual printers probe.
func doNetworkScan() {
	start := time.Now()

	subnets := getLocalSubnets()

	// Build set of local IPs to skip during scan (avoid self-detection via share)
	localIPs := make(map[string]bool)
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil {
				localIPs[ip4.String()] = true
			}
		}
	}

	var all []PrinterInfo

	for _, subnet := range subnets {
		found := scanSubnetForPrinters(subnet, localIPs)
		all = append(all, found...)
	}

	// Also probe manually configured printers — always show them even if probe fails
	cfg := getConfig()
	for _, addr := range cfg.ManualPrinters {
		if !strings.Contains(addr, ":") {
			addr = fmt.Sprintf("%s:%d", addr, networkPort)
		}
		ip := strings.Split(addr, ":")[0]

		// Avoid duplicates from subnet scan
		duplicate := false
		for _, p := range all {
			if p.Address == addr {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}

		model, isTSC := probeTSCPrinter(addr)
		if isTSC {
			name := model
			if name == "" {
				name = "TSC"
			}
			name += " @ " + ip
			all = append(all, PrinterInfo{
				Name:    name,
				Type:    "network",
				Address: addr,
				Model:   model,
				Online:  true,
			})
		} else {
			// Check if port is at least reachable
			online := probeRawPort(addr)
			all = append(all, PrinterInfo{
				Name:    "Manual @ " + ip,
				Type:    "manual",
				Address: addr,
				Online:  online,
			})
		}
	}

	networkMu.Lock()
	networkPrinters = all
	lastScanTime = time.Now()
	networkMu.Unlock()

	elapsed := time.Since(start)
	log.Printf("[network] Scan complete: %d printer(s) found in %v (%d subnet(s))", len(all), elapsed, len(subnets))
}

// getNetworkPrinters returns the cached list of network printers.
func getNetworkPrinters() []PrinterInfo {
	networkMu.RLock()
	defer networkMu.RUnlock()
	result := make([]PrinterInfo, len(networkPrinters))
	copy(result, networkPrinters)
	return result
}

// refreshNetworkPrinters forces an immediate rescan and returns results.
func refreshNetworkPrinters() []PrinterInfo {
	doNetworkScan()
	return getNetworkPrinters()
}
