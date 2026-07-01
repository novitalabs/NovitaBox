NOVITABOX_GOCACHE := $(CURDIR)/.gocache
NOVITABOX_GOMODCACHE := $(CURDIR)/.gomodcache
NOVITABOX_BUF_CACHE_DIR := $(CURDIR)/.bufcache
GOPROXY ?= https://goproxy.cn,direct

.PHONY: build build-linux-amd64 build-linux-arm64 test fmt proto tools

COMMANDS := boxapi boxctl boxd boxlet boxproxy boxshim

build:
	mkdir -p bin
	for cmd in $(COMMANDS); do \
		GOPROXY="$(GOPROXY)" GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go build -o "bin/$$cmd" "./cmd/$$cmd"; \
	done

build-linux-amd64:
	mkdir -p bin/linux-amd64
	for cmd in $(COMMANDS); do \
		GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOPROXY="$(GOPROXY)" GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go build -o "bin/linux-amd64/$$cmd" "./cmd/$$cmd"; \
	done

build-linux-arm64:
	mkdir -p bin/linux-arm64
	for cmd in $(COMMANDS); do \
		GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOPROXY="$(GOPROXY)" GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go build -o "bin/linux-arm64/$$cmd" "./cmd/$$cmd"; \
	done

test:
	GOPROXY="$(GOPROXY)" GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go test ./...

fmt:
	GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go fmt ./...

proto:
	BUF_CACHE_DIR="$(NOVITABOX_BUF_CACHE_DIR)" PATH="$(CURDIR)/.bin:$(PATH)" ./.bin/buf generate

tools:
	mkdir -p .bin
	cd tools && GOBIN="$(CURDIR)/.bin" GOPROXY="$(GOPROXY)" GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go install tool
