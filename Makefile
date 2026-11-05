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

GO       := go
GOLANGCI := golangci-lint

# The version CI installs, and the reason it lives here rather than in the
# workflow: it did live there, the workflow pinned v2.12.2 while this machine
# had v2.13.2, and the pipeline failed on something no local run could
# reproduce. v2.12.2 bundles staticcheck v0.7.0, whose IR builder panics
# building Go 1.27's internal/poll — on Linux, where that file is the epoll
# variant, so a macOS run never saw it. One pin, one place.
GOLANGCI_VERSION := v2.14.0

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

.PHONY: lint
lint: ## Run golangci-lint (uses .golangci.yml if present).
	@command -v $(GOLANGCI) >/dev/null 2>&1 || { \
		echo "$(GOLANGCI) not found — install it from https://golangci-lint.run/welcome/install/"; \
		exit 1; \
	}
	$(GOLANGCI) run ./...

.PHONY: lint-install
lint-install: ## Install the pinned golangci-lint (what CI runs).
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)

.PHONY: vuln
vuln: ## Scan deps and reachable code for known vulnerabilities (govulncheck).
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: sec
sec: lint vuln ## Security checks: golangci-lint (incl. gosec) + govulncheck.

# The Grafana dashboard is generated, not edited: contrib/grafana/*.json is
# the output of this script, and checked in so that nobody needs Python to
# import it. Standard library only.
.PHONY: dashboards
dashboards: ## Regenerate the Grafana dashboard in contrib/grafana (needs python3).
	python3 -I contrib/grafana/generate.py

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
