//go:build !windows && !crossbuild

package main

/*
#cgo CFLAGS: -I/opt/homebrew/include/libusb-1.0 -I/usr/local/include/libusb-1.0
#cgo LDFLAGS: -L/opt/homebrew/lib -L/usr/local/lib -lusb-1.0
#include <libusb.h>
#include <stdlib.h>
#include <stdio.h>

// Check if a USB device exists (returns 0 if found, -1 if not)
int usb_device_exists(int vendor_id, int product_id) {
    libusb_context *ctx = NULL;
    if (libusb_init(&ctx) < 0) return -1;
    libusb_device_handle *handle = libusb_open_device_with_vid_pid(ctx, vendor_id, product_id);
    if (handle == NULL) {
        libusb_exit(ctx);
        return -1;
    }
    libusb_close(handle);
    libusb_exit(ctx);
    return 0;
}

// Find the bulk OUT endpoint for a given interface
unsigned char find_bulk_out_endpoint(libusb_device *dev, int interface_num) {
    struct libusb_config_descriptor *config;
    if (libusb_get_active_config_descriptor(dev, &config) != 0) return 0;

    unsigned char ep = 0;
    if (interface_num < config->bNumInterfaces) {
        const struct libusb_interface *iface = &config->interface[interface_num];
        if (iface->num_altsetting > 0) {
            const struct libusb_interface_descriptor *desc = &iface->altsetting[0];
            for (int i = 0; i < desc->bNumEndpoints; i++) {
                const struct libusb_endpoint_descriptor *epd = &desc->endpoint[i];
                // Bulk OUT: transfer type = bulk (0x02), direction = OUT (0x00)
                if ((epd->bmAttributes & 0x03) == 0x02 &&
                    (epd->bEndpointAddress & 0x80) == 0x00) {
                    ep = epd->bEndpointAddress;
                    fprintf(stderr, "[usb] Found bulk OUT endpoint: 0x%02x\n", ep);
                    break;
                }
            }
        }
    }
    libusb_free_config_descriptor(config);
    return ep;
}

// Send raw data to a USB printer by vendor/product ID
int usb_raw_print(int vendor_id, int product_id, const unsigned char *data, int length) {
    libusb_context *ctx = NULL;
    libusb_device_handle *handle = NULL;
    int ret;

    ret = libusb_init(&ctx);
    if (ret < 0) return -1;

    handle = libusb_open_device_with_vid_pid(ctx, vendor_id, product_id);
    if (handle == NULL) {
        libusb_exit(ctx);
        return -2; // device not found
    }

    // Detach kernel driver if active (macOS CUPS grabs the device)
    if (libusb_kernel_driver_active(handle, 0) == 1) {
        libusb_detach_kernel_driver(handle, 0);
    }

    ret = libusb_claim_interface(handle, 0);
    if (ret < 0) {
        libusb_close(handle);
        libusb_exit(ctx);
        return -3; // cannot claim interface
    }

    // Auto-detect the bulk OUT endpoint
    libusb_device *dev = libusb_get_device(handle);
    unsigned char endpoint = find_bulk_out_endpoint(dev, 0);
    if (endpoint == 0) {
        fprintf(stderr, "[usb] No bulk OUT endpoint found, trying 0x01 and 0x02\n");
        // Try common endpoints as fallback
        int transferred = 0;
        ret = libusb_bulk_transfer(handle, 0x02, (unsigned char *)data, length, &transferred, 5000);
        if (ret < 0) {
            ret = libusb_bulk_transfer(handle, 0x01, (unsigned char *)data, length, &transferred, 5000);
        }
        libusb_release_interface(handle, 0);
        libusb_close(handle);
        libusb_exit(ctx);
        if (ret < 0) return -4;
        return transferred;
    }

    int transferred = 0;
    ret = libusb_bulk_transfer(handle, endpoint, (unsigned char *)data, length, &transferred, 5000);

    libusb_release_interface(handle, 0);
    libusb_close(handle);
    libusb_exit(ctx);

    if (ret < 0) return -4; // transfer failed
    return transferred;
}
*/
import "C"

import (
	"fmt"
	"os/exec"
	"strings"
)

const (
	tscVendorID  = 0x1203
	tscProductID = 0x0133 // TDP-244 Plus
)

// C_usb_device_exists checks if the known TSC USB device is connected.
// Exported as a Go function so other files (driver_darwin.go) can use it without importing C.
func C_usb_device_exists() bool {
	return C.usb_device_exists(C.int(tscVendorID), C.int(tscProductID)) == 0
}

