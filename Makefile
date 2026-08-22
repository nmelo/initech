VERSION ?= dev
VERSION_NO_V := $(patsubst v%,%,$(VERSION))
COMMIT ?= HEAD
REPO ?= nmelo/initech
RELEASE_WORKFLOW ?= Release
FORMULA ?= initech
EXPECTED_ASSETS := checksums.txt initech_darwin_amd64.tar.gz initech_darwin_arm64.tar.gz initech_linux_amd64.tar.gz initech_linux_arm64.tar.gz
LDFLAGS := -s -w -X github.com/nmelo/initech/cmd.Version=$(VERSION)
REQUIRE_RELEASE_VERSION = test -n "$(VERSION)" && case "$(VERSION)" in v*) ;; *) echo "VERSION must start with v, got $(VERSION)" >&2; exit 1 ;; esac

.PHONY: build test test-full integration vet lint test-census rig-census lint-test-names lint-test-names-self-test clean release check install-hooks hooks-check release-tag release-wait release-assets release-verify release-ship

build:
	go build -ldflags "$(LDFLAGS)" -o initech .

test:
	go test ./... -count=1 -short

test-full:
	go test ./... -count=1

test-race:
	go test ./... -count=1 -race

integration:
	go test ./... -count=1 -run 'Integ|Watchdog|RenderNotBlocked|DaemonAuth|AddPane_(Success|SetsGoroutines|EventChWired|EnvInjected|GridRecalculated|NoConfigModification)|NoBracketedPaste|WaitsForReady|StashSkipsRetry|ResumesSuspended|ResizeDebounce'

vet:
	go vet ./...

# Cross-platform vet. `go vet` type-checks _test.go files too, so this catches
# a test file that consumes a symbol from a '//go:build !windows' sibling
# without matching the constraint -- twice in two days (ini-vfk, ini-69w).
# It cross-compiles without running anything, so it costs seconds on darwin.
#
# In `check`, therefore in the pre-commit hook: the ini-vfk fix was a manual
# sweep that was correct when run and stale one bead later, because the
# offending file did not exist yet. Manual sweeps rot; a check in the loop
# every commit runs does not. Catching this at COMMIT time on every machine
# beats catching it at TAG time in CI, which is where it cost a release cut.
vet-windows:
	GOOS=windows GOARCH=amd64 go vet ./...

lint:
	golangci-lint run ./...

# Reject test names ending in _NoOp / _NoPanic / _DoesNotPanic / _Smoke.
# Names admit "we just call it and see if it crashes" intent — either add
# a real assertion or pick an honest name. Override per-test with
# // lint:test-name-allow <reason> directly above the function. ini-ybe.1.
lint-test-names:
	@bash scripts/lint-test-names.sh

# Self-test the lint script itself (fixture-driven, no impact on the repo).
lint-test-names-self-test:
	@bash scripts/lint-test-names_test.sh

clean:
	rm -f initech

# ini-2x8.8: fail when a test file compiles on one platform and not another
# without a declared exemption. Sits next to vet-windows on purpose -- both
# answer a cross-platform question STATICALLY, so both belong at commit time
# rather than waiting for the CI matrix. vet-windows catches "this file no
# longer compiles there"; test-census catches "this file is no longer THERE".
test-census:
	@go run ./scripts/testcensus

# ini-0lko: fail when an env-gated rig exists that no CI job runs. Sits beside
# test-census for the same reason -- it answers a question about the INVENTORY
# statically, so it belongs at commit time. The composed-rigs job names each rig
# twice (an env: block and a -run selector) and both lists are hand-maintained;
# INITECH_9IMX was never in either for its whole life while the job reported
# green. Derive the answer instead of trusting the list.
rig-census:
	@go run ./scripts/rigcensus

check: hooks-check vet vet-windows test-census rig-census lint-test-names test

release:
	@set -eu; \
	TOKEN=$$(gh auth token); \
	GITHUB_TOKEN="$$TOKEN" HOMEBREW_TAP_TOKEN="$$TOKEN" goreleaser release --clean

release-tag:
	@set -eu; \
	$(REQUIRE_RELEASE_VERSION); \
	git fetch origin --tags --force; \
	git rev-parse --verify --quiet "$(COMMIT)^{commit}" >/dev/null; \
	if git rev-parse --verify --quiet "refs/tags/$(VERSION)" >/dev/null; then \
		echo "local tag $(VERSION) already exists" >&2; \
		exit 1; \
	fi; \
	if git ls-remote --exit-code --tags origin "refs/tags/$(VERSION)" >/dev/null 2>&1; then \
		echo "remote tag $(VERSION) already exists" >&2; \
		exit 1; \
	fi; \
	git tag -a "$(VERSION)" "$(COMMIT)" -m "$(VERSION)"; \
	git push origin "refs/tags/$(VERSION)"

