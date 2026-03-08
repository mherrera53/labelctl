package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"fyne.io/systray"
)

// runTray starts the system tray icon and blocks until the user selects "Salir".
// If autoOpen is true, it opens the dashboard via the webview abstraction.
// If systray fails, falls back to blocking forever so the HTTP service stays alive.
func runTray(dashURL string, autoOpen bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[tray] PANIC in systray: %v — falling back to headless mode", r)
			select {} // keep service alive
		}
	}()

	systray.Run(
		func() { onTrayReady(dashURL, autoOpen) },
		onTrayExit,
	)
}

// trayPrinterInfoText builds the printer count + DPI summary string for the menu.
func trayPrinterInfoText() string {
	printers, err := listAllPrinters()
	if err != nil || len(printers) == 0 {
		return "0 impresora(s)"
	}

	count := len(printers)

	// Find the most common DPI among detected printers
	dpiCounts := map[int]int{}
	for _, p := range printers {
		dpi := GetPrinterDPI(p.Name)
		dpiCounts[dpi]++
	}
	// Pick DPI with highest count
	bestDPI := defaultDPI
	bestCount := 0
	for dpi, c := range dpiCounts {
		if c > bestCount {
			bestDPI = dpi
			bestCount = c
		}
	}

	return fmt.Sprintf("%d impresora(s) — %d DPI", count, bestDPI)
}

// trayTooltipText builds the tooltip string with whitelabel branding.
func trayTooltipText() string {
	cfg := getConfig()
	if cfg.Whitelabel.Name != "" {
		return fmt.Sprintf("TSC Bridge — %s", cfg.Whitelabel.Name)
	}
	return "TSC Bridge v" + version
}

// sendTestPrint sends a basic TSPL test label to the given printer.
// Uses the default preset to generate the label layout.
func sendTestPrint(printerName string) error {
	cfg := getConfig()
	presetID := cfg.DefaultPreset
	if presetID == "" {
		presetID = "matrix-3x1-30x22"
	}

	preset := getPresetByID(presetID, cfg.CustomPresets)
	if preset == nil {
		// Fallback to a simple test without preset
		tspl := "SIZE 40 mm, 25 mm\r\nGAP 3 mm, 0 mm\r\nDIRECTION 0,0\r\nSPEED 4\r\nDENSITY 8\r\nSET TEAR ON\r\nCLS\r\n"
		tspl += "TEXT 16,16,\"3\",0,1,1,\"TSC BRIDGE TEST\"\r\n"
		tspl += fmt.Sprintf("TEXT 16,56,\"2\",0,1,1,\"v%s\"\r\n", version)
		tspl += "BARCODE 16,96,\"128\",48,1,0,2,2,\"TSCBRIDGE\"\r\n"
		tspl += "PRINT 1\r\n"
		return sendToPrinter(printerName, []byte(tspl))
	}

	// Generate test label using preset (same logic as handleTestPrint)
	tspl := generatePresetHeader(preset)
	for i := 0; i < preset.Columns; i++ {
		x := 8
		if i < len(preset.ColOffsets) {
			x = preset.ColOffsets[i]
		}
		tspl += fmt.Sprintf("TEXT %d,16,\"3\",0,1,1,\"TEST COL %d\"\r\n", x, i+1)
		tspl += fmt.Sprintf("BARCODE %d,48,\"128\",40,1,0,2,2,\"12345%d\"\r\n", x, i)
	}
	tspl += "PRINT 1\r\n"

	return sendToPrinter(printerName, []byte(tspl))
}

// sendToPrinter routes print data to the correct backend (network or local).
func sendToPrinter(printerName string, data []byte) error {
	allPrinters, _ := listAllPrinters()
	var targetPrinter *PrinterInfo

	if printerName == "" {
		if len(allPrinters) > 0 {
			targetPrinter = &allPrinters[0]
			printerName = targetPrinter.Name
		}
	} else {
		targetPrinter = findPrinter(printerName, allPrinters)
	}

	if printerName == "" {
		return fmt.Errorf("no printer available")
	}

	if targetPrinter != nil && (targetPrinter.Type == "network" || targetPrinter.Type == "manual" || targetPrinter.Type == "raw") {
		return networkRawPrint(targetPrinter.Address, data)
	}
	return rawPrint(printerName, data)
}

