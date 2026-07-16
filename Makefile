.PHONY: build test vet release checksums formula-check

VERSION ?= 0.0.0-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

export CGO_ENABLED := 0

LDFLAGS := -s -w -X portx/internal/buildinfo.Version=$(VERSION) -X portx/internal/buildinfo.Commit=$(COMMIT) -X portx/internal/buildinfo.Date=$(DATE)
GOFLAGS := -trimpath

build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/portx ./cmd/portx

test:
	go test ./...

vet:
	go vet ./...

# Cross-compile release binaries (signing is out of band).
release:
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_darwin_arm64 ./cmd/portx
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_darwin_amd64 ./cmd/portx
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_linux_amd64 ./cmd/portx
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_linux_arm64 ./cmd/portx
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_windows_amd64.exe ./cmd/portx
	$(MAKE) checksums

checksums:
	cd dist && shasum -a 256 portx_* > SHA256SUMS && cat SHA256SUMS

# Validate Homebrew formula Ruby syntax (requires ruby).
formula-check:
	ruby -c Formula/portx.rb
