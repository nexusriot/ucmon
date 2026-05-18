# ucmon — build & packaging
#
# Pure-Go (no cgo), so every target cross-compiles from a single machine.
#
#   make                 # build binaries for all platforms
#   make amd64            # generic Linux x86_64
#   make uconsole         # ClockworkPi uConsole (CM4, arm64)
#   make pizero2w         # Raspberry Pi Zero 2 W (32-bit Pi OS, armv7)
#   make deb              # .deb packages for all platforms
#   make deb-uconsole     # single .deb (same pattern for each platform)
#   make install          # build native + install to $(PREFIX)/bin (sudo)
#   make run              # build & run natively
#   make clean

BIN     := ucmon
PKG     := ./cmd/ucmon
DIST    := dist
PREFIX  ?= /usr/local

# Keep the .deb version in lockstep with the embedded UI version string.
VERSION := $(shell sed -nE 's/.*const version = "([^"]+)".*/\1/p' internal/ui/model.go)

GO      ?= go
GOFLAGS :=
LDFLAGS := -s -w
BUILDENV := CGO_ENABLED=0

# platform = GOOS GOARCH GOARM debarch
# (GOARM is empty for non-arm targets)
amd64_triple    := linux amd64 .  amd64
uconsole_triple := linux arm64 .  arm64
pizero2w_triple := linux arm   7  armhf

PLATFORMS := amd64 uconsole pizero2w

.PHONY: all build $(PLATFORMS) deb $(addprefix deb-,$(PLATFORMS)) \
        install uninstall run fmt vet test clean version help

all: build

build: $(PLATFORMS) ## Build binaries for every platform

define BUILD_RULE
$(1): ## Build $(1) binary
	@mkdir -p $(DIST)
	$(eval P := $($(1)_triple))
	@echo ">> building $(BIN) $(VERSION) for $(1) ($(word 1,$(P))/$(word 2,$(P))$(if $(filter-out .,$(word 3,$(P))),v$(word 3,$(P)),))"
	$(BUILDENV) GOOS=$(word 1,$(P)) GOARCH=$(word 2,$(P)) \
		$(if $(filter-out .,$(word 3,$(P))),GOARM=$(word 3,$(P)),) \
		$(GO) build $(GOFLAGS) -trimpath -ldflags '$(LDFLAGS)' \
		-o $(DIST)/$(BIN)-$(VERSION)-$(word 1,$(P))-$(word 2,$(P))$(if $(filter-out .,$(word 3,$(P))),v$(word 3,$(P)),) $(PKG)
endef
$(foreach pl,$(PLATFORMS),$(eval $(call BUILD_RULE,$(pl))))

deb: $(addprefix deb-,$(PLATFORMS)) ## Build .deb for every platform

define DEB_RULE
deb-$(1): $(1) ## Build .deb for $(1)
	$(eval P := $($(1)_triple))
	$(eval DEBARCH := $(word 4,$(P)))
	$(eval BINFILE := $(BIN)-$(VERSION)-$(word 1,$(P))-$(word 2,$(P))$(if $(filter-out .,$(word 3,$(P))),v$(word 3,$(P)),))
	$(eval STAGE := $(DIST)/pkg/$(BIN)_$(VERSION)_$(DEBARCH))
	@command -v dpkg-deb >/dev/null 2>&1 || { echo "error: dpkg-deb not found (install dpkg)"; exit 1; }
	@echo ">> packaging $(BIN)_$(VERSION)_$(DEBARCH).deb"
	@rm -rf $(STAGE)
	@mkdir -p $(STAGE)/DEBIAN $(STAGE)/usr/bin
	@sed -e 's/_version_/$(VERSION)/' \
	     -e 's/^Architecture:.*/Architecture: $(DEBARCH)/' \
	     DEBIAN/control > $(STAGE)/DEBIAN/control
	@install -m 0755 $(DIST)/$(BINFILE) $(STAGE)/usr/bin/$(BIN)
	@dpkg-deb --build --root-owner-group $(STAGE) $(DIST)/$(BIN)_$(VERSION)_$(DEBARCH).deb >/dev/null
	@echo "   -> $(DIST)/$(BIN)_$(VERSION)_$(DEBARCH).deb"
endef
$(foreach pl,$(PLATFORMS),$(eval $(call DEB_RULE,$(pl))))

install: ## Build for the host and install to $(PREFIX)/bin (uses sudo)
	$(BUILDENV) $(GO) build $(GOFLAGS) -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(BIN) $(PKG)
	sudo install -m 0755 $(DIST)/$(BIN) $(PREFIX)/bin/$(BIN)
	@echo "installed $(PREFIX)/bin/$(BIN) ($(VERSION))"

uninstall: ## Remove the installed binary
	sudo rm -f $(PREFIX)/bin/$(BIN)

run: ## Build and run natively
	$(BUILDENV) $(GO) run $(PKG)

fmt: ## gofmt -w
	gofmt -w internal cmd

vet: ## go vet
	$(GO) vet ./...

test: ## go test
	$(GO) test ./...

version: ## Print the version derived from the source
	@echo $(VERSION)

clean: ## Remove build artifacts
	rm -rf $(DIST) build
	rm -f $(BIN) cmd/ucmon/ucmon *.deb

help: ## List targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		sort | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'
