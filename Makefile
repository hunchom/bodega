BINARY    := yum
PREFIX    ?= $(HOME)/.local
BIN_DIR   := $(PREFIX)/bin
COMP_DIR  := $(HOME)/.zsh/completions

VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT    := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
  -X github.com/hunchom/yum/internal/version.Version=$(VERSION) \
  -X github.com/hunchom/yum/internal/version.Commit=$(COMMIT) \
  -X github.com/hunchom/yum/internal/version.Date=$(DATE)

.PHONY: all build install uninstall test lint clean completions

all: build

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o $(BINARY) ./cmd/yum

install: build completions
	install -d $(BIN_DIR)
	install -m 0755 $(BINARY) $(BIN_DIR)/$(BINARY)
	@echo "installed → $(BIN_DIR)/$(BINARY)"
	@./scripts/patch-zshrc.sh || true

completions: build
	install -d $(COMP_DIR)
	./$(BINARY) completions zsh > $(COMP_DIR)/_yum
	@echo "completions → $(COMP_DIR)/_yum"

uninstall:
	rm -f $(BIN_DIR)/$(BINARY) $(COMP_DIR)/_yum

test:
	go test -race ./...

lint:
	go vet ./...
	gofmt -l . | tee /tmp/gofmt.out; test ! -s /tmp/gofmt.out

clean:
	rm -f $(BINARY)
	go clean -testcache
