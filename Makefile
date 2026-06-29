BIN     := veto
CMD     := ./cmd/veto
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build install test lint clean release

build:
	go build $(LDFLAGS) -o $(BIN) $(CMD)

install:
	go install $(LDFLAGS) $(CMD)

test:
	go test -race -timeout 120s ./...

lint:
	go vet ./...

clean:
	rm -f $(BIN)

release: test
	@echo "Tag with: git tag v<version> && git push origin v<version>"
