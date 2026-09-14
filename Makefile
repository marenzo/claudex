VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildDate=$(BUILD_DATE)
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ARCHIVE ?= dist/claudex-$(VERSION)-$(GOOS)-$(GOARCH).tar.gz
IMAGE ?= claudex:local
STATICCHECK_VERSION ?= v0.8.1
GOVULNCHECK_VERSION ?= v1.8.0

.PHONY: build package test test-dashboard check lint install docker clean e2e

build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags '$(LDFLAGS)' -o dist/claudex ./cmd/claudex

package: build
	COPYFILE_DISABLE=1 tar -czf '$(ARCHIVE)' dist/claudex examples docs README.md LICENSE THIRD_PARTY_NOTICES.md

test: test-dashboard
	go test -race ./...

test-dashboard:
	node --check internal/dashboard/app.js
	node --check internal/dashboard/analytics.js
	node --test internal/dashboard/analytics_test.cjs

check: test
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)

# Static analysis and known-vulnerability scan; downloads the pinned tools, so it needs network.
lint:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

install: build
	./dist/claudex install $(INSTALL_FLAGS)

docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE) .

clean:
	rm -rf dist

# Opt-in end-to-end run: Claude Code + gateway in Docker against your real Codex sign-in.
e2e:
	go test -tags e2e -count=1 -timeout 60m -v ./e2e/
