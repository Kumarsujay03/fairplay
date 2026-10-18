.PHONY: all web build test run clean release

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

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

release: web
	for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/fairplay-$$os-$$arch$$ext ./cmd/fairplay; \
	done

clean:
	rm -rf bin dist internal/webui/dist/assets internal/webui/dist/index.html
