BINARY=promscout
VERSION?=1.0.0
COMMIT=$(shell git rev-parse --short HEAD)
DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS=-ldflags "-X 'promscout/pkg/version.Version=$(VERSION)' \
                  -X 'promscout/pkg/version.GitCommit=$(COMMIT)' \
                  -X 'promscout/pkg/version.BuildDate=$(DATE)'"

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/promscout

clean:
	rm -rf bin

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...
