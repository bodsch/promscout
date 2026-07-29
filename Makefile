BINARY  := promscout
BIN_DIR := bin
MODULE  := bodsch.me/promscout

VERSION ?= 2.0.0
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -ldflags "-X '$(MODULE)/pkg/version.Version=$(VERSION)' \
                     -X '$(MODULE)/pkg/version.GitCommit=$(COMMIT)' \
                     -X '$(MODULE)/pkg/version.BuildDate=$(DATE)'"

# OS/ARCH combinations produced by the `release` target:
# Linux x86_64, Linux ARM64 and macOS ARM64 (Apple Silicon).
PLATFORMS := linux/amd64 linux/arm64 darwin/arm64

.PHONY: build test fmt vet tidy release clean

build:
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) .

test:
	go test ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

tidy:
	go mod tidy

release: clean
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		out=$(BIN_DIR)/$(BINARY)-$${os}-$${arch}; \
		echo "building $${out}"; \
		GOOS=$${os} GOARCH=$${arch} CGO_ENABLED=0 \
			go build $(LDFLAGS) -o $${out} . || exit 1; \
	done

clean:
	rm -rf $(BIN_DIR)
