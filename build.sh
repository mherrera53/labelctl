#!/bin/bash
# build.sh — Build TSC Bridge for macOS and Windows
# Generates icons, builds binaries, packages .app + .dmg, InnoSetup installer
set -e

PROJ_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$PROJ_DIR"
mkdir -p dist

VERSION=$(grep 'const version' main.go | head -1 | sed 's/.*"\(.*\)".*/\1/')
echo "╔══════════════════════════════════════╗"
echo "║   TSC Bridge v${VERSION} — Build Script    ║"
echo "╚══════════════════════════════════════╝"

# 1. Kill old instances
echo ""
echo "[1/7] Killing old tsc-bridge instances..."
pkill -f "tsc-bridge" 2>/dev/null && echo "  Killed old processes" || echo "  No old processes found"
sleep 1

# 2. Build macOS (arm64)
echo ""
echo "[2/7] Building macOS (arm64)..."
CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/tsc-bridge-mac .
echo "  ✓ dist/tsc-bridge-mac ($(du -h dist/tsc-bridge-mac | cut -f1))"

# 3. Build Windows 64-bit (requires mingw-w64 + Windows SDK headers)
echo ""
echo "[3/7] Building Windows 64-bit..."
if command -v x86_64-w64-mingw32-gcc &>/dev/null; then
  if CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 \
    go build -ldflags="-s -w -H windowsgui" -o dist/tsc-bridge.exe . 2>/dev/null; then
    echo "  ✓ dist/tsc-bridge.exe ($(du -h dist/tsc-bridge.exe | cut -f1))"
  else
    echo "  ⚠ Windows 64-bit build failed (missing SDK headers?) — using existing binary if available"
    [ -f dist/tsc-bridge.exe ] && echo "  ✓ dist/tsc-bridge.exe (existing: $(du -h dist/tsc-bridge.exe | cut -f1))"
  fi
else
  echo "  ⚠ mingw-w64 (x86_64) not found — skipping Windows 64-bit"
  [ -f dist/tsc-bridge.exe ] && echo "  ✓ dist/tsc-bridge.exe (existing: $(du -h dist/tsc-bridge.exe | cut -f1))"
fi

# 3b. Build Windows 32-bit
echo ""
echo "[3b/7] Building Windows 32-bit..."
if command -v i686-w64-mingw32-gcc &>/dev/null; then
  if CGO_ENABLED=1 CC=i686-w64-mingw32-gcc GOOS=windows GOARCH=386 \
    go build -ldflags="-s -w -H windowsgui" -o dist/tsc-bridge-32.exe . 2>/dev/null; then
    echo "  ✓ dist/tsc-bridge-32.exe ($(du -h dist/tsc-bridge-32.exe | cut -f1))"
  else
    echo "  ⚠ Windows 32-bit build failed — using existing binary if available"
    [ -f dist/tsc-bridge-32.exe ] && echo "  ✓ dist/tsc-bridge-32.exe (existing: $(du -h dist/tsc-bridge-32.exe | cut -f1))"
  fi
else
  echo "  ⚠ mingw-w64 (i686) not found — skipping Windows 32-bit"
  [ -f dist/tsc-bridge-32.exe ] && echo "  ✓ dist/tsc-bridge-32.exe (existing: $(du -h dist/tsc-bridge-32.exe | cut -f1))"
fi

# 4. Generate icons from binary
echo ""
echo "[4/7] Generating application icons..."
ICON_PNG="dist/icon_1024.png"
ICON_ICO="dist/tsc-bridge.ico"
./dist/tsc-bridge-mac --generate-icon "$ICON_PNG"
./dist/tsc-bridge-mac --generate-ico "$ICON_ICO"
echo "  ✓ $ICON_PNG"
echo "  ✓ $ICON_ICO"

# Generate .icns for macOS
ICONSET=$(mktemp -d)/AppIcon.iconset
mkdir -p "$ICONSET"
for SIZE in 16 32 64 128 256 512; do
  sips -z $SIZE $SIZE "$ICON_PNG" --out "$ICONSET/icon_${SIZE}x${SIZE}.png" >/dev/null 2>&1
  D=$((SIZE * 2))
  if [ $D -le 1024 ]; then
    sips -z $D $D "$ICON_PNG" --out "$ICONSET/icon_${SIZE}x${SIZE}@2x.png" >/dev/null 2>&1
  fi
done
# 512@2x = 1024
cp "$ICON_PNG" "$ICONSET/icon_512x512@2x.png"
ICNS_FILE="dist/AppIcon.icns"
iconutil -c icns "$ICONSET" -o "$ICNS_FILE" 2>/dev/null && echo "  ✓ $ICNS_FILE" || echo "  ⚠ iconutil failed — .app will have no icon"
rm -rf "$(dirname "$ICONSET")"

