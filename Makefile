VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

VERSION_PKG := github.com/itspriddle/snatcher/internal/version
LDFLAGS := -s -w \
	-X $(VERSION_PKG).Version=$(VERSION) \
	-X $(VERSION_PKG).Commit=$(COMMIT) \
	-X $(VERSION_PKG).Date=$(DATE)

.PHONY: build test lint clean check surface check-surface check-man \
	fmt fmt-check vet tidy tidy-check race-test vuln replace-check release-check

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/snatcher ./cmd/snatcher

test:
	go test ./...

lint:
	golangci-lint run

vet:
	go vet ./...

fmt:
	gofmt -s -w .

fmt-check:
	@test -z "$$(gofmt -s -l . | tee /dev/stderr)" || (echo "Code is not formatted. Run 'make fmt'" && exit 1)

race-test:
	go test -race -count=1 ./...

tidy:
	go mod tidy

tidy-check:
	@set -e; cp go.mod go.mod.tidycheck; cp go.sum go.sum.tidycheck; \
	restore() { mv go.mod.tidycheck go.mod; mv go.sum.tidycheck go.sum; }; \
	if ! go mod tidy; then restore; echo "'go mod tidy' failed."; exit 1; fi; \
	if ! git diff --quiet -- go.mod go.sum; then \
		restore; echo "go.mod/go.sum are not tidy. Run 'make tidy' and commit."; exit 1; fi; \
	rm -f go.mod.tidycheck go.sum.tidycheck

vuln:
	@echo "Running govulncheck..."
	govulncheck ./...

replace-check:
	@if grep -q '^[[:space:]]*replace[[:space:]]' go.mod; then \
		echo "ERROR: go.mod contains replace directives"; \
		grep '^[[:space:]]*replace[[:space:]]' go.mod; \
		echo "Remove replace directives before releasing."; exit 1; fi
	@echo "Replace check passed"

clean:
	rm -rf bin/

surface:
	go test ./internal/cli/ -run TestSurface -update

check-surface:
	go test ./internal/cli/ -run TestSurface -v

check-man:
	go test ./internal/cli/ -run TestManPageCoverage -count=1 -v

check: fmt-check vet lint test check-surface check-man tidy-check

release-check: check replace-check vuln race-test
