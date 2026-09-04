# Plainshow Cluster
#
# `make` builds the device and global account/key service. `make check` is
# what CI runs.

BINARY            := pscluster
PKG               := ./cmd/pscluster
ADMIN_BINARY      := pscluster-admin
ADMIN_PKG         := ./cmd/pscluster-admin
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X github.com/huggan360/plainshow-cluster/internal/version.Version=$(VERSION) \
	-X github.com/huggan360/plainshow-cluster/internal/version.Commit=$(COMMIT)

.PHONY: all build check test vet fmt web installer-check clean install install-admin dist arch-package release run smoke

all: build

## build: compile the device and global admin binaries for this machine
build: web
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(ADMIN_BINARY) $(ADMIN_PKG)
	@echo "built ./$(BINARY) and ./$(ADMIN_BINARY)  $(VERSION)"

## web: verify the interface's module graph before embedding it
web:
	@node scripts/check-web.mjs

## installer-check: parse the portable installer and native Arch package hooks
installer-check:
	@sh -n install.sh
	@bash -n packaging/arch/plainshow-cluster.install
	@sed -e 's/@VERSION@/1.0.0/g' -e 's/@ARCH@/x86_64/g' \
		-e 's/@BINARY_SHA256@/abc/g' -e 's/@SERVICE_SHA256@/def/g' \
		packaging/arch/PKGBUILD.in | bash -n
	@echo "installer and Arch packaging syntax clean"

## check: everything CI runs
check: fmt vet web installer-check test

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
##
## Refuses to start when the port is busy. A previous run left running, or a
## development node on the same port, produces a scatter of unrelated failures
## that looks like a regression and is not.
smoke: build
	@if ss -tln 2>/dev/null | grep -q ':9971 '; then \
		echo "port 9971 is already in use — stop the node using it and retry"; \
		exit 1; \
	fi
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

## dist: build the release assets, named as the updater expects to find them
##
## Asset names are a contract: internal/updater looks for pscluster-<os>-<arch>
## and reads checksums.txt in sha256sum format. Renaming either breaks every
## node's ability to update itself, silently, because a release with nothing for
## a platform simply looks like no release at all.
dist: web
	@rm -rf dist && mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 $(PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 $(PKG)
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(ADMIN_BINARY)-linux-amd64 $(ADMIN_PKG)
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(ADMIN_BINARY)-linux-arm64 $(ADMIN_PKG)
	@cp install.sh dist/install.sh && chmod 755 dist/install.sh
	@cd dist && sha256sum *-linux-* install.sh > checksums.txt
	@echo
	@ls -lh dist/
	@echo
	@cat dist/checksums.txt

## arch-package: build a native pacman package for this Arch machine
##
## makepkg is deliberately not installed by this target. On Arch it comes from
## pacman/base-devel; release CI also builds and publishes the x86_64 package.
arch-package: dist
	@command -v makepkg >/dev/null 2>&1 || { \
		echo "makepkg is required; on Arch install the base-devel group"; exit 1; \
	}
	@set -eu; \
	case "$$(uname -m)" in \
		x86_64) asset_arch=amd64; package_arch=x86_64 ;; \
		aarch64) asset_arch=arm64; package_arch=aarch64 ;; \
		*) echo "unsupported Arch package architecture: $$(uname -m)"; exit 1 ;; \
	esac; \
	version=$$(printf '%s' "$(VERSION)" | sed 's/^v//; s/[^[:alnum:].+_]/./g'); \
	work=dist/arch-package; \
	mkdir -p "$$work"; \
	cp "dist/$(BINARY)-linux-$$asset_arch" "$$work/pscluster"; \
	cp packaging/arch/plainshow-cluster.service packaging/arch/plainshow-cluster.install "$$work/"; \
	binary_sha=$$(sha256sum "$$work/pscluster" | cut -d ' ' -f 1); \
	service_sha=$$(sha256sum "$$work/plainshow-cluster.service" | cut -d ' ' -f 1); \
	sed -e "s/@VERSION@/$$version/g" -e "s/@ARCH@/$$package_arch/g" \
		-e "s/@BINARY_SHA256@/$$binary_sha/g" -e "s/@SERVICE_SHA256@/$$service_sha/g" \
		packaging/arch/PKGBUILD.in > "$$work/PKGBUILD"; \
	(cd "$$work" && makepkg --force --noconfirm --nodeps); \
	cp "$$work"/*.pkg.tar.zst dist/; \
	(cd dist && sha256sum plainshow-cluster-*.pkg.tar.zst > arch-checksums.txt); \
	ls -lh dist/*.pkg.tar.zst

## release: check, build the assets, and print the commands to publish them
release: check dist
	@echo
	@echo "  Assets are in dist/ for version $(VERSION)."
	@echo
	@echo "  Publish them:"
	@echo "    git tag -a v<version> -m 'v<version>' && git push origin v<version>"
	@echo
	@echo "  Pushing the tag runs .github/workflows/release.yml, which rebuilds"
	@echo "  and attaches these assets. Nodes with update.enabled find it within"
	@echo "  update.check_every."

## install: copy the binary into the install root's bin directory
install: build
	@./install.sh node ./$(BINARY)

install-admin: build
	@./install.sh admin ./$(ADMIN_BINARY)

## run: build and start a throwaway node in ./.devnode
run: build
	./$(BINARY) init --root ./.devnode --name dev --cluster dev-cluster 2>/dev/null || true
	./$(BINARY) serve --root ./.devnode

clean:
	rm -rf $(BINARY) $(ADMIN_BINARY) dist .devnode .smokenode .smokepid
