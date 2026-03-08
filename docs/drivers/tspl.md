# TSPL Driver

The TSPL driver generates TSPL2 commands for TSC thermal label printers.
This is the primary built-in driver and serves as the reference implementation.

## Supported Printers

- TSC TDP-244 Plus
- TSC TDP-247
- TSC TTP-245 Plus
- TSC TE200, TE210, TE300 series
- TSC Alpha series
- Any TSC printer supporting TSPL2

## Supported Features

| Feature | Supported |
|---------|-----------|
| Text | Yes |
| Code 128 | Yes |
| Code 39 | Yes |
| EAN-13 | Yes |
| UPC-A | Yes |
| QR Code | Yes (raster) |
| Images | Yes (raster, PCX) |
| Rotation | 0, 90, 180, 270 |
| Max DPI | 600 |
| Multi-label | Yes |

## Implementation

The driver is implemented in `tspl_renderer.go`. Key functions:

- `RenderBulkTSPL()` -- renders multiple rows into TSPL commands
- `renderTSPLLabel()` -- renders a single label
- `tsplText()` -- text rendering with font selection
- `tsplBarcode()` -- barcode rendering
- `tsplQRCode()` -- QR code as raster bitmap
- `tsplImage()` -- image rendering as PCX or bitmap

## TSPL Command Reference

The driver produces standard TSPL2 commands:

```
SIZE w mm, h mm     -- label dimensions
GAP g mm, 0 mm      -- gap between labels
CLS                  -- clear buffer
TEXT x,y,"f",r,xm,ym,"data"  -- text
BARCODE x,y,"type",h,hr,r,n,m,"data"  -- barcode
BITMAP x,y,w,h,mode,data  -- raster image
PRINT n,c            -- print n sets of c copies
```

## DPI Handling

TSPL uses dots as the native unit. The driver converts millimeters to dots:

```
dots = mm * DPI / 25.4
```

DPI is auto-detected on macOS (via CUPS PPD) and Windows (via WMI). On Linux,
it must be set manually in the configuration.

## Testing

The TSPL driver has been tested on:

- TSC TDP-244 Plus (203 DPI, USB)
- TSC TE200 (203 DPI, USB)

To run the driver tests:

```sh
go test -run TestTSPL ./...
```
