# vessica-studio — build & install the vstd engine
#
#   make build      → ./vstd (local, nothing global)
#   make install    → /usr/local/bin/vstd  (may need sudo; or PREFIX=~/bin make install)
#   make go-install → $HOME/go/bin/vstd via `go install` (no sudo; put ~/go/bin on PATH)

BIN    := vstd
PREFIX ?= /usr/local

.PHONY: build install go-install uninstall vet clean

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
