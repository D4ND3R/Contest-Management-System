# Contest Management System — developer entry points.
SHELL := /bin/bash
GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X github.com/D4ND3R/Contest-Management-System/internal/version.Version=$(VERSION) \
           -X github.com/D4ND3R/Contest-Management-System/internal/version.Commit=$(COMMIT)
PKGS ?= ./...

.PHONY: all build test test-race test-short test-sandbox test-e2e verify-host bench lint fmt generate \
        dev dev-native dev-down dev-logs migrate infra-stop clean loadtest help

all: lint build test

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  %-14s %s\n",$$1,$$2}'

build: ## build bin/cms and bin/cmsctl
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/cms ./cmd/cmsctl

test: ## unit + integration tests (starts throwaway PostgreSQL/Redis when needed)
	scripts/test.sh -count=1 $(PKGS)

test-race: ## tests with the race detector
	scripts/test.sh -count=1 -race $(PKGS)

test-short: ## unit tests only (no PostgreSQL/Redis)
	$(GO) test -short -count=1 $(PKGS)

test-sandbox: ## malicious-program battery + sample solutions (root + isolate)
	CMS_SANDBOX_TESTS=1 scripts/test.sh -count=1 -v -run 'TestMalicious|TestSampleSolutions' ./internal/worker/... ./internal/tasktypes/...

verify-host: build ## check this machine judges correctly (root; see docs/en/verify-host.md)
	scripts/verify-host.sh --cms bin/cms

test-e2e: ## whole-system tests alone, enforcing latency targets (judging needs root + isolate)
	CMS_SANDBOX_TESTS=1 CMS_PERF_ASSERT=1 scripts/test.sh -count=1 -p 1 -v ./internal/e2e/...

bench: ## Go benchmarks for hot paths
	scripts/test.sh -run '^$$' -bench . -benchmem $(PKGS)

lint: ## gofmt + go vet
	@out=$$(gofmt -l cmd internal web); \
	  if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet $(PKGS)

fmt: ## format the code
	gofmt -w cmd internal web

generate: ## regenerate sqlc code
	cd internal/db && sqlc generate

migrate: build ## apply migrations using $$CMS_CONFIG
	bin/cmsctl migrate

dev: ## full stack in docker compose (postgres, redis, minio + all services)
	docker compose up --build -d --wait
	@echo "contest http://localhost:8888  admin http://localhost:8889 (admin/admin)  ranking http://localhost:8890"

dev-native: ## full stack as native processes (no docker)
	scripts/dev.sh

dev-down: ## stop the docker compose stack
	docker compose down

dev-logs:
	docker compose logs -f --tail=100

infra-stop: ## stop the native test/dev PostgreSQL and Redis
	scripts/infra.sh stop testenv; scripts/infra.sh stop dev

loadtest: ## k6 load tests (see loadtest/README.md)
	$(MAKE) -C loadtest all

clean:
	rm -rf bin .cache/dev
