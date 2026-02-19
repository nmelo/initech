VERSION ?= dev
LDFLAGS := -s -w -X github.com/nmelo/initech/cmd.Version=$(VERSION)

.PHONY: build test vet clean release check

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
