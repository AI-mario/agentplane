# AgentPlane Makefile
# Builds statically-linked binaries for agentplane (control plane) and apctl (CLI).
# mattn/go-sqlite3 requires CGO, so CGO_ENABLED=1 for agentplane.
# apctl does not use SQLite directly and can be built without CGO.

# --- Version info ---
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

MODULE := github.com/agentplane/agentplane

# ldflags for version injection
LDFLAGS := -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.Date=$(DATE)

# --- Paths ---
BIN_DIR     := bin
CMD_AP      := ./cmd/agentplane
CMD_APCTL   := ./cmd/apctl

# --- Default target ---
.PHONY: all
all: build

# --- Build for current platform (CGO enabled for SQLite) ---
.PHONY: build
build: build-agentplane build-apctl

.PHONY: build-agentplane
build-agentplane:
	CGO_ENABLED=1 go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/agentplane $(CMD_AP)

.PHONY: build-apctl
build-apctl:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS) -s -w" -o $(BIN_DIR)/apctl $(CMD_APCTL)

# --- Release builds (cross-compilation) ---
# Note: Cross-compiling with CGO (required for mattn/go-sqlite3) needs
# platform-specific C cross-compilers. The release target attempts builds
# but may fail without the correct toolchain installed.
#
# For CI, install cross-compilers or use Docker with appropriate toolchains.
# Alternative: switch to modernc.org/sqlite for pure-Go SQLite (no CGO needed).

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

.PHONY: release
release: clean
	@for platform in $(PLATFORMS); do \
		GOOS=$$(echo $$platform | cut -d/ -f1); \
		GOARCH=$$(echo $$platform | cut -d/ -f2); \
		EXT=""; \
		EXTRA_LDFLAGS=""; \
		if [ "$$GOOS" = "windows" ]; then EXT=".exe"; fi; \
		if [ "$$GOOS" = "linux" ]; then EXTRA_LDFLAGS="-extldflags '-static'"; fi; \
		echo "Building agentplane $$GOOS/$$GOARCH..."; \
		CGO_ENABLED=1 GOOS=$$GOOS GOARCH=$$GOARCH \
			go build -ldflags "$(LDFLAGS) $$EXTRA_LDFLAGS" \
			-o $(BIN_DIR)/agentplane-$$GOOS-$$GOARCH$$EXT $(CMD_AP) || \
			echo "  WARN: agentplane $$GOOS/$$GOARCH failed (need CGO cross-compiler)"; \
		echo "Building apctl $$GOOS/$$GOARCH..."; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH \
			go build -ldflags "$(LDFLAGS) -s -w" \
			-o $(BIN_DIR)/apctl-$$GOOS-$$GOARCH$$EXT $(CMD_APCTL); \
	done

# --- Individual platform targets ---
.PHONY: build-linux-amd64
build-linux-amd64:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS) -extldflags '-static'" \
		-o $(BIN_DIR)/agentplane-linux-amd64 $(CMD_AP)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS) -s -w" \
		-o $(BIN_DIR)/apctl-linux-amd64 $(CMD_APCTL)

.PHONY: build-linux-arm64
build-linux-arm64:
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
		go build -ldflags "$(LDFLAGS) -extldflags '-static'" \
		-o $(BIN_DIR)/agentplane-linux-arm64 $(CMD_AP)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
		go build -ldflags "$(LDFLAGS) -s -w" \
		-o $(BIN_DIR)/apctl-linux-arm64 $(CMD_APCTL)

.PHONY: build-darwin-amd64
build-darwin-amd64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS)" \
		-o $(BIN_DIR)/agentplane-darwin-amd64 $(CMD_AP)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS) -s -w" \
		-o $(BIN_DIR)/apctl-darwin-amd64 $(CMD_APCTL)

.PHONY: build-darwin-arm64
build-darwin-arm64:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
		go build -ldflags "$(LDFLAGS)" \
		-o $(BIN_DIR)/agentplane-darwin-arm64 $(CMD_AP)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 \
		go build -ldflags "$(LDFLAGS) -s -w" \
		-o $(BIN_DIR)/apctl-darwin-arm64 $(CMD_APCTL)

.PHONY: build-windows-amd64
build-windows-amd64:
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS)" \
		-o $(BIN_DIR)/agentplane-windows-amd64.exe $(CMD_AP)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -ldflags "$(LDFLAGS) -s -w" \
		-o $(BIN_DIR)/apctl-windows-amd64.exe $(CMD_APCTL)

# --- Dashboard ---
.PHONY: dashboard
dashboard:
	cd web/dashboard && npm ci && npm run build

# --- Test ---
.PHONY: test
test:
	CGO_ENABLED=1 go test ./... -count=1

.PHONY: test-short
test-short:
	CGO_ENABLED=1 go test ./... -short -count=1

# --- Lint ---
.PHONY: lint
lint:
	golangci-lint run ./...

# --- Clean ---
.PHONY: clean
clean:
	rm -rf $(BIN_DIR)

# --- Install (current platform) ---
.PHONY: install
install: build
	cp $(BIN_DIR)/agentplane $(GOPATH)/bin/ 2>/dev/null || cp $(BIN_DIR)/agentplane /usr/local/bin/
	cp $(BIN_DIR)/apctl $(GOPATH)/bin/ 2>/dev/null || cp $(BIN_DIR)/apctl /usr/local/bin/

.PHONY: help
help:
	@echo "AgentPlane Build System"
	@echo ""
	@echo "Targets:"
	@echo "  build              Build both binaries for current platform (default)"
	@echo "  build-agentplane   Build agentplane binary (CGO_ENABLED=1 for SQLite)"
	@echo "  build-apctl        Build apctl CLI binary (pure Go, no CGO)"
	@echo "  release            Cross-compile for all platforms"
	@echo "  build-linux-amd64  Build for Linux amd64"
	@echo "  build-linux-arm64  Build for Linux arm64"
	@echo "  build-darwin-amd64 Build for macOS amd64"
	@echo "  build-darwin-arm64 Build for macOS arm64"
	@echo "  build-windows-amd64 Build for Windows amd64"
	@echo "  dashboard          Build React dashboard (npm)"
	@echo "  test               Run all tests"
	@echo "  test-short         Run tests in short mode"
	@echo "  lint               Run golangci-lint"
	@echo "  clean              Remove build artifacts"
	@echo "  install            Install binaries to PATH"
	@echo "  help               Show this help"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION=$(VERSION)"
	@echo "  COMMIT=$(COMMIT)"
	@echo "  BIN_DIR=$(BIN_DIR)"
	@echo ""
	@echo "Embedded assets:"
	@echo "  Dashboard:   web/dashboard/dist/ (build with 'make dashboard' first)"
	@echo "  Migrations:  internal/store/sqlite/migrations/*.sql"
	@echo "               internal/store/postgres/migrations/*.sql"
	@echo ""
	@echo "Notes:"
	@echo "  - agentplane requires CGO (mattn/go-sqlite3). Cross-compiling needs"
	@echo "    platform-specific C compilers or Docker toolchains."
	@echo "  - apctl is pure Go (no CGO) and cross-compiles trivially."
	@echo "  - Linux builds use -extldflags '-static' for fully static binaries."
	@echo "  - Dashboard assets and SQL migrations are embedded via Go embed.FS."
