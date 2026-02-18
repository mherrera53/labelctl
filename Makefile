VERSION := 2.0.0
BINARY := tsc-bridge
DIST := dist

.PHONY: all build-mac build-windows package-mac package-windows clean

all: build-mac build-windows

# macOS: CGO required for libusb
build-mac:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o $(DIST)/$(BINARY)-mac .
	@echo "Built $(DIST)/$(BINARY)-mac (macOS arm64)"

# Windows: CGO disabled, no libusb — uses Print Spooler API
build-windows:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(DIST)/$(BINARY).exe .
	@echo "Built $(DIST)/$(BINARY).exe (Windows amd64)"

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
	cp install_windows.bat $(DIST)/tsc-bridge-win-$(VERSION)/
	cd $(DIST) && zip -r tsc-bridge-win-$(VERSION).zip tsc-bridge-win-$(VERSION)/
	@echo "Packaged $(DIST)/tsc-bridge-win-$(VERSION).zip"

clean:
	rm -rf $(DIST)
