# Label Format Specification

Version: 1.0

This document defines the universal label format used by TSC Bridge. Any
application that produces labels in this format can use any TSC Bridge driver
to print them.

## Overview

The label format is JSON-based, inspired by [pdfme](https://pdfme.com/). It
describes:

- Page dimensions (label size)
- Field positions and sizes
- Field types (text, barcode, QR code, image, line, rectangle)
- Variable bindings for dynamic data

## Schema Structure

```json
{
  "basePdf": {
    "width": 50,
    "height": 30
  },
  "schemas": [
    [
      { "name": "field1", "type": "text", ... },
      { "name": "field2", "type": "barcodes128", ... }
    ]
  ]
}
```

### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `basePdf` | object | Yes | Label dimensions |
| `schemas` | array | Yes | Array of page schemas (one per page) |

### basePdf

| Field | Type | Unit | Description |
|-------|------|------|-------------|
| `width` | number | mm | Label width in millimeters |
| `height` | number | mm | Label height in millimeters |

### schemas

An array of arrays. Each inner array represents one page (label) and contains
an array of field objects. For single-label templates, there is one inner
array.

## Field Types

### text

Plain text rendered at a fixed position.

```json
{
  "name": "product_name",
  "type": "text",
  "position": { "x": 5, "y": 5 },
  "width": 40,
  "height": 8,
  "content": "",
  "fontSize": 12,
  "fontName": "Helvetica",
  "alignment": "left",
  "rotation": 0
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | required | Unique field identifier |
| `type` | string | required | Must be `"text"` |
| `position` | object | required | `{ "x": mm, "y": mm }` from top-left |
| `width` | number | required | Field width in mm |
| `height` | number | required | Field height in mm |
| `content` | string | `""` | Static text or default value |
| `fontSize` | number | `10` | Font size in points |
| `fontName` | string | `"Helvetica"` | Font family |
| `fontColor` | string | `"#000000"` | Text color (hex) |
| `alignment` | string | `"left"` | `"left"`, `"center"`, `"right"` |
| `rotation` | number | `0` | Rotation in degrees (0, 90, 180, 270) |
| `lineHeight` | number | `1.2` | Line height multiplier |

### multiVariableText

Text with multiple variable placeholders. Variables are enclosed in curly
braces: `{nombre} {apellido}`.

```json
{
  "name": "full_info",
  "type": "multiVariableText",
  "position": { "x": 5, "y": 5 },
  "width": 40,
  "height": 15,
  "content": "{nombre} {apellido}\n{empresa}\n{puesto}",
  "variables": ["nombre", "apellido", "empresa", "puesto"],
  "fontSize": 10
}
```

Additional fields beyond `text`:

| Field | Type | Description |
|-------|------|-------------|
| `variables` | string[] | List of variable names used in `content` |

Variable resolution: when rendering, the bridge replaces `{varname}` with the
corresponding value from the row data. If the row uses prefixed keys (e.g.,
`gafete_nombre`), the bridge matches by suffix.

### barcodes128

Code 128 barcode.

```json
{
  "name": "product_code",
  "type": "barcodes128",
  "position": { "x": 5, "y": 15 },
  "width": 40,
  "height": 10,
  "content": ""
}
```

### barcodes39

Code 39 barcode. Same structure as `barcodes128`.

### barcodesean13

EAN-13 barcode. Input must be 12 or 13 digits.

### barcodesupca

UPC-A barcode. Input must be 11 or 12 digits.

### barcodesitf

Interleaved 2 of 5 barcode. Input must be even number of digits.

### barcodescodabar

Codabar barcode.

### qrcode

QR code with configurable content type.

```json
{
  "name": "vcard_qr",
  "type": "qrcode",
  "position": { "x": 35, "y": 5 },
  "width": 12,
  "height": 12,
  "content": "{token}"
}
```

QR content can be:

- Plain text
- URL
- vCard (BEGIN:VCARD...END:VCARD)
- Custom payload with `{variable}` placeholders

### image

Raster image from a URL or base64 data.

```json
{
  "name": "logo",
  "type": "image",
  "position": { "x": 2, "y": 2 },
  "width": 15,
  "height": 15,
  "content": "https://example.com/logo.png"
}
```

Content formats: HTTP/HTTPS URL, base64-encoded PNG/JPEG, or data URI.

### line

Horizontal or vertical line.

```json
{
  "name": "separator",
  "type": "line",
  "position": { "x": 0, "y": 14 },
  "width": 50,
  "height": 0.3
}
```

### rectangle

Rectangular border or filled box.

```json
{
  "name": "border",
  "type": "rectangle",
  "position": { "x": 1, "y": 1 },
  "width": 48,
  "height": 28,
  "strokeWidth": 0.5
}
```

## Variable Binding

When printing, the application provides row data as key-value pairs:

```json
{
  "nombre": "Juan",
  "apellido": "Garcia",
  "empresa": "Acme Corp",
  "codigo": "1234567890"
}
```

The bridge matches row keys to field names using this precedence:

1. **Exact match**: `row["nombre"]` for field named `nombre`
2. **Suffix match**: `row["gafete_nombre"]` matches field `nombre`
3. **Normalized match**: `row["gafete.nombre"]` matches field `gafete_nombre`
   (dots and underscores are interchangeable)

## Coordinate System

- Origin: top-left corner of the label
- Units: millimeters
- X axis: left to right
- Y axis: top to bottom
- All positions and dimensions are in millimeters
- Drivers convert to dots using: `dots = mm * DPI / 25.4`

## DPI

Common thermal printer DPI values:

| DPI | Dots per mm | Typical use |
|-----|-------------|------------|
| 203 | 8 | Standard labels, shipping |
| 300 | ~12 | High-quality labels, badges |
| 600 | ~24 | Ultra-fine print, small labels |

The label format is DPI-independent. Drivers are responsible for converting
millimeter values to dots at the target DPI.

## Versioning

This specification follows semantic versioning. The current version is 1.0.

- **Patch** (1.0.x): Clarifications, typo fixes
- **Minor** (1.x.0): New field types, new optional properties (backward
  compatible)
- **Major** (x.0.0): Breaking changes to existing field types or coordinate
  system

## Examples

### Shipping Label (100x60mm)

```json
{
  "basePdf": { "width": 100, "height": 60 },
  "schemas": [[
    {
      "name": "recipient",
      "type": "text",
      "position": { "x": 5, "y": 5 },
      "width": 60, "height": 8,
      "fontSize": 14, "fontName": "Helvetica-Bold"
    },
    {
      "name": "address",
      "type": "multiVariableText",
      "position": { "x": 5, "y": 15 },
      "width": 60, "height": 20,
      "content": "{street}\n{city}, {state} {zip}",
      "variables": ["street", "city", "state", "zip"],
      "fontSize": 10
    },
    {
      "name": "tracking",
      "type": "barcodes128",
      "position": { "x": 5, "y": 40 },
      "width": 90, "height": 15
    }
  ]]
}
```

### Badge/Credential (86x54mm)

```json
{
  "basePdf": { "width": 86, "height": 54 },
  "schemas": [[
    {
      "name": "photo",
      "type": "image",
      "position": { "x": 3, "y": 3 },
      "width": 20, "height": 25
    },
    {
      "name": "name",
      "type": "text",
      "position": { "x": 26, "y": 5 },
      "width": 55, "height": 10,
      "fontSize": 16, "alignment": "center"
    },
    {
      "name": "company",
      "type": "text",
      "position": { "x": 26, "y": 17 },
      "width": 55, "height": 6,
      "fontSize": 10, "alignment": "center"
    },
    {
      "name": "qr",
      "type": "qrcode",
      "position": { "x": 65, "y": 30 },
      "width": 18, "height": 18
    }
  ]]
}
```

### Product Label (50x30mm)

```json
{
  "basePdf": { "width": 50, "height": 30 },
  "schemas": [[
    {
      "name": "product",
      "type": "text",
      "position": { "x": 2, "y": 2 },
      "width": 46, "height": 6,
      "fontSize": 12
    },
    {
      "name": "price",
      "type": "text",
      "position": { "x": 2, "y": 9 },
      "width": 20, "height": 6,
      "fontSize": 14, "fontName": "Helvetica-Bold"
    },
    {
      "name": "sku",
      "type": "barcodes128",
      "position": { "x": 2, "y": 17 },
      "width": 46, "height": 10
    }
  ]]
}
```
