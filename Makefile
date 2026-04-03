VERSION ?= dev
VERSION_NO_V := $(patsubst v%,%,$(VERSION))
COMMIT ?= HEAD
REPO ?= nmelo/initech
RELEASE_WORKFLOW ?= Release
FORMULA ?= initech
EXPECTED_ASSETS := checksums.txt initech_darwin_amd64.tar.gz initech_darwin_arm64.tar.gz initech_linux_amd64.tar.gz initech_linux_arm64.tar.gz
LDFLAGS := -s -w -X github.com/nmelo/initech/cmd.Version=$(VERSION)
REQUIRE_RELEASE_VERSION = test -n "$(VERSION)" && case "$(VERSION)" in v*) ;; *) echo "VERSION must start with v, got $(VERSION)" >&2; exit 1 ;; esac

.PHONY: build test vet lint clean release check install-hooks release-tag release-wait release-assets release-verify release-ship

build:
	go build -ldflags "$(LDFLAGS)" -o initech .

test:
	go test ./... -count=1

vet:
	go vet ./...

lint:
	golangci-lint run ./...

clean:
	rm -f initech

check: vet test

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
	fi; \
	initech doctor

release-ship: test release-tag release-verify
	@echo "Mechanical release steps completed for $(VERSION)"

install-hooks:
	@GIT_DIR=$$(git rev-parse --git-dir) && \
	  mkdir -p "$$GIT_DIR/hooks" && \
	  printf '#!/bin/sh\nmake check\n' > "$$GIT_DIR/hooks/pre-commit" && \
	  chmod +x "$$GIT_DIR/hooks/pre-commit" && \
	  echo "pre-commit hook installed at $$GIT_DIR/hooks/pre-commit"
