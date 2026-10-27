VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ARCHIVE ?= dist/claudex-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz
IMAGE ?= claudex:local
STATICCHECK_VERSION ?= v0.8.2
GOVULNCHECK_VERSION ?= v1.9.0
GITLEAKS_VERSION ?= v8.30.1
PREFIX ?= $(HOME)/.local

.PHONY: build package test check lint secrets install docker clean e2e docs-check

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags '$(LDFLAGS)' -o claudex ./cmd/claudex

package: build
	mkdir -p '$(dir $(ARCHIVE))'
	COPYFILE_DISABLE=1 tar --exclude='docs/design' -czf '$(ARCHIVE)' claudex examples docs README.md CHANGELOG.md SECURITY.md LICENSE THIRD_PARTY_NOTICES.md

docs-check:
	python3 .github/scripts/check_docs.py
	PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s .github/scripts -p 'test_*.py'
	python3 .github/scripts/release.py --check

test:
	go test -race ./...

check: test
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)

# Static analysis, known-vulnerability and secret scans; downloads the pinned tools, so it needs network.
lint: secrets
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# Scans every commit reachable from the checkout for leaked credentials.
secrets:
	go run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) git --no-banner --redact .

install: build
	install -d "$(DESTDIR)$(PREFIX)/bin"
	install -m 755 claudex "$(DESTDIR)$(PREFIX)/bin/claudex"

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

clean:
	rm -f claudex
	rm -rf dist

# Opt-in end-to-end run: Claude Code + gateway in Docker against your real Codex sign-in.
e2e:
	go test -tags e2e -count=1 -timeout 60m -v ./e2e/
