.PHONY: format fmt format-check gostyle lint actionlint shellcheck mod-check quality-gate-contracts feature-flag-check harness-check dup-check duplication-contracts security vuln suppressions suppression-contracts test test-leaks perf-check bench-gate bench-contracts fuzz-corpus-check ci-tools-cov race cov build ci release release-check automation-check queue-me-check clean toolchain-check tools-install setup hooks-install hooks-uninstall

PACKAGE_PATTERN ?= ./...
BINARY_NAME ?= wtgc
CMD_PATH ?= ./cmd/wtgc
BIN_DIR ?= bin
DIST_DIR ?= dist
VERSION ?= dev
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || date -u '+%Y-%m-%dT%H:%M:%SZ')
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct HEAD 2>/dev/null || date -u '+%s')
QUALITY_ARTIFACT_DIR ?= .artifacts/ci-gates
COVERAGE_FILE ?= $(QUALITY_ARTIFACT_DIR)/coverage.out
COVERAGE_TOTAL_FILE ?= $(QUALITY_ARTIFACT_DIR)/coverage-total.txt
GO ?= go
GO_TOOLCHAIN ?= go1.26.6
GO_CMD := GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO)
GOLANGCI_LINT_VERSION ?= v2.9.0
GOSTYLE_VERSION ?= v0.25.3
ACTIONLINT_VERSION ?= v1.7.12
SHELLCHECK_VERSION ?= v0.11.1
ACTIONLINT_FILES ?= .github/workflows/*.yml
SHELLCHECK_FILES ?= $(shell find scripts .githooks -type f \( -name '*.sh' -o -path '.githooks/*' \) -print 2>/dev/null)
GOSEC_VERSION ?= v2.22.11
GOSEC_FLAGS ?= -exclude=G204
GOVULNCHECK_VERSION ?= v1.7.0
HOST_GOOS := $(shell $(GO_CMD) env GOOS)
HOST_GOARCH := $(shell $(GO_CMD) env GOARCH)
PLATFORMS ?= $(HOST_GOOS)/$(HOST_GOARCH)
LD_FLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)
RELEASE_LD_FLAGS := -s -w -buildid= $(LD_FLAGS)
DUPLICATION_BASE ?= origin/main
DUPLICATION_MAX ?= 3
DUPLICATION_TOKEN_THRESHOLD ?= 55
MEMORY_BENCH_BASE ?= origin/main
MEMORY_BENCH_MAX_BYTES_PCT ?= 15
MEMORY_BENCH_MAX_ALLOCS_PCT ?= 10
BENCH_COUNT ?= 3
BENCH_TIME ?= 200ms

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

gostyle:
	GOFLAGS=-buildvcs=false $(GO_CMD) run github.com/k1LoW/gostyle@$(GOSTYLE_VERSION) run -c .gostyle.yml ./...

lint: gostyle
	$(GO_CMD) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run $(PACKAGE_PATTERN)

actionlint:
	GOFLAGS=-buildvcs=false $(GO_CMD) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) $(ACTIONLINT_FILES)

shellcheck:
	@files='$(SHELLCHECK_FILES)'; \
	if [ -z "$$files" ]; then echo "No shell scripts found for ShellCheck."; exit 0; fi; \
	printf '%s\n' "$$files" | xargs -n 1 env GOFLAGS=-buildvcs=false $(GO_CMD) run github.com/wasilibs/go-shellcheck/cmd/shellcheck@$(SHELLCHECK_VERSION) --shell=sh

mod-check:
	$(GO_CMD) mod tidy -diff
	$(GO_CMD) mod verify

quality-gate-contracts:
	./scripts/check-quality-gate-contract-test.sh

feature-flag-check:
	$(GO_CMD) run ./tools/featureflag --file .ci/feature-flags.json

harness-check:
	./scripts/check-harness-skill.sh
	./scripts/harness-skill-safety-smoke.sh

dup-check:
	DUPLICATION_BASE="$(DUPLICATION_BASE)" DUPLICATION_MAX="$(DUPLICATION_MAX)" DUPLICATION_TOKEN_THRESHOLD="$(DUPLICATION_TOKEN_THRESHOLD)" ./scripts/check-duplication.sh

duplication-contracts:
	./scripts/check-duplication-contract-test.sh

security:
	$(GO_CMD) run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) $(GOSEC_FLAGS) $(PACKAGE_PATTERN)

vuln:
	$(GO_CMD) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) $(PACKAGE_PATTERN)

suppression-base := $(if $(SUPPRESSION_BASE),$(SUPPRESSION_BASE),$(DUPLICATION_BASE))
suppression-contracts:
	./scripts/check-suppressions-contract-test.sh
	node ./scripts/suppression-accountability-workflow-test.mjs

suppressions: suppression-contracts
	SUPPRESSION_BASE="$(suppression-base)" ./scripts/check-suppressions.sh

test:
	$(GO_CMD) test $(PACKAGE_PATTERN)

test-leaks:
	WTGC_GOLEAK=1 $(GO_CMD) test $(PACKAGE_PATTERN)

perf-check: bench-gate

bench-gate:
	@set -eu; base_ref="$(MEMORY_BENCH_BASE)"; \
	if ! git rev-parse --verify -q "$$base_ref^{commit}" >/dev/null; then echo "memory benchmark base ref '$$base_ref' is unavailable; set MEMORY_BENCH_BASE to an explicit fetched commit/ref"; exit 1; fi; \
	base_commit=$$(git merge-base "$$base_ref" HEAD) || { echo "memory benchmark base is unrelated to HEAD"; exit 1; }; \
	workdir=$$(mktemp -d); base_tree="$$workdir/base"; base_out="$$workdir/base.out"; head_out="$$workdir/head.out"; pid_file="$$workdir/benchmark.pid"; \
	owner_pid=$$(ps -o ppid= -p $$$$ | tr -d ' '); \
	( while kill -0 "$$owner_pid" >/dev/null 2>&1; do sleep 1; done; if [ -s "$$pid_file" ]; then benchmark_pid=$$(cat "$$pid_file"); child_pids=$$(pgrep -P "$$benchmark_pid" 2>/dev/null || true); if [ -n "$$child_pids" ]; then kill -TERM $$child_pids >/dev/null 2>&1 || true; fi; kill -TERM "$$benchmark_pid" >/dev/null 2>&1 || true; deadline=$$(( $$(date +%s) + 5 )); while { kill -0 "$$benchmark_pid" >/dev/null 2>&1 || [ -n "$$child_pids" ]; } && [ "$$(date +%s)" -lt "$$deadline" ]; do live_children=$$(for child_pid in $$child_pids; do kill -0 "$$child_pid" >/dev/null 2>&1 && printf '%s ' "$$child_pid"; done); [ -z "$$live_children" ] && child_pids=''; sleep 1; done; if [ -n "$$child_pids" ]; then kill -KILL $$child_pids >/dev/null 2>&1 || true; fi; kill -KILL "$$benchmark_pid" >/dev/null 2>&1 || true; fi; git worktree remove --force "$$base_tree" >/dev/null 2>&1 || true; rm -rf "$$workdir" ) & watchdog=$$!; \
	cleanup() { kill "$$watchdog" >/dev/null 2>&1 || true; wait "$$watchdog" >/dev/null 2>&1 || true; git worktree remove --force "$$base_tree" >/dev/null 2>&1 || true; rm -rf "$$workdir"; }; trap cleanup EXIT; trap 'cleanup; exit 129' HUP; trap 'cleanup; exit 130' INT; trap 'cleanup; exit 142' ALRM; trap 'cleanup; exit 143' TERM; \
	git worktree add --detach "$$base_tree" "$$base_commit" >/dev/null; \
	run_benchmark() { (cd "$$1" && exec env GOFLAGS=-buildvcs=false GOTOOLCHAIN=$(GO_TOOLCHAIN) $(GO) test ./internal/app -run '^$$' -bench '^BenchmarkRunClassifies350Worktrees$$' -benchmem -count=$(BENCH_COUNT) -benchtime=$(BENCH_TIME) -timeout=90s) > "$$2" 2>&1 & benchmark_pid=$$!; printf '%s\n' "$$benchmark_pid" > "$$pid_file"; if ! wait "$$benchmark_pid"; then rm -f "$$pid_file"; cat "$$2"; return 1; fi; rm -f "$$pid_file"; }; \
	run_benchmark "$$base_tree" "$$base_out" || exit 1; \
	run_benchmark . "$$head_out" || exit 1; \
	./scripts/managed-output.sh ensure "$(QUALITY_ARTIFACT_DIR)"; cp "$$base_out" "$(QUALITY_ARTIFACT_DIR)/bench-base.out"; cp "$$head_out" "$(QUALITY_ARTIFACT_DIR)/bench-head.out"; \
	$(GO_CMD) run ./tools/benchdelta --base "$(QUALITY_ARTIFACT_DIR)/bench-base.out" --head "$(QUALITY_ARTIFACT_DIR)/bench-head.out" --min-samples $(BENCH_COUNT) --max-bytes-pct $(MEMORY_BENCH_MAX_BYTES_PCT) --max-allocs-pct $(MEMORY_BENCH_MAX_ALLOCS_PCT) --summary-out "$(QUALITY_ARTIFACT_DIR)/memory-bench-summary.md"

bench-contracts:
	./scripts/check-bench-gate-contract-test.sh

fuzz-corpus-check:
	$(GO_CMD) test ./internal/gitx -run '^TestFuzzCorpusContract$$'
	$(GO_CMD) test ./internal/gitx -run '^$$' -fuzz '^FuzzParseWorktreeListPorcelainZ$$' -fuzztime=1x
	$(GO_CMD) test ./internal/gitx -run '^$$' -fuzz '^FuzzDiscoverRootInput$$' -fuzztime=1x

ci-tools-cov:
	@./scripts/managed-output.sh ensure "$(QUALITY_ARTIFACT_DIR)"
	$(GO_CMD) test ./tools/benchdelta ./tools/coveragegate ./tools/featureflag -covermode=atomic -coverprofile="$(QUALITY_ARTIFACT_DIR)/ci-tools-coverage.out"
	$(GO_CMD) run ./tools/coveragegate --coverprofile "$(QUALITY_ARTIFACT_DIR)/ci-tools-coverage.out" --config .ci/ci-tools-coverage.json --total-out "$(QUALITY_ARTIFACT_DIR)/ci-tools-coverage-total.json" --packages-out "$(QUALITY_ARTIFACT_DIR)/ci-tools-coverage-packages.json" --package-failures-out "$(QUALITY_ARTIFACT_DIR)/ci-tools-coverage-failures.json"

race:
	$(GO_CMD) test -race $(PACKAGE_PATTERN)

cov:
	@./scripts/managed-output.sh ensure "$$(dirname "$(COVERAGE_FILE)")"
	@packages=$$($(GO_CMD) list $(PACKAGE_PATTERN) | grep -v '/integration$$' | grep -v '/internal/testgit$$'); \
	$(GO_CMD) test $$packages -covermode=atomic -coverprofile="$(COVERAGE_FILE)"
	$(GO_CMD) run ./tools/coveragegate --coverprofile "$(COVERAGE_FILE)" --config .ci/coverage-ratchet.json --total-out "$(COVERAGE_TOTAL_FILE:.txt=.json)" --packages-out "$(QUALITY_ARTIFACT_DIR)/coverage-packages.json" --package-failures-out "$(QUALITY_ARTIFACT_DIR)/coverage-package-failures.json"

build:
	./scripts/managed-output.sh ensure "$(BIN_DIR)"
	$(GO_CMD) build -trimpath -buildvcs=false -ldflags="$(LD_FLAGS)" -o "$(BIN_DIR)/$(BINARY_NAME)" "$(CMD_PATH)"

ci: quality-gate-contracts mod-check feature-flag-check harness-check automation-check actionlint shellcheck format-check lint duplication-contracts dup-check security vuln suppressions test test-leaks fuzz-corpus-check bench-contracts bench-gate ci-tools-cov race cov build

automation-check:
	@set -e; for script in scripts/*.sh .githooks/pre-commit examples/hooks/*; do sh -n "$$script"; done
	@command -v ruby >/dev/null 2>&1 || (echo "ruby is required to validate workflow YAML"; exit 1)
	./scripts/check-github-actions-pinning.sh
	ruby scripts/check-github-actions-runners.rb
	./scripts/check-automation-examples.sh
	sh ./scripts/check-release-automation.sh
	./scripts/check-managed-output.sh
	$(MAKE) mod-check feature-flag-check
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
	if [ "$$major" -lt 1 ] || { [ "$$major" -eq 1 ] && { [ "$$minor" -lt 26 ] || { [ "$$minor" -eq 26 ] && [ "$$patch" -lt 6 ]; }; }; }; then \
		echo "Go 1.26.6 or newer is required (found $$version)."; \
		exit 1; \
	fi

tools-install:
	$(GO_CMD) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO_CMD) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	$(GO_CMD) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO_CMD) install github.com/k1LoW/gostyle@$(GOSTYLE_VERSION)
	$(GO_CMD) install github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION)
	$(GO_CMD) install github.com/wasilibs/go-shellcheck/cmd/shellcheck@$(SHELLCHECK_VERSION)

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
