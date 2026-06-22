NOVITABOX_GOCACHE := $(CURDIR)/.gocache
NOVITABOX_GOMODCACHE := $(CURDIR)/.gomodcache
NOVITABOX_BUF_CACHE_DIR := $(CURDIR)/.bufcache

.PHONY: build test fmt proto tools

build:
	mkdir -p bin
	GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go build -o bin/boxapi ./cmd/boxapi
	GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go build -o bin/boxlet ./cmd/boxlet
	GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go build -o bin/boxproxy ./cmd/boxproxy
	GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go build -o bin/boxshim ./cmd/boxshim
	GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go build -o bin/boxd ./cmd/boxd

test:
	GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go test ./...

fmt:
	GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go fmt ./...

proto:
	BUF_CACHE_DIR="$(NOVITABOX_BUF_CACHE_DIR)" PATH="$(CURDIR)/.bin:$(PATH)" ./.bin/buf generate

tools:
	mkdir -p .bin
	cd tools && GOBIN="$(CURDIR)/.bin" GOCACHE="$(NOVITABOX_GOCACHE)" GOMODCACHE="$(NOVITABOX_GOMODCACHE)" go install tool