func onTrayReady(dashURL string, autoOpen bool) {
	systray.SetTooltip(trayTooltipText())
	systray.SetIcon(generateAppIcon(32))

	// --- Printer info (disabled) ---
	mPrinterInfo := systray.AddMenuItem(trayPrinterInfoText(), "Impresoras detectadas")
	mPrinterInfo.Disable()

	systray.AddSeparator()

	// --- Dashboard ---
	mOpen := systray.AddMenuItem("Abrir Dashboard", "Abrir panel de control")

	// --- Re-detect printers ---
	mRescan := systray.AddMenuItem("Re-detectar impresoras", "Buscar impresoras en la red")

	// --- Test Print ---
	mTestPrint := systray.AddMenuItem("Test Print", "Imprimir etiqueta de prueba")

	systray.AddSeparator()

	// --- Auto-start toggle ---
	mAutoStart := systray.AddMenuItemCheckbox(
		"Iniciar con el sistema",
		"Iniciar TSC Bridge al encender el equipo",
		isAutoStartEnabled(),
	)

	systray.AddSeparator()

	// --- Port & version info (disabled) ---
	cfg := getConfig()
	mInfo := systray.AddMenuItem(
		fmt.Sprintf("Puerto %d — v%s", cfg.Port, version),
		"Informacion del servicio",
	)
	mInfo.Disable()

	systray.AddSeparator()

	// --- Quit ---
	mQuit := systray.AddMenuItem("Salir", "Detener servicio y salir")

	// Auto-open dashboard on first launch
	if autoOpen {
		go func() {
			time.Sleep(2 * time.Second) // let HTTP server goroutine start accepting
			showDashboard(dashURL)
		}()
	}

	// Background goroutine: update printer info every 10 seconds
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			text := trayPrinterInfoText()
			mPrinterInfo.SetTitle(text)
			mPrinterInfo.SetTooltip("Impresoras detectadas")
		}
	}()

	// Event loop
	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				go showDashboard(dashURL)

			case <-mRescan.ClickedCh:
				go func() {
					log.Printf("[tray] Re-detecting printers...")
					mRescan.SetTitle("Escaneando...")
					mRescan.Disable()

					refreshNetworkPrinters()
					DetectAllPrinterDPIs()

					text := trayPrinterInfoText()
					mPrinterInfo.SetTitle(text)
					mRescan.SetTitle("Re-detectar impresoras")
					mRescan.Enable()
					log.Printf("[tray] Re-detection complete: %s", text)
				}()

			case <-mTestPrint.ClickedCh:
				go func() {
					cfg := getConfig()
					printerName := cfg.DefaultPrinter
					log.Printf("[tray] Test print requested (printer=%q)", printerName)
					if err := sendTestPrint(printerName); err != nil {
						log.Printf("[tray] Test print failed: %v", err)
					} else {
						log.Printf("[tray] Test print sent successfully")
					}
				}()

			case <-mAutoStart.ClickedCh:
				enabled := !isAutoStartEnabled()
				if err := setAutoStart(enabled); err != nil {
					log.Printf("[tray] autostart toggle error: %v", err)
				} else {
					if enabled {
						mAutoStart.Check()
					} else {
						mAutoStart.Uncheck()
					}
					configMu.Lock()
					appConfig.AutoStart = enabled
					configMu.Unlock()
					saveConfig()
					log.Printf("[tray] autostart toggled: %v", enabled)
				}

			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func onTrayExit() {
	log.Printf("System tray exit — shutting down")
	destroyWebview()
	os.Exit(0)
}
