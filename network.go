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
	Name    string `json:"name"`
	Type    string `json:"type"`              // "usb" | "cups" | "spooler" | "network"
	Address string `json:"address,omitempty"` // "192.168.1.50:9100" for network printers
	Model   string `json:"model,omitempty"`   // parsed from ~!I response
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
	"TSC", "TDP", "TE2", "TE3", "TX2", "TX3", "TTP", "DA2", "MH", "Alpha",
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
func probeTSCPrinter(addr string) (string, bool) {
	conn, err := net.DialTimeout("tcp", addr, probeTimeout)
	if err != nil {
		return "", false
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(probeTimeout))

	// Send TSC info command
	_, err = conn.Write([]byte("~!I\r\n"))
	if err != nil {
		return "", false
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		// Port is open but no response — might still be a raw printer
		// but we can't confirm it's TSC
		return "", false
	}

	response := string(buf[:n])
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

// scanSubnetForPrinters scans a /24 subnet for TSC printers on port 9100.
func scanSubnetForPrinters(subnet net.IPNet) []PrinterInfo {
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

		wg.Add(1)
		sema <- struct{}{}
		go func(addr, ip string) {
			defer wg.Done()
			defer func() { <-sema }()

			// Quick TCP connect check
			conn, err := net.DialTimeout("tcp", addr, scanTimeout)
			if err != nil {
				return
			}
			conn.Close()

			// Port is open — probe for TSC
			model, isTSC := probeTSCPrinter(addr)
			if !isTSC {
				return
			}

			name := "TSC"
			if model != "" {
				name = model
			}
			name += " @ " + ip

			mu.Lock()
			found = append(found, PrinterInfo{
				Name:    name,
				Type:    "network",
				Address: addr,
				Model:   model,
			})
			mu.Unlock()
		}(addr, ip)
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
	var all []PrinterInfo

	for _, subnet := range subnets {
		found := scanSubnetForPrinters(subnet)
		all = append(all, found...)
	}

	// Also probe manually configured printers
	cfg := getConfig()
	for _, addr := range cfg.ManualPrinters {
		if !strings.Contains(addr, ":") {
			addr = fmt.Sprintf("%s:%d", addr, networkPort)
		}
		model, isTSC := probeTSCPrinter(addr)
		if isTSC {
			ip := strings.Split(addr, ":")[0]
			name := "TSC"
			if model != "" {
				name = model
			}
			name += " @ " + ip

			// Avoid duplicates from subnet scan
			duplicate := false
			for _, p := range all {
				if p.Address == addr {
					duplicate = true
					break
				}
			}
			if !duplicate {
				all = append(all, PrinterInfo{
					Name:    name,
					Type:    "network",
					Address: addr,
					Model:   model,
				})
			}
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
