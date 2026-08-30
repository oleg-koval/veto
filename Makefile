BIN     := veto
CMD     := ./cmd/veto
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build install test lint clean release release-check benchmark agent-skill-check

build:
	go build $(LDFLAGS) -o $(BIN) $(CMD)

install:
	go install $(LDFLAGS) $(CMD)

test:
	go test -race -timeout 120s ./...

benchmark:
	@go run ./cmd/veto benchmark --corpus internal/eval/testdata/routing_corpus.json

agent-skill-check:
	@./scripts/agent-skill-smoke.sh

lint:
	go vet ./...

clean:
	rm -f $(BIN)

release-check: test lint
	@test -n "$(RELEASE_VERSION)" || { echo "RELEASE_VERSION is required (for example, 0.1.0)" >&2; exit 1; }
	@./scripts/release-notes.sh "v$(RELEASE_VERSION)" >/dev/null
	@release_dist=$$(mktemp -d); trap 'rm -rf "$$release_dist"' EXIT; ./scripts/package-release.sh "v$(RELEASE_VERSION)" "$$release_dist"; ./scripts/render-homebrew-formula.sh "v$(RELEASE_VERSION)" "$$release_dist/SHA256SUMS" "$$release_dist/veto.rb"
	goreleaser check
	goreleaser release --snapshot --clean

release: release-check
	@echo "Ready to tag after owner approval:"
	@echo "  git tag -a v$(RELEASE_VERSION) -m 'v$(RELEASE_VERSION)'"
	@echo "  git push origin v$(RELEASE_VERSION)"
