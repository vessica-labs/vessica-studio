# vessica-studio — build & install the vstd engine
#
#   make build      → ./vstd (local, nothing global)
#   make install    → /usr/local/bin/vstd  (may need sudo; or PREFIX=~/bin make install)
#   make go-install → $HOME/go/bin/vstd via `go install` (no sudo; put ~/go/bin on PATH)

BIN    := vstd
PREFIX ?= /usr/local

.PHONY: build install go-install uninstall vet clean arch-lint ci-check

build:
	go build -trimpath -o $(BIN) ./cmd/vstd

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)
	@echo "installed $(PREFIX)/bin/$(BIN) — $$($(PREFIX)/bin/$(BIN) version)"

go-install:
	go install ./cmd/vstd
	@echo "installed $$(go env GOPATH)/bin/$(BIN) — ensure that dir is on PATH"

uninstall:
	rm -f $(PREFIX)/bin/$(BIN)

vet:
	go vet ./...

clean:
	rm -f $(BIN)

# Fast deterministic gate. Full regression/race/media checks precede releases.
arch-lint:
	python3 -m unittest discover -s .harness/scripts -p 'test_*.py'
	python3 .harness/scripts/arch-lint.py
	go test ./internal/architecture

ci-check: arch-lint
	test -z "$$(gofmt -l $$(find cmd internal plugin -name '*.go' -type f))"
	go mod verify
	go build -trimpath ./cmd/vstd
	git diff --check
