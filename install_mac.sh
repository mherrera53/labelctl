#!/bin/bash
set -e

BINARY_NAME="tsc-bridge"
INSTALL_DIR="$HOME/bin"
PLIST_NAME="com.tsc-bridge.plist"
LAUNCH_AGENTS="$HOME/Library/LaunchAgents"
HOSTNAME="myprinter.com"
CONFIG_DIR="$HOME/.tsc-bridge"
CERT_DIR="$CONFIG_DIR/certs"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== TSC Bridge Installer (macOS) ==="
echo ""

# Create install directory
mkdir -p "$INSTALL_DIR"

# Copy binary
if [ -f "$SCRIPT_DIR/$BINARY_NAME" ]; then
    cp "$SCRIPT_DIR/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
    chmod +x "$INSTALL_DIR/$BINARY_NAME"
    echo "[OK] Binary installed to $INSTALL_DIR/$BINARY_NAME"
else
    echo "[ERROR] Binary not found: $SCRIPT_DIR/$BINARY_NAME"
    exit 1
fi

# --- Add hostname to /etc/hosts ---
if grep -q "$HOSTNAME" /etc/hosts 2>/dev/null; then
    echo "[OK] $HOSTNAME already in /etc/hosts"
else
    echo "[*] Adding $HOSTNAME to /etc/hosts (requires sudo)..."
    echo "127.0.0.1  $HOSTNAME" | sudo tee -a /etc/hosts > /dev/null
    echo "[OK] $HOSTNAME added to /etc/hosts"
fi

# --- Generate certs by running bridge briefly ---
echo "[*] Generating SSL certificates..."
mkdir -p "$CERT_DIR"
# Start bridge temporarily to trigger cert generation, then kill
"$INSTALL_DIR/$BINARY_NAME" &
BRIDGE_PID=$!
sleep 2
kill $BRIDGE_PID 2>/dev/null || true
wait $BRIDGE_PID 2>/dev/null || true

# --- Trust CA certificate in macOS Keychain ---
CA_CERT="$CERT_DIR/ca.pem"
if [ -f "$CA_CERT" ]; then
    echo "[*] Trusting CA certificate (requires sudo)..."
    sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$CA_CERT"
    echo "[OK] CA certificate trusted — browser will show green lock"
else
    echo "[WARN] CA cert not found at $CA_CERT — SSL may show warnings"
fi

# --- Install LaunchAgent for auto-start ---
mkdir -p "$LAUNCH_AGENTS"

# Unload existing agent if running
if launchctl list 2>/dev/null | grep -q "com.tsc-bridge"; then
    launchctl unload "$LAUNCH_AGENTS/$PLIST_NAME" 2>/dev/null || true
    echo "[OK] Unloaded existing LaunchAgent"
fi

# Copy plist with correct path
sed "s|__BINARY_PATH__|$INSTALL_DIR/$BINARY_NAME|g" "$SCRIPT_DIR/$PLIST_NAME" > "$LAUNCH_AGENTS/$PLIST_NAME"
echo "[OK] LaunchAgent installed"

# Load LaunchAgent
launchctl load "$LAUNCH_AGENTS/$PLIST_NAME"
echo "[OK] LaunchAgent loaded — tsc-bridge starts at login"

# Start now
launchctl start com.tsc-bridge
echo "[OK] tsc-bridge started"

# --- Create macOS .app wrapper for Dashboard GUI ---
APP_DIR="$HOME/Applications/TSC Bridge Dashboard.app"
mkdir -p "$APP_DIR/Contents/MacOS"
cat > "$APP_DIR/Contents/MacOS/tsc-bridge-dashboard" << 'LAUNCHER'
#!/bin/bash
# Launch tsc-bridge in dashboard (GUI) mode
exec "$HOME/bin/tsc-bridge" --dashboard
LAUNCHER
chmod +x "$APP_DIR/Contents/MacOS/tsc-bridge-dashboard"

cat > "$APP_DIR/Contents/Info.plist" << 'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>
    <string>TSC Bridge Dashboard</string>
    <key>CFBundleExecutable</key>
    <string>tsc-bridge-dashboard</string>
    <key>CFBundleIdentifier</key>
    <string>com.tsc-bridge.dashboard</string>
    <key>CFBundleVersion</key>
    <string>2.0.0</string>
    <key>LSUIElement</key>
    <false/>
</dict>
</plist>
PLIST
echo "[OK] Dashboard app created at $APP_DIR"

echo ""
echo "============================================"
echo "  Servicio:   Auto-start al login (LaunchAgent)"
echo "  Dashboard:  ~/Applications/TSC Bridge Dashboard.app"
echo "  API HTTP:   http://127.0.0.1:9271/"
echo "  API HTTPS:  https://$HOSTNAME:9272/"
echo "============================================"
echo ""
echo "Para abrir el dashboard: open '$APP_DIR'"
echo "  o ejecutar: tsc-bridge --dashboard"
echo ""
echo "Para desinstalar:"
echo "  launchctl unload ~/Library/LaunchAgents/$PLIST_NAME"
echo "  rm ~/Library/LaunchAgents/$PLIST_NAME"
echo "  rm $INSTALL_DIR/$BINARY_NAME"
echo "  rm -rf '$APP_DIR'"
echo "  sudo sed -i '' '/$HOSTNAME/d' /etc/hosts"
echo "  sudo security delete-certificate -c 'TSC Bridge Local CA' /Library/Keychains/System.keychain"
