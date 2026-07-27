VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w \
	-X github.com/igun997/freelancer-mcp/internal/cliapp.Version=$(VERSION) \
	-X main.version=$(VERSION)

.PHONY: all build test vet fmt check install clean

all: check build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/freelancer ./cmd/freelancer
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/freelancer-mcp ./cmd/freelancer-mcp

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

check: vet
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed on:"; echo "$$unformatted"; exit 1; fi
	go test ./...

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/freelancer
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/freelancer-mcp

clean:
	rm -rf bin dist
