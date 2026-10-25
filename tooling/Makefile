# pinned and built by the module's toolchain; a golangci-lint built with an older Go refuses the go.mod version
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

# launcher/ mirrors the scaffold's launcher, its own module
MODULES := . launcher

.PHONY: lint test test-fork test-live tidy install

# config verify: run silently ignores unknown keys
lint:
	@$(GOLANGCI_LINT) config verify
	@for m in $(MODULES); do (cd $$m && go mod tidy -diff && $(GOLANGCI_LINT) run ./...) || exit 1; done

# both cgo modes: go-ethereum's cgo-only code behaves differently without it; -race needs cgo
test:
	@for m in $(MODULES); do \
		echo "$$m: CGO_ENABLED=0"; (cd $$m && CGO_ENABLED=0 go test ./...) || exit 1; \
		echo "$$m: CGO_ENABLED=1 -race"; (cd $$m && CGO_ENABLED=1 go test -race ./...) || exit 1; \
	done

# deploy, add-version, governance and status on an anvil fork of Coston2, against ../scaffold's template;
# needs anvil, forge, Docker and the network. FCE_FORK_URL and FCE_SCAFFOLD override the defaults.
test-fork:
	@FCE_FORK_URL=$${FCE_FORK_URL:-https://coston2-api.flare.network/ext/C/rpc} go test -count=1 -timeout 30m -run '^TestFork$$' ./cmd/fce

# the template end to end on Coston2: deploy, run behind a public quick tunnel, send, direct, stop.
# Sends real transactions: needs a funded keystore (FCE_LIVE_KEYSTORE, FCE_LIVE_PASSWORD_FILE), an indexer
# database's settings (FCE_LIVE_INDEXER_DB), Docker, forge and the network.
test-live:
	@test -n "$$FCE_LIVE_KEYSTORE" -a -n "$$FCE_LIVE_PASSWORD_FILE" -a -n "$$FCE_LIVE_INDEXER_DB" || \
		{ echo "set FCE_LIVE_KEYSTORE, FCE_LIVE_PASSWORD_FILE and FCE_LIVE_INDEXER_DB" >&2; exit 2; }
	@go test -count=1 -timeout 60m -run '^TestLive$$' ./cmd/fce

tidy:
	@for m in $(MODULES); do (cd $$m && go mod tidy) || exit 1; done

# installs fce into GOBIN (or GOPATH/bin)
install:
	@go install ./cmd/fce
