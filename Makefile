# All Go tooling runs inside a pinned container; nothing but Docker is needed on the host.
GO_IMAGE   ?= golang:1.27.1-bookworm
CACHE_DIR  ?= $(HOME)/.cache/msgate-go
VERSION    := $(shell cat VERSION)
SOURCE_SHA := $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_SEQ  := $(shell git rev-list --count HEAD 2>/dev/null || echo 0)
LDFLAGS    := -s -w -X github.com/JustAzul/cc-msgate/internal/version.Version=$(VERSION) -X github.com/JustAzul/cc-msgate/internal/version.SourceCommit=$(SOURCE_SHA) -X github.com/JustAzul/cc-msgate/internal/version.BuildSeq=$(BUILD_SEQ)

DOCKER_GO = docker run --rm --label msgate.dev=1 -u $$(id -u):$$(id -g) \
  -v $(CURDIR):/src -w /src \
  -v $(CACHE_DIR)/mod:/gomod -v $(CACHE_DIR)/build:/gocache \
  -e GOMODCACHE=/gomod -e GOCACHE=/gocache -e HOME=/tmp \
  -e GOFLAGS=-buildvcs=false -e CGO_ENABLED=0 \
  $(GO_IMAGE)

.PHONY: test vet vet-darwin fmt build cache dist verify-plugin

cache:
	mkdir -p $(CACHE_DIR)/mod $(CACHE_DIR)/build

test: cache
	$(DOCKER_GO) go test ./... $(TESTFLAGS)

vet: cache
	$(DOCKER_GO) go vet ./...

# Compile-and-vet check for the darwin-only files; they cannot run here.
vet-darwin: cache
	$(DOCKER_GO) env GOOS=darwin GOARCH=arm64 go vet ./...
	$(DOCKER_GO) env GOOS=darwin GOARCH=amd64 go vet ./...

fmt: cache
	$(DOCKER_GO) gofmt -l -w cmd internal test tools

build: cache
	$(DOCKER_GO) go build -trimpath -ldflags '$(LDFLAGS)' -o build/msgate ./cmd/msgate

# Same tree CI publishes to the dist branch, built inside the pinned container.
# git metadata is not visible in the container, so the host passes it in.
dist: cache
	rm -rf dist
	$(DOCKER_GO) env SOURCE_SHA=$(SOURCE_SHA) BUILD_SEQ=$(BUILD_SEQ) sh scripts/build-dist.sh dist

# Loads the plugin in a real Claude Code process, with no model spend, and fails
# unless its `msgate` interface mounts. Needs claude on PATH; run it before
# any push that touches plugin/.
verify-plugin:
	scripts/verify-plugin.sh
