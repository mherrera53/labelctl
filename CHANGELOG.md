# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [3.0.0] - 2026-03-08

### Added

- Native macOS WKWebView dashboard window (no browser dependency)
- System tray integration for macOS, Windows, and Linux
- Interactive label designer with drag-and-drop elements
- TSPL2 renderer for TSC thermal printers
- PDF renderer with TrueType font support and vector graphics
- Batch printing from Excel/CSV files with field mapping
- QR code generation (free text, vCard, URL, custom payload)
- Barcode support (Code 128, Code 39, EAN-13, UPC-A, ITF, Codabar)
- Auto-DPI detection for TSC printers (macOS and Windows)
- Direct USB printing via libusb on macOS and Linux
- CUPS integration for macOS and Linux
- Windows raw printing via Win32 API
- Self-signed TLS certificate generation for HTTPS localhost
- AES-256 encrypted credential storage
- Whitelabel branding support (name, logo, colors)
- Autostart on login (LaunchAgent on macOS, Registry on Windows)
- Network printer discovery
- Label border styles: simple, double, thick, rounded, shadow, inset,
  ornate, art deco, ticket, dashed, certificate, filigree, dotted
- Arrow key movement for designer elements (Shift for fine control)
- Auto-fit elements to canvas
- Professional application icon generation (PNG, ICO, ICNS)
- macOS .app bundle with DMG distribution
- Windows InnoSetup installer script
- Cross-compilation support (macOS to Windows/Linux)

### Changed

- Migrated from browser-only dashboard to embedded native window
- Dashboard is now compiled into the binary via `go:embed`

## [2.0.0] - 2026-02-15

### Added

- HTTP API for print job submission
- Basic TSPL command generation
- Configuration file support

## [1.0.0] - 2026-01-10

### Added

- Initial release
- Direct USB printing to TSC TDP-244 Plus
- Command-line interface
