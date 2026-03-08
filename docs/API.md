# API Reference

TSC Bridge exposes an HTTP API on `127.0.0.1:PORT` (default port: 9638). All
endpoints accept and return JSON unless otherwise noted.

## Authentication

Most endpoints require no authentication when accessed from localhost. When
CORS is configured, requests from allowed origins are accepted. The `auth`
endpoints manage the connection to the backend server.

## Endpoints

### Status

#### `GET /status`

Returns bridge status, version, and connection information.

Response:

```json
{
  "status": "ok",
  "version": "3.0.0",
  "uptime": "2h15m",
  "printer": "TSC_TDP-244_Plus",
  "dpi": 203,
  "backend": "https://api.example.com",
  "authenticated": true
}
```

### Printers

#### `GET /printers`

Lists all detected printers with their type, status, and capabilities.

Response:

```json
{
  "printers": [
    {
      "name": "TSC_TDP-244_Plus",
      "type": "usb",
      "model": "TDP-244 Plus",
      "online": true,
      "status": "idle"
    },
    {
      "name": "HP_LaserJet",
      "type": "cups",
      "online": true,
      "status": "idle"
    }
  ]
}
```

Printer types: `usb` (direct USB via libusb), `cups` (macOS/Linux CUPS),
`windows` (Win32 raw).

### Printing

#### `POST /print`

Sends raw printer commands to a specific printer.

Request:

```json
{
  "printer": "TSC_TDP-244_Plus",
  "data": "SIZE 50 mm, 30 mm\nGAP 3 mm, 0 mm\nCLS\nTEXT 10,10,\"3\",0,1,1,\"Hello\"\nPRINT 1,1\n"
}
```

Response:

```json
{
  "status": "ok",
  "bytes_sent": 85
}
```

#### `POST /batch-pdf`

Generates a multi-page PDF from a label template and row data.

Request:

```json
{
  "template_id": "uuid-of-template",
  "rows": [
    { "nombre": "Juan Garcia", "codigo": "12345" },
    { "nombre": "Maria Lopez", "codigo": "67890" }
  ],
  "mapping": {}
}
```

Alternative template sources (mutually exclusive with `template_id`):

- `"template_file": "/path/to/template.json"` -- local file
- `"template_json": { ... }` -- inline template object

Response (default): binary PDF file with `Content-Type: application/pdf`.

Response (`?mode=url`):

```json
{
  "url": "/output/batch_1709856000000.pdf",
  "filename": "batch_2.pdf",
  "pages": 2
}
```

#### `POST /batch-tspl`

Generates TSPL commands from a template and optionally prints them.

Request:

```json
{
  "template_id": "uuid-of-template",
  "rows": [
    { "nombre": "Juan Garcia", "codigo": "12345" }
  ],
  "printer": "TSC_TDP-244_Plus",
  "copies": 1,
  "mode": "print",
  "dpi": 203
}
```

Modes:

| Mode | Behavior |
|------|----------|
| `print` | Sends commands directly to the printer (default) |
| `preview` | Returns TSPL text without printing |
| `raster` | Generates a bitmap preview image |

Response (`mode: print`):

```json
{
  "status": "ok",
  "labels": 1,
  "printer": "TSC_TDP-244_Plus"
}
```

Response (`mode: preview`):

```json
{
  "tspl": "SIZE 50 mm, 30 mm\n..."
}
```

### Templates

#### `GET /templates`

Lists locally stored templates.

Response:

```json
{
  "templates": [
    {
      "id": "local-uuid",
      "name": "Product Label 50x30",
      "width": 50,
      "height": 30,
      "fields": [...]
    }
  ]
}
```

#### `POST /templates`

Saves a template locally.

Request: template object (same format as the label schema).

#### `DELETE /templates/{id}`

Deletes a local template.

### Files

#### `GET /output/{filename}`

Serves a generated file (PDF, image). By default, serves inline for embedding
in iframes.

Query parameters:

| Parameter | Effect |
|-----------|--------|
| `dl=1` | Forces `Content-Disposition: attachment` (download) |

#### `POST /upload-pdf`

Uploads a PDF file for processing.

### Configuration

#### `GET /config`

Returns the current configuration (sensitive fields redacted).

#### `POST /config`

Updates configuration. Body: partial config object (merged with existing).

### Authentication

#### `POST /auth/connect`

Connects to a backend server.

Request:

```json
{
  "url": "https://api.example.com",
  "token": "auth-token"
}
```

#### `POST /auth/disconnect`

Disconnects from the backend server.

#### `GET /auth/status`

Returns authentication state.

### Dashboard

#### `GET /dashboard`

Serves the embedded HTML dashboard.

### DPI

#### `GET /dpi`

Returns auto-detected DPI for the selected printer.

Response:

```json
{
  "dpi": 203,
  "source": "driver",
  "printer": "TSC_TDP-244_Plus"
}
```

### Drivers

#### `GET /drivers`

Returns TSC driver installation status (macOS and Windows only).

## Error Responses

All errors return a JSON object with an `error` field:

```json
{
  "error": "printer not found: NonExistent_Printer"
}
```

HTTP status codes:

| Code | Meaning |
|------|---------|
| 200 | Success |
| 400 | Bad request (invalid JSON, missing fields) |
| 404 | Resource not found |
| 405 | Method not allowed |
| 500 | Internal server error |
| 502 | Bad gateway (backend API error) |

## CORS

The bridge sets CORS headers based on the configured allowed origins. By
default, requests from `localhost` and `127.0.0.1` are allowed on any port.

Additional origins can be configured in `~/.tsc-bridge/config.json`:

```json
{
  "cors": {
    "origins": ["https://app.example.com", "https://admin.example.com"]
  }
}
```
