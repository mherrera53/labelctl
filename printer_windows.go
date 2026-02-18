//go:build windows

package main

import (
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

// listLocalPrinters lists installed printers whose name contains "TSC" via Print Spooler.
func listLocalPrinters() ([]PrinterInfo, error) {
	cmd := exec.Command("powershell", "-NoProfile", "-Command",
		`Get-Printer | Where-Object {$_.Name -match 'TSC'} | Select-Object -ExpandProperty Name`)
	out, err := cmd.Output()
	if err != nil {
		return []PrinterInfo{}, nil
	}

	var printers []PrinterInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			printers = append(printers, PrinterInfo{
				Name: name,
				Type: "spooler",
			})
		}
	}
	return printers, nil
}

// rawPrint sends raw bytes to a printer via the Windows Print Spooler API.
func rawPrint(printerName string, data []byte) error {
	pName, err := syscall.UTF16PtrFromString(printerName)
	if err != nil {
		return fmt.Errorf("invalid printer name: %w", err)
	}

	var handle uintptr
	ret, _, _ := openPrinterW.Call(
		uintptr(unsafe.Pointer(pName)),
		uintptr(unsafe.Pointer(&handle)),
		0,
	)
	if ret == 0 {
		return fmt.Errorf("OpenPrinterW failed for %q", printerName)
	}
	defer closePrinter.Call(handle)

	docName, _ := syscall.UTF16PtrFromString("TSC Label")
	datatype, _ := syscall.UTF16PtrFromString("RAW")

	di := docInfo1{
		DocName:    docName,
		OutputFile: nil,
		Datatype:   datatype,
	}

	ret, _, _ = startDocPrinterW.Call(
		handle,
		1,
		uintptr(unsafe.Pointer(&di)),
	)
	if ret == 0 {
		return fmt.Errorf("StartDocPrinterW failed")
	}
	defer endDocPrinter.Call(handle)

	ret, _, _ = startPagePrinter.Call(handle)
	if ret == 0 {
		return fmt.Errorf("StartPagePrinter failed")
	}
	defer endPagePrinter.Call(handle)

	var written uint32
	ret, _, _ = writePrinter.Call(
		handle,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(len(data)),
		uintptr(unsafe.Pointer(&written)),
	)
	if ret == 0 {
		return fmt.Errorf("WritePrinter failed")
	}

	return nil
}
