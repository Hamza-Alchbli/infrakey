.PHONY: help build test audit \
	bin bin-linux bin-linux-arm64 bin-macos bin-macos-amd64 bin-all clean-bin

GO ?= go
CMD_DIR ?= ./cmd/infrakey
BIN_DIR ?= bin
BINARY ?= infrakey

default: help

help:
	@echo "Targets:"
	@echo "  make build            # compile all packages"
	@echo "  make test             # run all tests"
	@echo "  make audit            # run test + race + vet checks"
	@echo "  make bin              # build CLI for current OS/ARCH -> $(BIN_DIR)/$(BINARY)"
	@echo "  make bin-linux        # build Linux amd64 CLI -> $(BIN_DIR)/$(BINARY)-linux-amd64"
	@echo "  make bin-linux-arm64  # build Linux arm64 CLI -> $(BIN_DIR)/$(BINARY)-linux-arm64"
	@echo "  make bin-macos        # build macOS arm64 CLI -> $(BIN_DIR)/$(BINARY)-darwin-arm64"
	@echo "  make bin-macos-amd64  # build macOS amd64 CLI -> $(BIN_DIR)/$(BINARY)-darwin-amd64"
	@echo "  make bin-all          # build linux+macos artifacts into $(BIN_DIR)/"
	@echo "  make clean-bin        # remove generated binaries in $(BIN_DIR)/"

build:
	$(GO) build ./...

test:
	$(GO) test ./...

audit:
	$(GO) test ./...
	$(GO) test -race ./...
	$(GO) vet ./...

bin:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BIN_DIR)/$(BINARY) $(CMD_DIR)

bin-linux:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(CMD_DIR)

bin-linux-arm64:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -o $(BIN_DIR)/$(BINARY)-linux-arm64 $(CMD_DIR)

bin-macos:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 $(GO) build -o $(BIN_DIR)/$(BINARY)-darwin-arm64 $(CMD_DIR)

bin-macos-amd64:
	mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 $(GO) build -o $(BIN_DIR)/$(BINARY)-darwin-amd64 $(CMD_DIR)

bin-all: bin-linux bin-linux-arm64 bin-macos bin-macos-amd64

clean-bin:
	rm -f $(BIN_DIR)/$(BINARY) \
		$(BIN_DIR)/$(BINARY)-linux-amd64 \
		$(BIN_DIR)/$(BINARY)-linux-arm64 \
		$(BIN_DIR)/$(BINARY)-darwin-arm64 \
		$(BIN_DIR)/$(BINARY)-darwin-amd64
