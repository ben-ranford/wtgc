.PHONY: format fmt format-check lint security vuln suppressions test race cov build ci release release-check automation-check clean toolchain-check tools-install setup hooks-install hooks-uninstall

PACKAGE_PATTERN ?= ./...
BINARY_NAME ?= wtgc
CMD_PATH ?= ./cmd/wtgc
BIN_DIR ?= bin
DIST_DIR ?= dist
VERSION ?= dev
COVERAGE_FILE ?= .artifacts/coverage.out
COVERAGE_TOTAL_FILE ?= $(dir $(COVERAGE_FILE))coverage-total.txt
COVERAGE_MIN ?= 85
GO ?= go
GO_TOOLCHAIN ?= go1.26.1
GO_CMD := GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO)
GOLANGCI_LINT_VERSION ?= v2.9.0
GOSEC_VERSION ?= v2.22.11
GOSEC_FLAGS ?= -exclude=G204
GOVULNCHECK_VERSION ?= v1.7.0
HOST_GOOS := $(shell $(GO_CMD) env GOOS)
HOST_GOARCH := $(shell $(GO_CMD) env GOARCH)
PLATFORMS ?= $(HOST_GOOS)/$(HOST_GOARCH)

format:
	gofmt -w .

fmt: format

format-check:
	@files=$$(gofmt -l .); \
	if [ -n "$$files" ]; then \
		echo "The following files are not gofmt-formatted:"; \
		echo "$$files"; \
		exit 1; \
	fi

lint:
	$(GO_CMD) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run $(PACKAGE_PATTERN)

security:
	$(GO_CMD) run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) $(GOSEC_FLAGS) $(PACKAGE_PATTERN)

vuln:
	$(GO_CMD) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) $(PACKAGE_PATTERN)

suppressions:
	./scripts/check-suppressions.sh

test:
	$(GO_CMD) test $(PACKAGE_PATTERN)

race:
	$(GO_CMD) test -race $(PACKAGE_PATTERN)

cov:
	@./scripts/managed-output.sh ensure "$$(dirname "$(COVERAGE_FILE)")"
	@packages=$$($(GO_CMD) list $(PACKAGE_PATTERN) | grep -v '/integration$$' | grep -v '/internal/testgit$$'); \
	$(GO_CMD) test $$packages -covermode=atomic -coverprofile="$(COVERAGE_FILE)"
	@total=$$($(GO_CMD) tool cover -func="$(COVERAGE_FILE)" | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
	echo "Total coverage: $$total% (required: >= $(COVERAGE_MIN)%)"; \
	printf "%s\n" "$$total" > "$(COVERAGE_TOTAL_FILE)"; \
	awk "BEGIN { exit !($$total >= $(COVERAGE_MIN)) }" || (echo "Coverage gate failed: $$total% < $(COVERAGE_MIN)%"; exit 1)

build:
	./scripts/managed-output.sh ensure "$(BIN_DIR)"
	$(GO_CMD) build -trimpath -ldflags="-X main.version=$(VERSION)" -o "$(BIN_DIR)/$(BINARY_NAME)" "$(CMD_PATH)"

ci: automation-check format-check lint security vuln suppressions test race cov build

automation-check:
	@set -e; for script in scripts/*.sh .githooks/pre-commit examples/hooks/*; do sh -n "$$script"; done
	@command -v ruby >/dev/null 2>&1 || (echo "ruby is required to validate workflow YAML"; exit 1)
	./scripts/check-github-actions-pinning.sh
	ruby scripts/check-github-actions-runners.rb
	./scripts/check-automation-examples.sh
	./scripts/check-managed-output.sh
	ruby -e 'require "yaml"; ARGV.each { |path| YAML.load_file(path) }' .github/workflows/*.yml examples/lefthook.yml

release:
	@test -d "$(CMD_PATH)" || (echo "$(CMD_PATH) does not exist; release packaging requires the CLI entrypoint."; exit 1)
	./scripts/managed-output.sh reset "$(DIST_DIR)"
	@set -e; for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*}; \
		GOARCH=$${platform#*/}; \
		name="$(BINARY_NAME)_$(VERSION)_$${GOOS}_$${GOARCH}"; \
		output_dir="$(DIST_DIR)/$$name"; \
		./scripts/managed-output.sh reset "$$output_dir"; \
		ext=""; \
		if [ "$$GOOS" = "windows" ]; then ext=".exe"; fi; \
		echo "Building $$name"; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH $(GO_CMD) build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o "$$output_dir/$(BINARY_NAME)$$ext" "$(CMD_PATH)"; \
		if [ "$$GOOS" = "windows" ]; then \
			(cd "$(DIST_DIR)" && zip -qr "$$name.zip" "$$name" -x "$$name/.wtgc-managed-output"); \
		else \
			tar --exclude "$$name/.wtgc-managed-output" -czf "$(DIST_DIR)/$$name.tar.gz" -C "$(DIST_DIR)" "$$name"; \
		fi; \
		./scripts/managed-output.sh remove "$$output_dir"; \
	done
	./scripts/checksums.sh "$(DIST_DIR)"
	./scripts/validate-release-artifacts.sh "$(DIST_DIR)"

release-check: automation-check
	$(MAKE) release VERSION="$(VERSION)" PLATFORMS="$(PLATFORMS)"

clean:
	./scripts/managed-output.sh remove "$(BIN_DIR)"
	./scripts/managed-output.sh remove "$(DIST_DIR)"
	./scripts/managed-output.sh remove ".artifacts"

toolchain-check:
	@command -v go >/dev/null 2>&1 || (echo "go not found in PATH"; exit 1)
	@version="$$(go env GOVERSION 2>/dev/null || go version | awk '{print $$3}')"; \
	version="$${version#go}"; \
	major="$${version%%.*}"; \
	rest="$${version#*.}"; \
	minor="$${rest%%.*}"; \
	major="$${major%%[^0-9]*}"; \
	minor="$${minor%%[^0-9]*}"; \
	if [ -z "$$major" ] || [ -z "$$minor" ]; then \
		echo "Unable to parse Go version: $$version"; \
		exit 1; \
	fi; \
	if [ "$$major" -lt 1 ] || { [ "$$major" -eq 1 ] && [ "$$minor" -lt 26 ]; }; then \
		echo "Go 1.26.x or newer is required (found $$version)."; \
		exit 1; \
	fi

tools-install:
	$(GO_CMD) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO_CMD) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	$(GO_CMD) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)

setup: toolchain-check
	$(GO_CMD) mod download
	$(MAKE) tools-install
	@echo "Toolchain ready. Use: make ci"

hooks-install:
	@git config core.hooksPath .githooks
	@chmod +x .githooks/pre-commit
	@echo "Installed git hooks from .githooks"

hooks-uninstall:
	@git config --unset core.hooksPath || true
	@echo "Removed custom core.hooksPath hook configuration"
