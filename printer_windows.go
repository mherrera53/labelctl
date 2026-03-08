//go:build windows

package main

import (
	"encoding/csv"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	winspool        = syscall.NewLazyDLL("winspool.drv")
	openPrinterW    = winspool.NewProc("OpenPrinterW")
	startDocPrinterW = winspool.NewProc("StartDocPrinterW")
	startPagePrinter = winspool.NewProc("StartPagePrinter")
	writePrinter     = winspool.NewProc("WritePrinter")
	endPagePrinter   = winspool.NewProc("EndPagePrinter")
	endDocPrinter    = winspool.NewProc("EndDocPrinter")
	closePrinter     = winspool.NewProc("ClosePrinter")
)

// DOC_INFO_1W matches the Windows DOC_INFO_1W structure.
type docInfo1 struct {
	DocName    *uint16
	OutputFile *uint16
	Datatype   *uint16
}

// hideWindow sets CREATE_NO_WINDOW flag to prevent console flash on Windows.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

// hideWindowCmd is an alias used by cross-platform code (browser.go).
func hideWindowCmd(cmd *exec.Cmd) { hideWindow(cmd) }

// listLocalPrinters lists ALL installed printers via Print Spooler with status.
func listLocalPrinters() ([]PrinterInfo, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		`Get-Printer | Select-Object Name,PrinterStatus,PortName | ConvertTo-Csv -NoTypeInformation`)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return []PrinterInfo{}, nil
	}

	var printers []PrinterInfo
	reader := csv.NewReader(strings.NewReader(strings.TrimSpace(string(out))))
	records, csvErr := reader.ReadAll()
	if csvErr != nil {
		return []PrinterInfo{}, nil
	}
	for i, fields := range records {
		if i == 0 { // skip CSV header
			continue
		}
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		statusStr := strings.TrimSpace(fields[1])
		if name == "" {
			continue
		}

		// PrinterStatus: 0=Normal, 1=Paused, 2=Error, 3=PendingDeletion, 4=PaperJam, 5=PaperOut, 6=ManualFeed, 7=PaperProblem, etc.
		online := statusStr == "0" || statusStr == "Normal"
		status := "idle"
		if !online {
			status = "offline"
		}

		printers = append(printers, PrinterInfo{
			Name:   name,
			Type:   "spooler",
			Online: online,
			Status: status,
		})
	}
	return printers, nil
}

// rawPrint sends raw bytes to a printer via the Windows Print Spooler API.
// Data is sent in chunks to avoid overflowing the printer's input buffer
// (TSC TDP-244 and similar models have ~32KB buffers).
func rawPrint(printerName string, data []byte) error {
	fmt.Printf("[print-win] Opening printer %q (%d bytes to send)\n", printerName, len(data))
	pName, err := syscall.UTF16PtrFromString(printerName)
	if err != nil {
		return fmt.Errorf("invalid printer name: %w", err)
	}

	var handle uintptr
	ret, _, errno := openPrinterW.Call(
		uintptr(unsafe.Pointer(pName)),
		uintptr(unsafe.Pointer(&handle)),
		0,
	)
	if ret == 0 {
		return fmt.Errorf("OpenPrinterW failed for %q: %v (errno %d)", printerName, errno, errno)
	}
	defer closePrinter.Call(handle)

	docName, _ := syscall.UTF16PtrFromString("TSC Label")
	datatype, _ := syscall.UTF16PtrFromString("RAW")

	di := docInfo1{
		DocName:    docName,
		OutputFile: nil,
		Datatype:   datatype,
	}

	ret, _, errno = startDocPrinterW.Call(
		handle,
		1,
		uintptr(unsafe.Pointer(&di)),
	)
	if ret == 0 {
		return fmt.Errorf("StartDocPrinterW failed: %v", errno)
	}
	defer endDocPrinter.Call(handle)

	ret, _, errno = startPagePrinter.Call(handle)
	if ret == 0 {
		return fmt.Errorf("StartPagePrinter failed: %v", errno)
	}
	defer endPagePrinter.Call(handle)

	// Send data in chunks to avoid overflowing the printer's input buffer.
	// TSC thermal printers have limited buffers (~32KB); sending large
	// TSPL streams in one shot causes the printer to fall out of command
	// mode and print raw text/hex instead of interpreting commands.
	const chunkSize = 4096
	totalWritten := 0
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[offset:end]

		var written uint32
		ret, _, errno = writePrinter.Call(
			handle,
			uintptr(unsafe.Pointer(&chunk[0])),
			uintptr(len(chunk)),
			uintptr(unsafe.Pointer(&written)),
		)
		if ret == 0 {
			return fmt.Errorf("WritePrinter failed at offset %d/%d: %v", offset, len(data), errno)
		}
		totalWritten += int(written)
	}

	fmt.Printf("[print-win] Sent %d bytes in %d chunks to %s\n",
		totalWritten, (len(data)+chunkSize-1)/chunkSize, printerName)
	return nil
}
