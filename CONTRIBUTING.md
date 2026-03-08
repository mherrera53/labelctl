# Contributing

Thank you for your interest in contributing to TSC Bridge. This document
explains how to set up your development environment, write code, and submit
changes.

Please read our [Code of Conduct](CODE_OF_CONDUCT.md) before contributing.

## Table of Contents

- [Getting Started](#getting-started)
- [Development Environment](#development-environment)
- [Writing a Printer Driver](#writing-a-printer-driver)
- [Submitting Changes](#submitting-changes)
- [Coding Guidelines](#coding-guidelines)
- [Reporting Bugs](#reporting-bugs)
- [Suggesting Features](#suggesting-features)

## Getting Started

1. Fork the repository
2. Clone your fork:
   ```sh
   git clone https://github.com/YOUR_USERNAME/tsc-bridge.git
   cd tsc-bridge
   ```
3. Create a branch for your work:
   ```sh
   git checkout -b feature/zpl-driver
   ```

## Development Environment

### Prerequisites

- Go 1.21 or later
- CGO enabled
- Platform-specific dependencies:

| Platform | Dependencies |
|----------|-------------|
| macOS | Xcode Command Line Tools, `brew install libusb` |
| Windows | MinGW-w64 or MSYS2 |
| Linux | `libusb-1.0-0-dev`, `libgtk-3-dev`, `libappindicator3-dev` |

### Build and Run

```sh
# Build
go build -o tsc-bridge .

# Run
./tsc-bridge

# Run tests
go test ./...
```

### Cross-Compilation

To cross-compile without platform SDK headers, use the `crossbuild` tag:

```sh
# From macOS to Linux
CGO_ENABLED=1 CC="zig cc -target x86_64-linux-gnu" \
  GOOS=linux GOARCH=amd64 \
  go build -tags crossbuild -o tsc-bridge-linux .
```

### Dashboard Development

The dashboard is a single HTML file (`dashboard.html`) embedded in the binary
via `go:embed`. To iterate on the dashboard:

1. Edit `dashboard.html`
2. Rebuild: `go build -o tsc-bridge .`
3. Restart the bridge

There is no hot-reload. The dashboard must be rebuilt into the binary after
every change.

## Writing a Printer Driver

The most impactful contribution is a driver for a printer brand you have access
to. See [docs/DRIVERS.md](docs/DRIVERS.md) for the complete guide.

In summary:

1. Create a new file: `<language>_renderer.go` (e.g., `zpl_renderer.go`)
2. Implement the `Driver` interface
3. Register the driver in `init()`
4. Add tests in `<language>_renderer_test.go`
5. Add documentation in `docs/drivers/<language>.md`

### What Makes a Good Driver

- Translates the universal label format faithfully
- Handles all field types: text, barcode, QR code, image, line, rectangle
- Respects DPI settings
- Includes tests that validate output against known-good command sequences
- Documents which printer models have been tested

## Submitting Changes

### Commit Messages

Use clear, descriptive commit messages. Follow this format:

```
<type>: <short description>

<optional body explaining why, not what>
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `build`, `ci`.

Examples:
- `feat: add ZPL driver with barcode support`
- `fix: correct DPI scaling for 300dpi printers`
- `docs: add Brother QL driver development notes`

### Pull Requests

1. Ensure all tests pass: `go test ./...`
2. Run the linter if available: `golangci-lint run`
3. Push your branch and open a pull request
4. Fill in the PR template
5. Wait for review

### Code Review

All submissions require review before merging. Reviewers will check:

- Correctness of the implementation
- Test coverage for new code
- Adherence to the coding guidelines below
- Documentation for new features or drivers

## Coding Guidelines

### Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- No blank line between function signature and opening brace
- Group imports: stdlib, external, internal
- Error messages are lowercase, no trailing punctuation
- Use `log.Printf` for runtime logging with `[tag]` prefixes

### Error Handling

- Return errors, do not panic
- Wrap errors with context: `fmt.Errorf("parse template: %w", err)`
- Log errors at the point where they are handled, not where they originate

### Platform Code

- Use build tags to separate platform-specific code
- Every platform-specific file must have a corresponding `_other.go` stub
- Test on at least one platform before submitting; CI covers the rest

### Documentation

- Document exported functions and types
- Add a `docs/drivers/<language>.md` file for new drivers
- Update the README driver table

## Reporting Bugs

Open an issue using the **Bug Report** template. Include:

- TSC Bridge version (`tsc-bridge --version`)
- Operating system and version
- Printer model
- Steps to reproduce
- Expected vs. actual behavior
- Relevant log output

## Suggesting Features

Open an issue using the **Feature Request** template. Describe:

- The problem you are trying to solve
- Your proposed solution
- Alternatives you have considered

For new printer driver requests, use the **Driver Request** template.
