# pinned and built by the module's toolchain; a golangci-lint built with an older Go refuses the go.mod version
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

MODULES := . launcher

# sol/ and every extension's Foundry project; each runs forge soldeer install first, since forge build doesn't
CONTRACTS := sol $(patsubst %/foundry.toml,%,$(wildcard */contracts/foundry.toml examples/*/contracts/foundry.toml))

.PHONY: lint test tidy image repro builder drift

# config verify: run silently ignores unknown keys
lint:
	@$(GOLANGCI_LINT) config verify
	@for m in $(MODULES); do (cd $$m && go mod tidy -diff && $(GOLANGCI_LINT) run ./...) || exit 1; done
	@# forge lint builds without bytecode: in the project's out/ and cache/, forge build would keep that and forge test find no tests
	@d=$$(mktemp -d) && trap 'rm -rf "$$d"' EXIT && \
	for c in $(CONTRACTS); do (cd $$c && forge soldeer install --quiet && forge fmt --check && FOUNDRY_OUT=$$d/$$c/out FOUNDRY_CACHE_PATH=$$d/$$c/cache forge lint --deny warnings) || exit 1; done
	@# extensions compile sol/ with their own dependencies, so they pin sol/'s at sol/'s versions
	@for c in $(filter-out sol,$(CONTRACTS)); do \
		awk 'BEGIN { RS = "" } NR == FNR { want[$$0]; next } { delete want[$$0] } END { for (b in want) { print FILENAME ": not pinned as in sol/soldeer.lock:\n" b; bad = 1 } exit bad }' sol/soldeer.lock $$c/soldeer.lock || exit 1; \
	done

# both cgo modes: the image builds the extension without cgo, go test defaults to cgo where gcc exists; -race needs cgo
test:
	@for m in $(MODULES); do \
		echo "$$m: CGO_ENABLED=0"; (cd $$m && CGO_ENABLED=0 go test ./...) || exit 1; \
		echo "$$m: CGO_ENABLED=1 -race"; (cd $$m && CGO_ENABLED=1 go test -race ./...) || exit 1; \
	done
	@# --force: a forge lint run by hand may have left artifacts without bytecode
	@for c in $(CONTRACTS); do echo "$$c: forge test"; (cd $$c && forge soldeer install --quiet && forge test --force) || exit 1; done

# every directory with cmd/extension, so a fork that deletes examples/ needs no edit here
EXTENSIONS := $(patsubst %/cmd/extension/,%,$(wildcard */cmd/extension/ examples/*/cmd/extension/))

tidy:
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

# BuildKit is an input of the image's hash; the builder's name changes with the pin, so a bump never builds on the old one
BUILDKIT := moby/buildkit:v0.31.0@sha256:a095b3d11ce1a9a05b6064ef515dfca0291ec5bcf2ea8178da8f6461924294e1
BUILDER := fce-buildkit-$(shell echo '$(BUILDKIT)' | sed 's/.*:\(v.*\)@sha256:\(.\{12\}\).*/\1-\2/')

# builds $e/Dockerfile for the TEE's platform, context the repo root, passing its SOURCE_DATE_EPOCH explicitly (older BuildKit
# ignores the default), without BUILDX_GIT_LABELS: its labels carry the commit
BUILD = { test -f $$e/Dockerfile || { echo "$$e: no Dockerfile"; exit 1; }; } && \
	epoch=$$(sed -n 's/^ARG SOURCE_DATE_EPOCH=//p' $$e/Dockerfile) && \
	{ test -n "$$epoch" || { echo "$$e/Dockerfile: no ARG SOURCE_DATE_EPOCH=<value>"; exit 1; }; } && \
	env -u BUILDX_GIT_LABELS docker buildx build --builder $(BUILDER) --platform linux/amd64 --build-arg SOURCE_DATE_EPOCH=$$epoch -f $$e/Dockerfile .

# the attested hash, from a docker-format image tar on stdin: its config digest, as either image store writes it
CONFIG_DIGEST = { tar -xOf - manifest.json | sed -n 's/.*"Config":"\(blobs\/sha256\/\)\{0,1\}\([0-9a-f]\{64\}\).*/sha256:\2/p'; }

# loads each extension's image as fce/<dir> and prints its config digest
image: builder
	@test -n "$(EXTENSIONS)" || { echo "no extension found: no */cmd/extension"; exit 1; }
	@for e in $(EXTENSIONS); do \
		$(BUILD) --output type=docker,name=fce/$$e,rewrite-timestamp=true || exit 1; \
		c=$$(docker save fce/$$e | $(CONFIG_DIGEST)) && test -n "$$c" || { echo "$$e: no config digest in fce/$$e"; exit 1; }; \
		echo "$$e: fce/$$e $$c"; \
	done

# two builds without cache must give the same config digest; a failing build prints its log
repro: builder
	@test -n "$(EXTENSIONS)" || { echo "no extension found: no */cmd/extension"; exit 1; }
	@d=$$(mktemp -d) && trap 'rm -rf "$$d"' EXIT && \
	for e in $(EXTENSIONS); do \
		for i in 1 2; do \
			echo "$$e: build $$i"; \
			$(BUILD) --no-cache --progress=plain --output type=docker,dest=$$d/$$i.tar,rewrite-timestamp=true >$$d/log 2>&1 || { cat $$d/log; exit 1; }; \
		done; \
		a=$$($(CONFIG_DIGEST) < $$d/1.tar) && b=$$($(CONFIG_DIGEST) < $$d/2.tar) && \
		echo "$$e: $$a $$b" && test -n "$$a" && test "$$a" = "$$b" || { echo "$$e: not reproducible"; exit 1; }; \
	done

# the pinned builder, created on first use
builder:
	@docker buildx inspect $(BUILDER) >/dev/null 2>&1 || \
		docker buildx create --name $(BUILDER) --driver docker-container --driver-opt image=$(BUILDKIT) --bootstrap >/dev/null

# each examples/*/Dockerfile equals template/Dockerfile but for the lines under "# per extension:", up to the next blank line
PER_EXTENSION = awk '/^\# per extension:/ { print; skip = 1; next } /^$$/ { skip = 0 } !skip'
drift:
	@d=$$(mktemp -d) && trap 'rm -rf "$$d"' EXIT && $(PER_EXTENSION) template/Dockerfile > $$d/template && \
	for f in $(wildcard examples/*/Dockerfile); do \
		$(PER_EXTENSION) $$f > $$d/example && \
		diff -u --label template/Dockerfile --label $$f $$d/template $$d/example || \
		{ echo "$$f: differs from template/Dockerfile outside its '# per extension:' blocks"; exit 1; }; \
	done
