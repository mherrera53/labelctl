VERSION := 3.0.0
BINARY := tsc-bridge
DIST := dist

.PHONY: all build-mac build-windows build-windows-32 package-mac package-windows icons app dmg clean

all: build-mac icons app

# macOS: CGO required for systray
build-mac:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o $(DIST)/$(BINARY)-mac .
	@echo "Built $(DIST)/$(BINARY)-mac (macOS arm64)"

# Windows 64-bit: Intel + AMD x86-64 (brew install mingw-w64)
build-windows:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
		go build -ldflags="-s -w -H windowsgui" -o $(DIST)/$(BINARY).exe .
	@echo "Built $(DIST)/$(BINARY).exe (Windows amd64)"

# Windows 32-bit: Intel + AMD x86 legacy (brew install mingw-w64)
build-windows-32:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=windows GOARCH=386 CC=i686-w64-mingw32-gcc \
		go build -ldflags="-s -w -H windowsgui" -o $(DIST)/$(BINARY)-32.exe .
	@echo "Built $(DIST)/$(BINARY)-32.exe (Windows 386)"

# Generate all icon formats
icons: build-mac
	@$(DIST)/$(BINARY)-mac --generate-icon $(DIST)/icon_1024.png
	@$(DIST)/$(BINARY)-mac --generate-ico $(DIST)/$(BINARY).ico
	@echo "Generated icons"

# Package macOS .app bundle
app: build-mac icons
	@bash build.sh 2>/dev/null || true

# Create macOS DMG
dmg: app
	@echo "DMG created by build.sh"

# Package macOS: binary + installer + LaunchAgent plist
package-mac: build-mac
	@mkdir -p $(DIST)/tsc-bridge-mac-$(VERSION)
	cp $(DIST)/$(BINARY)-mac $(DIST)/tsc-bridge-mac-$(VERSION)/$(BINARY)
	cp install_mac.sh $(DIST)/tsc-bridge-mac-$(VERSION)/
	cp com.tsc-bridge.plist $(DIST)/tsc-bridge-mac-$(VERSION)/
	cd $(DIST) && zip -r tsc-bridge-mac-$(VERSION).zip tsc-bridge-mac-$(VERSION)/
	@echo "Packaged $(DIST)/tsc-bridge-mac-$(VERSION).zip"

# Package Windows: binary + installer batch
package-windows: build-windows
	@mkdir -p $(DIST)/tsc-bridge-win-$(VERSION)
	cp $(DIST)/$(BINARY).exe $(DIST)/tsc-bridge-win-$(VERSION)/
	cp $(DIST)/$(BINARY).ico $(DIST)/tsc-bridge-win-$(VERSION)/ 2>/dev/null || true
	cp install_windows.bat $(DIST)/tsc-bridge-win-$(VERSION)/
	cp tsc-bridge.iss $(DIST)/tsc-bridge-win-$(VERSION)/ 2>/dev/null || true
	cd $(DIST) && zip -r tsc-bridge-win-$(VERSION).zip tsc-bridge-win-$(VERSION)/
	@echo "Packaged $(DIST)/tsc-bridge-win-$(VERSION).zip"

clean:
	rm -rf $(DIST)
