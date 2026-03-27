VERSION ?= dev
LDFLAGS := -s -w -X github.com/nmelo/initech/cmd.Version=$(VERSION)

.PHONY: build test vet clean release check install-hooks

build:
	go build -ldflags "$(LDFLAGS)" -o initech .

test:
	go test ./... -count=1

vet:
	go vet ./...

clean:
	rm -f initech

check: vet test

release:
	goreleaser release --clean

install-hooks:
	@GIT_DIR=$$(git rev-parse --git-dir) && \
	  mkdir -p "$$GIT_DIR/hooks" && \
	  printf '#!/bin/sh\nmake check\n' > "$$GIT_DIR/hooks/pre-commit" && \
	  chmod +x "$$GIT_DIR/hooks/pre-commit" && \
	  echo "pre-commit hook installed at $$GIT_DIR/hooks/pre-commit"