# 5. Package macOS .app bundle
echo ""
echo "[5/7] Packaging macOS .app bundle..."
APP="dist/TSC Bridge.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp dist/tsc-bridge-mac "$APP/Contents/MacOS/tsc-bridge"
chmod +x "$APP/Contents/MacOS/tsc-bridge"

# Copy icon
if [ -f "$ICNS_FILE" ]; then
  cp "$ICNS_FILE" "$APP/Contents/Resources/AppIcon.icns"
fi

cat > "$APP/Contents/Info.plist" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key>
    <string>tsc-bridge</string>
    <key>CFBundleIdentifier</key>
    <string>com.abstraktgt.tsc-bridge</string>
    <key>CFBundleName</key>
    <string>TSC Bridge</string>
    <key>CFBundleDisplayName</key>
    <string>TSC Bridge</string>
    <key>CFBundleVersion</key>
    <string>${VERSION}</string>
    <key>CFBundleShortVersionString</key>
    <string>${VERSION}</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleIconFile</key>
    <string>AppIcon</string>
    <key>LSMinimumSystemVersion</key>
    <string>11.0</string>
    <key>LSUIElement</key>
    <true/>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>LSApplicationCategoryType</key>
    <string>public.app-category.utilities</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>NSAppTransportSecurity</key>
    <dict>
        <key>NSAllowsLocalNetworking</key>
        <true/>
    </dict>
</dict>
</plist>
PLIST
echo "  ✓ $APP"

# 6. Create DMG for macOS distribution
echo ""
echo "[6/7] Creating macOS DMG..."
DMG="dist/TSC-Bridge-${VERSION}-macOS.dmg"
rm -f "$DMG"
# Create a temporary folder with the .app and a symlink to /Applications
DMG_STAGING=$(mktemp -d)
cp -R "$APP" "$DMG_STAGING/"
ln -s /Applications "$DMG_STAGING/Applications"
hdiutil create -volname "TSC Bridge ${VERSION}" -srcfolder "$DMG_STAGING" -ov -format UDZO "$DMG" >/dev/null 2>&1 \
  && echo "  ✓ $DMG" || echo "  ⚠ DMG creation failed"
rm -rf "$DMG_STAGING"

# 7. Package Windows installer files
echo ""
echo "[7/7] Packaging Windows installer..."
WIN_DIR="dist/tsc-bridge-win-${VERSION}"
rm -rf "$WIN_DIR"
mkdir -p "$WIN_DIR"
if [ -f dist/tsc-bridge.exe ]; then
  cp dist/tsc-bridge.exe "$WIN_DIR/"
fi
if [ -f dist/tsc-bridge-32.exe ]; then
  cp dist/tsc-bridge-32.exe "$WIN_DIR/"
fi
cp dist/tsc-bridge.ico "$WIN_DIR/" 2>/dev/null || true
cp install_windows.bat "$WIN_DIR/"
cp tsc-bridge.iss "$WIN_DIR/" 2>/dev/null || true
echo "  ✓ $WIN_DIR/"

if [ -f dist/tsc-bridge.exe ]; then
  cd dist && zip -r "tsc-bridge-win-${VERSION}.zip" "tsc-bridge-win-${VERSION}/" >/dev/null
  echo "  ✓ dist/tsc-bridge-win-${VERSION}.zip"
  cd "$PROJ_DIR"
fi

# Summary
echo ""
echo "╔══════════════════════════════════════════╗"
echo "║          Build Complete! v${VERSION}          ║"
echo "╠══════════════════════════════════════════╣"
echo "║ macOS:                                   ║"
echo "║   dist/TSC Bridge.app                    ║"
if [ -f "$DMG" ]; then
echo "║   $DMG  ║"
fi
echo "║                                          ║"
if [ -f dist/tsc-bridge.exe ]; then
echo "║ Windows:                                 ║"
echo "║   dist/tsc-bridge.exe (64-bit)           ║"
fi
if [ -f dist/tsc-bridge-32.exe ]; then
echo "║   dist/tsc-bridge-32.exe (32-bit)        ║"
fi
echo "║                                          ║"
echo "║ Icons:                                   ║"
echo "║   dist/AppIcon.icns (macOS)              ║"
echo "║   dist/tsc-bridge.ico (Windows)          ║"
echo "╚══════════════════════════════════════════╝"
echo ""
echo "macOS: Drag 'TSC Bridge.app' to /Applications"
echo "       or distribute the DMG: $DMG"
echo ""
echo "Windows: Run install_windows.bat as admin"
echo "         or compile tsc-bridge.iss with InnoSetup for GUI installer"
