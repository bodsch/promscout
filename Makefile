BINARY=promscout
VERSION?=1.1.0
COMMIT=$(shell git rev-parse --short HEAD)
DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS=-ldflags "-X 'promscout/pkg/version.Version=$(VERSION)' \
                  -X 'promscout/pkg/version.GitCommit=$(COMMIT)' \
                  -X 'promscout/pkg/version.BuildDate=$(DATE)'"

build:
	go build $(LDFLAGS) -o $(BINARY) .

clean:
	rm -rf bin

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...