// listLocalPrinters lists ALL printers: USB (direct) + all CUPS printers with status.
func listLocalPrinters() ([]PrinterInfo, error) {
	var printers []PrinterInfo

	// Check if TSC USB device is physically connected via libusb
	usbConnected := C.usb_device_exists(C.int(tscVendorID), C.int(tscProductID)) == 0

	// Parse CUPS printer status: "lpstat -p" gives status of each printer
	statusMap := make(map[string]string) // name -> "idle"|"disabled"|"printing"
	if out, err := exec.Command("lpstat", "-p").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "la impresora ") && !strings.HasPrefix(line, "impresora ") && !strings.HasPrefix(line, "printer ") {
				continue
			}
			// Parse: "la impresora NAME está inactiva" or "printer NAME is idle"
			// or: "impresora NAME desactivada"
			fields := strings.Fields(line)
			if len(fields) < 3 {
				continue
			}
			// Name is the 3rd field (after "la impresora" or "printer")
			var name string
			if strings.HasPrefix(line, "la impresora ") || strings.HasPrefix(line, "impresora ") {
				// Spanish: "la impresora X está inactiva" or "impresora X desactivada"
				for _, f := range fields {
					if f != "la" && f != "impresora" {
						name = f
						break
					}
				}
			} else {
				// English: "printer X is idle"
				name = fields[1]
			}
			if name == "" {
				continue
			}

			lower := strings.ToLower(line)
			if strings.Contains(lower, "desactivad") || strings.Contains(lower, "disabled") {
				statusMap[name] = "disabled"
			} else if strings.Contains(lower, "imprimiendo") || strings.Contains(lower, "printing") {
				statusMap[name] = "printing"
			} else {
				statusMap[name] = "idle"
			}
		}
	}

	// List ALL CUPS printers (not just TSC)
	out, err := exec.Command("lpstat", "-a").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			name := strings.Fields(line)[0]
			lower := strings.ToLower(name)

			status := statusMap[name]
			online := status != "disabled"

			// Determine type — TSC printers may have USB direct access
			pType := "cups"
			isTSC := false
			for _, kw := range []string{"tsc", "tdp", "ttp", "te2", "te3"} {
				if strings.Contains(lower, kw) {
					isTSC = true
					break
				}
			}

			if isTSC && usbConnected {
				// TSC printer with USB device present — mark as USB and online
				printers = append(printers, PrinterInfo{
					Name:   name,
					Type:   "usb",
					Model:  "TDP-244 Plus",
					Online: true,
					Status: status,
				})
			} else if isTSC && !usbConnected {
				// TSC printer in CUPS but USB not connected
				printers = append(printers, PrinterInfo{
					Name:   name,
					Type:   "cups",
					Online: false,
					Status: "disconnected",
				})
			} else {
				printers = append(printers, PrinterInfo{
					Name:   name,
					Type:   pType,
					Online: online,
					Status: status,
				})
			}
		}
	}

	return printers, nil
}

// rawPrint sends data directly to the printer. Tries USB first, falls back to CUPS.
func rawPrint(printerName string, data []byte) error {
	if strings.HasPrefix(printerName, "(simulated)") {
		fmt.Printf("[simulate] Would print %d bytes to %s\n", len(data), printerName)
		fmt.Printf("[simulate] Commands:\n%s\n", string(data))
		return nil
	}

	// Try direct USB first for TSC printers
	isTSC := strings.Contains(printerName, "USB") || strings.Contains(strings.ToLower(printerName), "tsc")
	if isTSC {
		cData := C.CBytes(data)
		defer C.free(cData)

		ret := C.usb_raw_print(
			C.int(tscVendorID),
			C.int(tscProductID),
			(*C.uchar)(cData),
			C.int(len(data)),
		)

		if ret > 0 {
			fmt.Printf("[print-usb] Sent %d/%d bytes directly via USB to %s\n", int(ret), len(data), printerName)
			return nil
		}
		// USB failed — fall through to CUPS
		fmt.Printf("[print-usb] USB not available (ret=%d), falling back to CUPS for %s\n", int(ret), printerName)
	}

	// CUPS fallback: resolve the actual CUPS printer name
	cupsName := printerName
	// Strip "-USB" suffix if present — CUPS doesn't use it
	cupsName = strings.TrimSuffix(cupsName, "-USB")

	// If we still can't find the printer, search CUPS for any TSC printer
	if isTSC {
		if out, err := exec.Command("lpstat", "-a").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				fields := strings.Fields(strings.TrimSpace(line))
				if len(fields) == 0 {
					continue
				}
				name := fields[0]
				lower := strings.ToLower(name)
				if strings.Contains(lower, "tsc") || strings.Contains(lower, "tdp") || strings.Contains(lower, "ttp") {
					cupsName = name
					break
				}
			}
		}
	}

	cmd := exec.Command("lp", "-d", cupsName, "-o", "raw")
	cmd.Stdin = strings.NewReader(string(data))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("lp -d %s failed: %v — %s", cupsName, err, string(output))
	}
	fmt.Printf("[print-cups] Sent %d bytes via lp to %s\n", len(data), cupsName)
	return nil
}
