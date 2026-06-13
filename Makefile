BIN := bin/finador

.PHONY: all build install fmt fmt-check vet lint test race check hooks clean help

all: build

build: ## Compile the binary into bin/finador
	go build -trimpath -o $(BIN) ./cmd/finador

install: ## Install finador on your PATH (go install — honors GOBIN, else GOPATH/bin)
	go install -trimpath ./cmd/finador
	@dir=$$(go env GOBIN); [ -n "$$dir" ] || dir=$$(go env GOPATH)/bin; echo "installed: $$dir/finador"

fmt: ## Rewrite sources with gofmt
	gofmt -w .

fmt-check: ## Fail if any file needs gofmt
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then \
		echo "gofmt needed on:"; echo "$$out"; exit 1; fi

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint (skipped with a warning if not installed)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "WARNING: golangci-lint not found, lint skipped (https://golangci-lint.run/docs/welcome/install/)"; fi

test: ## Run all tests
	go test ./... -count=1

race: ## Run race-sensitive packages with -race
	go test -race -count=1 ./internal/web/ ./internal/store/

check: fmt-check vet lint test race ## Full quality gate

hooks: ## Install the git pre-commit hook (points git at .githooks/)
	git config core.hooksPath .githooks
	@echo "pre-commit hook installed (git config core.hooksPath .githooks)"

clean: ## Remove built binaries
	rm -rf bin

help: ## List targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | \
		awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'