release-wait:
	@set -eu; \
	$(REQUIRE_RELEASE_VERSION); \
	run_id=""; \
	attempt=0; \
	while [ $$attempt -lt 30 ]; do \
		run_id=$$(gh run list --repo "$(REPO)" --workflow "$(RELEASE_WORKFLOW)" --branch "$(VERSION)" --limit 1 --json databaseId --jq '.[0].databaseId'); \
		if [ -n "$$run_id" ] && [ "$$run_id" != "null" ]; then \
			break; \
		fi; \
		attempt=$$((attempt + 1)); \
		sleep 2; \
	done; \
	if [ -z "$$run_id" ] || [ "$$run_id" = "null" ]; then \
		echo "no $(RELEASE_WORKFLOW) workflow found for $(VERSION)" >&2; \
		exit 1; \
	fi; \
	echo "Watching workflow $$run_id for $(VERSION)"; \
	gh run watch "$$run_id" --repo "$(REPO)" --exit-status

release-assets:
	@set -eu; \
	$(REQUIRE_RELEASE_VERSION); \
	asset_names=$$(mktemp); \
	tmpdir=$$(mktemp -d); \
	trap 'rm -f "$$asset_names"; rm -rf "$$tmpdir"' EXIT HUP INT TERM; \
	gh release view "$(VERSION)" --repo "$(REPO)" --json assets --jq '.assets[].name' > "$$asset_names"; \
	for asset in $(EXPECTED_ASSETS); do \
		if ! grep -Fx "$$asset" "$$asset_names" >/dev/null; then \
			echo "missing release asset: $$asset" >&2; \
			cat "$$asset_names" >&2; \
			exit 1; \
		fi; \
	done; \
	gh release download "$(VERSION)" --repo "$(REPO)" --pattern checksums.txt --dir "$$tmpdir"; \
	test -s "$$tmpdir/checksums.txt"; \
	echo "Release assets verified for $(VERSION)"

release-verify: release-wait release-assets
	@set -eu; \
	$(REQUIRE_RELEASE_VERSION); \
	brew update; \
	if ! brew cat "$(FORMULA)" | grep -F 'version "$(VERSION_NO_V)"' >/dev/null; then \
		echo "brew formula $(FORMULA) not updated to $(VERSION_NO_V)" >&2; \
		exit 1; \
	fi; \
	brew upgrade "$(FORMULA)"; \
	actual_version=$$(initech version); \
	if [ "$$actual_version" != "initech $(VERSION_NO_V)" ]; then \
		echo "initech version mismatch: $$actual_version" >&2; \
		exit 1; \
	fi

release-ship: test release-tag release-verify
	@echo "Mechanical release steps completed for $(VERSION)"

# Activation, not installation (ini-3nzc): the hook itself is VERSIONED at
# scripts/hooks/pre-commit -- one source of truth instead of a copy per clone
# that 6 of 8 fleet checkouts never made. This target just points git at it.
install-hooks:
	@git config core.hooksPath scripts/hooks && \
	  echo "core.hooksPath -> scripts/hooks (versioned pre-commit active)"

# hooks-check (ini-3nzc): fails make check loudly when the invoking checkout
# has no hook wiring. WIRING check only -- cheap enough to run every time.
# The INVOCATION proof (a failing hook must block a commit) lives in
# internal/git's hook tests and in the per-checkout acceptance probe; an
# installed-but-never-invoked hook is the presence-trap in git costume.
#
# Two accepted wirings: core.hooksPath -> scripts/hooks (the ini-3nzc way),
# or an executable pre-commit in the resolved hooks dir (grandfathered hand
# installs). NOTE the submodule trap in the failure text: src/.git here is a
# FILE, so the hooks dir lives under .git/modules/<agent>/src/hooks in the
# WORKSPACE repo -- six agents read "install the hook", looked at src/.git,
# and reasonably concluded hooks were not installable.
hooks-check:
	@if [ "$$(git config core.hooksPath)" = "scripts/hooks" ]; then \
	  :; \
	elif [ -x "$$(git rev-parse --git-path hooks)/pre-commit" ]; then \
	  :; \
	else \
	  echo ""; \
	  echo "hooks-check: THIS CHECKOUT HAS NO PRE-COMMIT HOOK WIRING."; \
	  echo ""; \
	  echo "  Commits here can push code that fails 'make check' (this is how a"; \
	  echo "  release-gating red main shipped: ini-3nzc). Fix, one command:"; \
	  echo ""; \
	  echo "      make install-hooks"; \
	  echo ""; \
	  echo "  (Sets core.hooksPath to the VERSIONED scripts/hooks. Note: src/.git"; \
	  echo "  is a gitfile in this fleet's submodule layout -- the old hooks dir is"; \
	  echo "  at .git/modules/<agent>/src/hooks in the workspace repo, which is why"; \
	  echo "  looking for src/.git/hooks made hooks seem uninstallable.)"; \
	  echo ""; \
	  echo "  Then verify INVOCATION, not presence: point core.hooksPath at a dir"; \
	  echo "  whose pre-commit is 'exit 1' and confirm an empty commit is BLOCKED,"; \
	  echo "  then run make install-hooks again."; \
	  exit 1; \
	fi
