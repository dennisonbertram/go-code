PREFIX ?= $(HOME)/.local

# Packages exercised by the race detector: the concurrency-critical core.
# Excludes internal/cloudscheduler, which has pre-existing Docker-dependent
# tests that require a live Docker daemon.
RACE_PKGS := ./internal/harness/... ./internal/server/... ./internal/workflow/... ./cmd/harnessd/... ./cmd/harnesscli/...

.PHONY: build install install-system uninstall clean test test-race test-e2e

build:
	mkdir -p build/bin
	go build -o build/bin/harnessd ./cmd/harnessd
	go build -o build/bin/harnesscli ./cmd/harnesscli

# test runs the fast, non-race test suite (matches CI's test-fast job).
test:
	go test ./internal/... ./cmd/... -count=1

# test-race runs the race detector over the concurrency-critical packages
# (matches CI's test-race job). Race builds are slow, so this is scoped
# rather than run over the full ./... tree.
test-race:
	go test -race -count=1 -timeout=20m $(RACE_PKGS)

# test-e2e runs the end-to-end integration suite: a real in-process HTTP
# server driven over real HTTP/SSE with a scripted fake provider.
test-e2e:
	go test -race -count=1 ./test/e2e/...

install:
	./scripts/install.sh --prefix "$(PREFIX)"

install-system:
	./scripts/install.sh --system

uninstall:
	./scripts/install.sh --prefix "$(PREFIX)" --uninstall

clean:
	rm -rf build/bin build/install
