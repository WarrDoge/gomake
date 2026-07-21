.PHONY: fmt lint secrets unit integration coverage test verify build build-static build-all verify-static

APP := gomake
DIST_DIR := .tmp/dist
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

GOCACHE ?= $(CURDIR)/.tmp/gocache

fmt:
	gofmt -w .
	goimports -w .

lint:
	mkdir -p .tmp $(GOCACHE)
	GOCACHE=$(GOCACHE) golangci-lint run ./...

secrets:
	gitleaks detect --source . -v

unit:
	mkdir -p .tmp $(GOCACHE)
	GOCACHE=$(GOCACHE) go test ./...

integration:
	mkdir -p .tmp $(GOCACHE)
	GOCACHE=$(GOCACHE) go test -tags=integration .

coverage:
	mkdir -p .tmp $(GOCACHE)
	GOCACHE=$(GOCACHE) go test -coverprofile=.tmp/coverage.out ./...

test: unit integration

verify: fmt lint secrets test build-static verify-static

build:
	mkdir -p .tmp $(GOCACHE)
	GOCACHE=$(GOCACHE) go build -o .tmp/$(APP) .

build-static:
	mkdir -p .tmp $(GOCACHE)
	GOCACHE=$(GOCACHE) CGO_ENABLED=0 go build -o .tmp/$(APP)-static .

build-all:
	mkdir -p $(DIST_DIR) $(GOCACHE)
	set -eu; \
	for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; \
		goarch=$${platform#*/}; \
		ext=""; \
		if [ "$$goos" = "windows" ]; then ext=".exe"; fi; \
		out="$(DIST_DIR)/$(APP)-$$goos-$$goarch$$ext"; \
		echo "building $$out"; \
		GOCACHE=$(GOCACHE) CGO_ENABLED=0 GOOS=$$goos GOARCH=$$goarch go build -o "$$out" .; \
	done

verify-static: build-static
	file .tmp/$(APP)-static
	ldd .tmp/$(APP)-static 2>&1 | grep -F "not a dynamic executable"
