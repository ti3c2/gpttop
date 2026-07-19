SHELL := /bin/sh

ifneq ($(wildcard .tools/go1.26.5/bin/go),)
GO ?= .tools/go1.26.5/bin/go
else
GO ?= go
endif

MODULE := $(shell $(GO) list -m 2>/dev/null || echo gpttop)
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PREFIX ?= /usr/local

BIN := bin/gpttop
PKG := ./cmd/gpttop
LDFLAGS := -s -w \
	-X '$(MODULE)/internal/buildinfo.Version=$(VERSION)' \
	-X '$(MODULE)/internal/buildinfo.Commit=$(COMMIT)' \
	-X '$(MODULE)/internal/buildinfo.Date=$(DATE)'

.PHONY: help fmt fmt-check vet lint test test-race check build build-all install release clean

help:
	@printf '%s\n' \
		'Targets:' \
		'  fmt        Format Go files' \
		'  fmt-check  Verify Go formatting' \
		'  vet        Run go vet' \
		'  lint       Run the local lint gate' \
		'  test       Run deterministic tests' \
		'  test-race  Run race detector tests' \
		'  check      fmt-check, vet, test, build' \
		'  build      Build bin/gpttop' \
		'  build-all  Cross-build Linux/macOS amd64/arm64 binaries' \
		'  install    Install to PREFIX/bin, default /usr/local/bin' \
		'  release    Build archives and SHA-256 checksums, VERSION required' \
		'  clean      Remove build artifacts'

fmt:
	@gofmt -w $$(find . -type f -name '*.go' -not -path './.git/*' -not -path './.tools/*' -not -path './bin/*' -not -path './dist/*')

fmt-check:
	@files="$$(gofmt -l $$(find . -type f -name '*.go' -not -path './.git/*' -not -path './.tools/*' -not -path './bin/*' -not -path './dist/*'))"; \
	if [ -n "$$files" ]; then \
		echo "Go files need formatting:"; \
		echo "$$files"; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

lint: vet

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

check: fmt-check vet test build

build:
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)

build-all:
	@mkdir -p dist
	@set -eu; \
	for os in linux darwin; do \
		for arch in amd64 arm64; do \
			out="dist/gpttop-$${os}-$${arch}"; \
			echo "building $$out"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags="$(LDFLAGS)" -o "$$out" $(PKG); \
		done; \
	done

install: build
	install -d "$(PREFIX)/bin"
	install -m 0755 "$(BIN)" "$(PREFIX)/bin/gpttop"

release:
	@[ "$(VERSION)" != "dev" ] || { echo "VERSION is required, for example make release VERSION=v0.1.0" >&2; exit 2; }
	GO="$(GO)" ./scripts/release.sh "$(VERSION)"

clean:
	rm -rf bin dist
