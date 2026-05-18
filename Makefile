# Uptime Monitor — development task runner (SPEC §13.3, §25).

BINARY      := uptimemonitor
PKG         := github.com/deicod/uptimemonitor
VERSION_PKG := $(PKG)/internal/version

# Version metadata injected at link time.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X $(VERSION_PKG).Version=$(VERSION) \
           -X $(VERSION_PKG).Commit=$(COMMIT) \
           -X $(VERSION_PKG).Date=$(DATE)

# Atlas / migrations.
MIGRATIONS_DIR := internal/store/sqlite/migrations
NAME ?=

.PHONY: all build test vet fmt lint tidy \
        migrate-new migrate-validate migrate-apply ko-build clean help

all: build

## build: compile the binary with version ldflags into ./bin.
build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

## test: run the full test suite.
test:
	go test ./...

## vet: run go vet across all packages.
vet:
	go vet ./...

## fmt: format all Go source files.
fmt:
	gofmt -w .

## lint: run golangci-lint (requires golangci-lint on PATH).
lint:
	golangci-lint run

## tidy: tidy and verify module dependencies.
tidy:
	go mod tidy

## migrate-new: generate a new migration (usage: make migrate-new NAME=create_monitors).
migrate-new:
	@test -n "$(NAME)" || { echo "NAME is required, e.g. make migrate-new NAME=create_monitors"; exit 1; }
	atlas migrate diff $(NAME) --env local

## migrate-validate: validate migration checksum integrity.
migrate-validate:
	atlas migrate validate --env local

## migrate-apply: apply pending migrations to the local database.
migrate-apply:
	atlas migrate apply --env local

## ko-build: build a container image locally with ko.
ko-build:
	KO_DOCKER_REPO=ko.local ko build --local .

## clean: remove build artifacts.
clean:
	rm -rf bin

## help: list available targets.
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'
