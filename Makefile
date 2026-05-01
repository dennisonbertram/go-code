PREFIX ?= $(HOME)/.local

.PHONY: build install install-system uninstall clean

build:
	mkdir -p build/bin
	go build -o build/bin/harnessd ./cmd/harnessd
	go build -o build/bin/harnesscli ./cmd/harnesscli

install:
	./scripts/install.sh --prefix "$(PREFIX)"

install-system:
	./scripts/install.sh --system

uninstall:
	./scripts/install.sh --prefix "$(PREFIX)" --uninstall

clean:
	rm -rf build/bin build/install
