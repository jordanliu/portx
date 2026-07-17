.PHONY: build test vet format-check workflow-check staticcheck vulncheck check release checksums

VERSION ?= 0.0.0-dev
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)

export CGO_ENABLED := 0

# Keep analysis tools reproducible and prevent an automatic toolchain upgrade
# from changing the result of a release check.
export GOTOOLCHAIN := local
export GOFLAGS := -mod=readonly -trimpath

LDFLAGS := -s -w -X portx/internal/buildinfo.Version=$(VERSION) -X portx/internal/buildinfo.Commit=$(COMMIT) -X portx/internal/buildinfo.Date=$(DATE)

build:
	go build $(GOFLAGS) -buildvcs=false -ldflags "$(LDFLAGS)" -o bin/portx ./cmd/portx

test:
	go test ./...

vet:
	go vet ./...

format-check:
	@test -z "$$(gofmt -l .)" || { \
		echo "Go files need formatting:" >&2; \
		gofmt -d $$(gofmt -l .); \
		exit 1; \
	}

workflow-check:
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 .github/workflows/*.yml

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@v0.6.1 ./...

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...

check: format-check workflow-check vet test staticcheck vulncheck

# Cross-compile release binaries (signing is out of band).
release:
	mkdir -p dist
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -buildvcs=false -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_darwin_arm64 ./cmd/portx
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -buildvcs=false -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_darwin_amd64 ./cmd/portx
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -buildvcs=false -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_linux_amd64 ./cmd/portx
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -buildvcs=false -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_linux_arm64 ./cmd/portx
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -buildvcs=false -ldflags "$(LDFLAGS)" -o dist/portx_$(VERSION)_windows_amd64.exe ./cmd/portx
	$(MAKE) checksums

checksums:
	cd dist && shasum -a 256 portx_* > SHA256SUMS && cat SHA256SUMS
