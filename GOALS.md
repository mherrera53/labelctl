# Goals

TSC Bridge aims to be the universal thermal label printing bridge. It connects
web applications to thermal label printers through a local HTTP API, removing
the need for browser extensions, Java applets, or vendor-specific SDKs.

The following are the project goals, in order of priority from most to least
important. In case of conflict, goals higher on the list take precedence.

## 1. Reliability

Labels must print correctly, every time. A misprinted label costs time, money,
and trust. The bridge must never silently drop print jobs, corrupt label data,
or produce partial output.

## 2. Universality

Support as many thermal label printers as possible through a driver
architecture. No single vendor lock-in. The same web application should work
with TSC, Zebra, Brother, BIXOLON, Honeywell, or any other thermal printer.

## 3. Simplicity

A single binary with zero external dependencies. No runtime, no installer
wizard, no database. Drop the binary on any machine and it works. The HTTP API
is plain JSON over localhost.

## 4. Standards

Define and maintain an open label format specification that any application can
produce and any driver can consume. The format is JSON-based, human-readable,
and version-controlled.

## 5. Community

Make it easy for anyone to contribute a driver for their printer. Clear
interfaces, complete documentation, working examples, and a test harness that
validates driver correctness without physical hardware.

## 6. Cross-Platform

macOS, Windows, and Linux are first-class citizens. Platform-specific features
(USB direct printing, native windows, system tray) are implemented where
available, with graceful fallbacks where not.

## Non-Goals

- TSC Bridge is not a print server. It runs on the same machine as the
  printer, not on a remote server.
- TSC Bridge is not a label designer. It renders labels from templates and
  data. Design tools are separate concerns.
- TSC Bridge does not manage printer queues. It sends jobs and reports
  success or failure. Queue management is the operating system's job.
