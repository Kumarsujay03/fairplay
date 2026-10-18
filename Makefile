.PHONY: all web build test run install uninstall release clean

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
PREFIX  ?= $(HOME)/.local

all: build

web:
	cd web && npm ci --no-audit --no-fund && npm run build
	touch internal/webui/dist/.keep

build: web
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/fairplay ./cmd/fairplay

test:
	go test -race ./...
	cd web && npm run check

run: build
	./bin/fairplay

# Build from source and put `fairplay` on your PATH (~/.local/bin by default).
install: build
	install -d $(PREFIX)/bin
	install -m 755 bin/fairplay $(PREFIX)/bin/fairplay
	@echo "Installed $(PREFIX)/bin/fairplay. Start it with: fairplay"

uninstall:
	rm -f $(PREFIX)/bin/fairplay
	@echo "Removed $(PREFIX)/bin/fairplay. Your invites and settings are kept; delete ~/.config/fairplay and ~/.cache/fairplay to remove them too."

release:
	./build.sh $(VERSION)

clean:
	rm -rf bin dist internal/webui/dist/assets internal/webui/dist/index.html
