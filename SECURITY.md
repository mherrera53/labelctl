# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| 3.x     | Yes       |
| < 3.0   | No        |

## Reporting a Vulnerability

If you discover a security vulnerability in TSC Bridge, please report it
responsibly. **Do not open a public GitHub issue.**

Send an email to security@abstraktgt.com with:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

You will receive an acknowledgment within 48 hours. We will work with you to
understand the issue and coordinate a fix before any public disclosure.

## Security Model

TSC Bridge runs as a local service on `127.0.0.1`. It is not designed to be
exposed to the network. The threat model assumes:

- **Trusted**: The local machine and its users
- **Untrusted**: Remote network traffic, web page JavaScript (CORS-restricted)

### CORS

The HTTP API enforces CORS headers. Only origins explicitly configured in the
bridge configuration file are allowed to make API requests.

### TLS

TSC Bridge can generate a self-signed TLS certificate for HTTPS on localhost.
This prevents mixed-content warnings when the calling web application uses
HTTPS.

### Authentication

When connected to a backend, the bridge uses token-based authentication. Tokens
are stored encrypted (AES-256) in the local configuration file.

### USB Access

Direct USB printing requires operating system permissions. On macOS, the bridge
may need to detach the kernel driver from the USB device. On Linux, the user
may need to be in the `lp` or `plugdev` group.
