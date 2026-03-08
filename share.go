package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	shareReadTimeout  = 5 * time.Second
	shareMaxBytes     = 1 << 20 // 1 MB max per job
)

var (
	shareListener  net.Listener
	shareMu        sync.Mutex
	shareRunning   atomic.Bool
	shareConnCount atomic.Int64
	shareJobCount  atomic.Int64
)

// ShareStatus describes the current state of the printer sharing service.
type ShareStatus struct {
	Enabled        bool     `json:"enabled"`
	Running        bool     `json:"running"`
	Port           int      `json:"port"`
	Printer        string   `json:"printer"`
	Address        string   `json:"address,omitempty"`
	Connections    int64    `json:"connections"`
	JobsServed     int64    `json:"jobs_served"`
	LocalAddresses []string `json:"local_addresses"`
}

// getShareStatus returns the current sharing status.
func getShareStatus() ShareStatus {
	cfg := getConfig()
	addr := ""
	if shareRunning.Load() && shareListener != nil {
		addr = shareListener.Addr().String()
	}

	// Collect local IPv4 addresses from network interfaces
	var localAddrs []string
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
			ip4 := ipnet.IP.To4()
			if ip4 == nil || (ip4[0] == 169 && ip4[1] == 254) {
				continue
			}
			localAddrs = append(localAddrs, ip4.String())
		}
	}

	return ShareStatus{
		Enabled:        cfg.ShareEnabled,
		Running:        shareRunning.Load(),
		Port:           cfg.SharePort,
		Printer:        cfg.SharePrinter,
		Address:        addr,
		Connections:    shareConnCount.Load(),
		JobsServed:     shareJobCount.Load(),
		LocalAddresses: localAddrs,
	}
}

// startShareServer starts the TCP proxy listener for sharing a USB printer.
func startShareServer() {
	cfg := getConfig()
	if !cfg.ShareEnabled {
		log.Printf("[share] Printer sharing disabled")
		return
	}
	startShareListener(cfg.SharePort)
}

// startShareListener starts listening on the given port.
func startShareListener(port int) {
	shareMu.Lock()
	defer shareMu.Unlock()

	if shareRunning.Load() {
		return
	}

	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("[share] Cannot listen on %s: %v", addr, err)
		return
	}

	shareListener = ln
	shareRunning.Store(true)
	log.Printf("[share] Sharing printer on %s (port %d)", addr, port)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if shareRunning.Load() {
					log.Printf("[share] Accept error: %v", err)
				}
				return
			}
			shareConnCount.Add(1)
			go handleShareConnection(conn)
		}
	}()
}

// stopShareListener stops the sharing listener.
func stopShareListener() {
	shareMu.Lock()
	defer shareMu.Unlock()

	if !shareRunning.Load() {
		return
	}

	shareRunning.Store(false)
	if shareListener != nil {
		shareListener.Close()
		shareListener = nil
	}
	log.Printf("[share] Printer sharing stopped")
}

// getSharePrinterName resolves which local printer to use for sharing.
func getSharePrinterName() string {
	cfg := getConfig()
	if cfg.SharePrinter != "" {
		return cfg.SharePrinter
	}
	if cfg.DefaultPrinter != "" {
		return cfg.DefaultPrinter
	}
	local, _ := listLocalPrinters()
	if len(local) > 0 {
		return local[0].Name
	}
	return ""
}

// getShareModelResponse builds a synthetic ~!I response for the shared printer.
func getShareModelResponse() string {
	cfg := getConfig()
	printerName := cfg.SharePrinter
	if printerName == "" {
		printerName = cfg.DefaultPrinter
	}

	// Find the local printer to get its model
	local, _ := listLocalPrinters()
	model := "TSC TDP-244 Plus"
	for _, p := range local {
		if p.Name == printerName || printerName == "" {
			if p.Model != "" {
				model = p.Model
			} else {
				model = "TSC " + p.Name
			}
			break
		}
	}

	// Format like a real TSC ~!I response
	return fmt.Sprintf("%s\r\nV1.0\r\nShared via tsc-bridge\r\n", model)
}

// handleShareConnection reads data from a network client.
// If it's a ~!I probe, responds with printer info so scanners can identify it.
// Otherwise, forwards the TSPL2 data to the local printer.
func handleShareConnection(conn net.Conn) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	log.Printf("[share] Connection from %s", remote)

	// Read initial bytes with a short timeout to detect probes
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	head := make([]byte, 16)
	n, err := conn.Read(head)
	if err != nil || n == 0 {
		return
	}
	head = head[:n]

	// Check if this is a ~!I probe (used by network scanners)
	trimmed := bytes.TrimSpace(head)
	if bytes.Equal(trimmed, []byte("~!I")) || strings.HasPrefix(string(trimmed), "~!I") {
		response := getShareModelResponse()
		conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
		conn.Write([]byte(response))
		log.Printf("[share] Probe from %s — responded with model info", remote)
		return
	}

	// Not a probe — it's a print job. Read the rest of the data.
	conn.SetReadDeadline(time.Now().Add(shareReadTimeout))
	rest, err := io.ReadAll(io.LimitReader(conn, shareMaxBytes))
	if err != nil && err != io.EOF {
		log.Printf("[share] Read error from %s: %v", remote, err)
	}

	data := append(head, rest...)
	if len(data) == 0 {
		return
	}

	printerName := getSharePrinterName()
	if printerName == "" {
		log.Printf("[share] No local printer available for job from %s", remote)
		return
	}

	if err := rawPrint(printerName, data); err != nil {
		log.Printf("[share] Print failed for job from %s: %v", remote, err)
		return
	}

	shareJobCount.Add(1)
	log.Printf("[share] Printed %d bytes from %s to %s", len(data), remote, printerName)
}

// toggleShare enables or disables printer sharing dynamically.
func toggleShare(enable bool) {
	if enable {
		cfg := getConfig()
		startShareListener(cfg.SharePort)
	} else {
		stopShareListener()
	}
}
