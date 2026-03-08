#!/bin/bash
set -e

BINARY_NAME="tsc-bridge"
APP_NAME="TSC Bridge.app"
INSTALL_DIR="/Applications"
PLIST_NAME="com.tsc-bridge.plist"
LAUNCH_AGENTS="$HOME/Library/LaunchAgents"
HOSTNAME="myprinter.com"
CONFIG_DIR="$HOME/.tsc-bridge"
CERT_DIR="$CONFIG_DIR/certs"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo ""
echo "╔══════════════════════════════════════╗"
echo "║   TSC Bridge — Instalador macOS      ║"
echo "╚══════════════════════════════════════╝"
echo ""

# --- Kill existing instance ---
echo "[1/6] Deteniendo instancias previas..."
pkill -f "$BINARY_NAME" 2>/dev/null && echo "  Detenido" || echo "  Ninguna encontrada"
if launchctl list 2>/dev/null | grep -q "com.tsc-bridge"; then
    launchctl unload "$LAUNCH_AGENTS/$PLIST_NAME" 2>/dev/null || true
    echo "  LaunchAgent descargado"
fi
sleep 1

# --- Install .app bundle or raw binary ---
echo ""
echo "[2/6] Instalando aplicación..."
if [ -d "$SCRIPT_DIR/$APP_NAME" ]; then
    # Install .app bundle to /Applications
    rm -rf "$INSTALL_DIR/$APP_NAME"
    cp -R "$SCRIPT_DIR/$APP_NAME" "$INSTALL_DIR/$APP_NAME"
    BINARY_PATH="$INSTALL_DIR/$APP_NAME/Contents/MacOS/$BINARY_NAME"
    echo "  ✓ $APP_NAME instalado en $INSTALL_DIR"
elif [ -f "$SCRIPT_DIR/$BINARY_NAME" ]; then
    # Fallback: install raw binary to ~/bin
    mkdir -p "$HOME/bin"
    cp "$SCRIPT_DIR/$BINARY_NAME" "$HOME/bin/$BINARY_NAME"
    chmod +x "$HOME/bin/$BINARY_NAME"
    BINARY_PATH="$HOME/bin/$BINARY_NAME"
    echo "  ✓ Binario instalado en $HOME/bin/$BINARY_NAME"
else
    echo "  ✗ No se encontró $APP_NAME ni $BINARY_NAME"
    exit 1
fi

# --- Add hostname to /etc/hosts ---
echo ""
echo "[3/6] Configurando hostname..."
if grep -q "$HOSTNAME" /etc/hosts 2>/dev/null; then
    echo "  ✓ $HOSTNAME ya está en /etc/hosts"
else
    echo "  Agregando $HOSTNAME a /etc/hosts (requiere sudo)..."
    echo "127.0.0.1  $HOSTNAME" | sudo tee -a /etc/hosts > /dev/null
    echo "  ✓ $HOSTNAME agregado"
fi

# --- Generate certs ---
echo ""
echo "[4/6] Generando certificados SSL..."
mkdir -p "$CERT_DIR"
"$BINARY_PATH" --headless &
BRIDGE_PID=$!
sleep 3
kill $BRIDGE_PID 2>/dev/null || true
wait $BRIDGE_PID 2>/dev/null || true

# --- Trust CA certificate ---
CA_CERT="$CERT_DIR/ca.pem"
if [ -f "$CA_CERT" ]; then
    echo "  Instalando certificado CA (requiere sudo)..."
    sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$CA_CERT" 2>/dev/null \
        && echo "  ✓ Certificado CA instalado" \
        || echo "  ⚠ No se pudo instalar — HTTPS puede mostrar advertencias"
else
    echo "  ⚠ Certificado CA no encontrado — HTTPS puede mostrar advertencias"
fi

# --- Install LaunchAgent ---
echo ""
echo "[5/6] Configurando inicio automático..."
mkdir -p "$LAUNCH_AGENTS"

cat > "$LAUNCH_AGENTS/$PLIST_NAME" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.tsc-bridge</string>
    <key>ProgramArguments</key>
    <array>
        <string>${BINARY_PATH}</string>
        <string>--headless</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/tsc-bridge.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/tsc-bridge.err</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin</string>
    </dict>
</dict>
</plist>
PLIST
echo "  ✓ LaunchAgent creado"

launchctl load "$LAUNCH_AGENTS/$PLIST_NAME"
echo "  ✓ LaunchAgent cargado"

# --- Start service ---
echo ""
echo "[6/6] Iniciando servicio..."
launchctl start com.tsc-bridge 2>/dev/null || true
sleep 2

# Verify
if curl -s http://127.0.0.1:9638/status >/dev/null 2>&1 || curl -s http://127.0.0.1:9271/status >/dev/null 2>&1; then
    echo "  ✓ TSC Bridge está corriendo"
else
    echo "  ⚠ Servicio iniciado pero no responde aún — puede tardar unos segundos"
fi

echo ""
echo "╔══════════════════════════════════════════════╗"
echo "║         ✓ Instalación Completa               ║"
echo "╠══════════════════════════════════════════════╣"
echo "║  App:        $INSTALL_DIR/$APP_NAME"
echo "║  Servicio:   Auto-start al login (LaunchAgent)"
echo "║  Tray icon:  Aparece en la barra de menú     ║"
echo "║  Dashboard:  Clic en icono → Abrir Dashboard  ║"
echo "║  HTTPS:      https://$HOSTNAME:9272/   ║"
echo "╚══════════════════════════════════════════════╝"
echo ""
echo "Para desinstalar:"
echo "  launchctl unload ~/Library/LaunchAgents/$PLIST_NAME"
echo "  rm ~/Library/LaunchAgents/$PLIST_NAME"
echo "  rm -rf '/Applications/$APP_NAME'"
echo "  sudo sed -i '' '/$HOSTNAME/d' /etc/hosts"
echo "  sudo security delete-certificate -c 'TSC Bridge Local CA' /Library/Keychains/System.keychain"
echo ""
