# Plainshow Cluster
#
# One binary. `make` builds it, `make check` is what CI runs.

BINARY  := pscluster
PKG     := ./cmd/pscluster
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X github.com/huggan360/plainshow-cluster/internal/version.Version=$(VERSION) \
	-X github.com/huggan360/plainshow-cluster/internal/version.Commit=$(COMMIT)

.PHONY: all build check test vet fmt web clean install dist run smoke

all: build

## build: compile the binary for this machine
build: web
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "built ./$(BINARY)  $(VERSION)"

## web: verify the interface's module graph before embedding it
web:
	@node scripts/check-web.mjs

## check: everything CI runs
check: fmt vet web test

## fmt: fail if anything is unformatted
fmt:
	@out=$$(gofmt -l . 2>/dev/null); \
	if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi
	@echo "formatting clean"

vet:
	go vet ./...

## test: run the suite. The race detector needs a kernel with a 48-bit VMA;
## some arm64 boards report 47 and ThreadSanitizer refuses to start there, so
## race is a separate target rather than the default.
test:
	go test -count=1 ./...

race:
	go test -race -count=1 ./...

## smoke: end-to-end check against a throwaway node on port 9971
smoke: build
	@rm -rf .smokenode
	@./$(BINARY) init --root ./.smokenode --name smoke --cluster smoke --port 9971 >/dev/null
	@./$(BINARY) serve --root ./.smokenode >/dev/null 2>&1 & echo $$! > .smokepid
	@for i in $$(seq 1 40); do \
		curl -sf http://127.0.0.1:9971/api/overview >/dev/null 2>&1 && break; \
		sleep 0.3; \
	done
	@PSCLUSTER_URL=http://127.0.0.1:9971 node scripts/smoke.mjs; \
		status=$$?; kill $$(cat .smokepid) 2>/dev/null; \
		rm -rf .smokenode .smokepid; exit $$status

## dist: cross-compile for the machines a cluster is actually made of
dist: web
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 $(PKG)
	@ls -lh dist/

## install: copy the binary into the install root's bin directory
install: build
	@./install.sh

## run: build and start a throwaway node in ./.devnode
run: build
	./$(BINARY) init --root ./.devnode --name dev --cluster dev-cluster 2>/dev/null || true
	./$(BINARY) serve --root ./.devnode

clean:
	rm -rf $(BINARY) dist .devnode .smokenode .smokepid
