.PHONY: format fmt format-check lint security vuln suppressions test perf-check race cov build ci release release-check automation-check queue-me-check clean toolchain-check tools-install setup hooks-install hooks-uninstall

PACKAGE_PATTERN ?= ./...
BINARY_NAME ?= wtgc
CMD_PATH ?= ./cmd/wtgc
BIN_DIR ?= bin
DIST_DIR ?= dist
VERSION ?= dev
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || date -u '+%Y-%m-%dT%H:%M:%SZ')
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct HEAD 2>/dev/null || date -u '+%s')
COVERAGE_FILE ?= .artifacts/coverage.out
COVERAGE_TOTAL_FILE ?= $(dir $(COVERAGE_FILE))coverage-total.txt
COVERAGE_MIN ?= 85
GO ?= go
GO_TOOLCHAIN ?= go1.26.5
GO_CMD := GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO)
GOLANGCI_LINT_VERSION ?= v2.9.0
GOSEC_VERSION ?= v2.22.11
GOSEC_FLAGS ?= -exclude=G204
GOVULNCHECK_VERSION ?= v1.7.0
HOST_GOOS := $(shell $(GO_CMD) env GOOS)
HOST_GOARCH := $(shell $(GO_CMD) env GOARCH)
PLATFORMS ?= $(HOST_GOOS)/$(HOST_GOARCH)
LD_FLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)
RELEASE_LD_FLAGS := -s -w -buildid= $(LD_FLAGS)

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

perf-check:
	$(GO_CMD) test ./internal/app -run '^$$' -bench '^BenchmarkRunClassifies350Worktrees$$' -benchtime=1x -timeout=30s

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
	$(GO_CMD) build -trimpath -buildvcs=false -ldflags="$(LD_FLAGS)" -o "$(BIN_DIR)/$(BINARY_NAME)" "$(CMD_PATH)"

ci: automation-check format-check lint security vuln suppressions test perf-check race cov build

automation-check:
	@set -e; for script in scripts/*.sh .githooks/pre-commit examples/hooks/*; do sh -n "$$script"; done
	@command -v ruby >/dev/null 2>&1 || (echo "ruby is required to validate workflow YAML"; exit 1)
	./scripts/check-github-actions-pinning.sh
	ruby scripts/check-github-actions-runners.rb
	./scripts/check-automation-examples.sh
	sh ./scripts/check-release-automation.sh
	./scripts/check-managed-output.sh
	ruby -e 'require "yaml"; ARGV.each { |path| YAML.load_file(path) }' .github/workflows/*.yml examples/lefthook.yml
	ruby -rjson -e 'ARGV.each { |path| JSON.parse(File.read(path)) }' release-please-config.json .release-please-manifest.json
	$(MAKE) queue-me-check

queue-me-check:
	@command -v node >/dev/null 2>&1 || (echo "node is required to test the queue-me controller"; exit 1)
	$(GO_CMD) test ./scripts

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
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH $(GO_CMD) build -trimpath -buildvcs=false -ldflags="$(RELEASE_LD_FLAGS)" -o "$$output_dir/$(BINARY_NAME)$$ext" "$(CMD_PATH)"; \
		cp LICENSE "$$output_dir/LICENSE"; \
		cp README.md "$$output_dir/README.md"; \
		archive_ext=".tar.gz"; \
		if [ "$$GOOS" = "windows" ]; then archive_ext=".zip"; fi; \
		$(GO_CMD) run ./tools/releasepack --epoch "$(SOURCE_DATE_EPOCH)" "$$output_dir" "$(DIST_DIR)/$$name$$archive_ext"; \
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
	patch="$${rest#*.}"; \
	major="$${major%%[^0-9]*}"; \
	minor="$${minor%%[^0-9]*}"; \
	patch="$${patch%%[^0-9]*}"; \
	if [ "$$patch" = "$$rest" ]; then patch=0; fi; \
	if [ -z "$$major" ] || [ -z "$$minor" ] || [ -z "$$patch" ]; then \
		echo "Unable to parse Go version: $$version"; \
		exit 1; \
	fi; \
	if [ "$$major" -lt 1 ] || { [ "$$major" -eq 1 ] && { [ "$$minor" -lt 26 ] || { [ "$$minor" -eq 26 ] && [ "$$patch" -lt 5 ]; }; }; }; then \
		echo "Go 1.26.5 or newer is required (found $$version)."; \
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
