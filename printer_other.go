//go:build !windows

package main

/*
#cgo LDFLAGS: -lusb-1.0
#cgo CFLAGS: -I/opt/homebrew/include/libusb-1.0
#cgo LDFLAGS: -L/opt/homebrew/lib
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

// listLocalPrinters checks for TSC USB devices and CUPS printers.
func listLocalPrinters() ([]PrinterInfo, error) {
	var printers []PrinterInfo

	// Check if TSC device is connected via libusb
	if C.usb_device_exists(C.int(tscVendorID), C.int(tscProductID)) == 0 {
		printers = append(printers, PrinterInfo{
			Name:  "TSC-TDP-244-USB",
			Type:  "usb",
			Model: "TDP-244 Plus",
		})
	}

	// Also check CUPS for any TSC printers
	out, err := exec.Command("lpstat", "-a").Output()
	if err == nil {
		tscKeywords := []string{"tsc", "tdp", "te2", "te3"}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			name := strings.Fields(line)[0]
			lower := strings.ToLower(name)
			for _, kw := range tscKeywords {
				if strings.Contains(lower, kw) {
					printers = append(printers, PrinterInfo{
						Name: name,
						Type: "cups",
					})
					break
				}
			}
		}
	}

	return printers, nil
}

// rawPrint sends data directly to the printer via USB (bypassing CUPS).
func rawPrint(printerName string, data []byte) error {
	if strings.HasPrefix(printerName, "(simulated)") {
		fmt.Printf("[simulate] Would print %d bytes to %s\n", len(data), printerName)
		fmt.Printf("[simulate] Commands:\n%s\n", string(data))
		return nil
	}

	// Direct USB: bypass CUPS entirely
	if strings.Contains(printerName, "USB") || strings.Contains(strings.ToLower(printerName), "tsc") {
		cData := C.CBytes(data)
		defer C.free(cData)

		ret := C.usb_raw_print(
			C.int(tscVendorID),
			C.int(tscProductID),
			(*C.uchar)(cData),
			C.int(len(data)),
		)

		switch ret {
		case -1:
			return fmt.Errorf("libusb init failed")
		case -2:
			return fmt.Errorf("TSC printer not found on USB (vendor=%04x product=%04x)", tscVendorID, tscProductID)
		case -3:
			return fmt.Errorf("cannot claim USB interface — close other apps using the printer")
		case -4:
			return fmt.Errorf("USB transfer failed")
		default:
			if ret > 0 {
				fmt.Printf("[print-usb] Sent %d/%d bytes directly via USB to %s\n", int(ret), len(data), printerName)
				return nil
			}
			return fmt.Errorf("USB transfer returned %d", int(ret))
		}
	}

	// Fallback: CUPS lp command
	cmd := exec.Command("lp", "-d", printerName, "-o", "raw")
	cmd.Stdin = strings.NewReader(string(data))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("lp failed: %v — %s", err, string(output))
	}
	fmt.Printf("[print-cups] Sent %d bytes via lp to %s\n", len(data), printerName)
	return nil
}
