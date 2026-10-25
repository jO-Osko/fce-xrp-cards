# AGENTS.md

Guidance for AI coding agents.
Humans start at [README.md](README.md).

## What this repo is

The tooling for Flare Confidential Compute extensions: the `fce` CLI.
Extensions are built from the extension scaffold, a separate repo.

## Layout

- `cmd/fce`: the CLI.
- `conformance/`: the runner and the checks every extension must pass; its fixture format is a copy of the scaffold's and must read the same JSON.
- `imagecheck/`: checks an extension's built image from the outside: its Dockerfile and attested config, hadolint, the binaries it runs, and a smoke run of the image as shipped.
  Its probe, `imagecheck/cmd/imageprobe`, runs inside the image's containers; `fce` builds it from the embedded source, so it uses only the standard library.
  Some checks mirror the scaffold (the launcher's flags, tee-node's variables, port 5500); when the scaffold changes them, the scaffold raises its `fce-version`.
- `internal/fakenode`: the fake sign port, a copy of the scaffold's with the same fixed keys, the TEE's and one wallet's, so fixtures that sign match.
- `internal/fork`: finds an extension directory's fork, checks its `fce-version`, and reads its `fce-i-know-what-im-doing` file.
- `internal/chain`: connects to a Flare network: its RPC, Flare's contract registry and the FlareTeeManager it names, and transactions signed with a key from a Foundry keystore.
- `internal/forge`: runs an extension's forge scripts against a chain.
- `internal/record`: an extension's deployment record, what fce deployed and registered on one chain.
- `internal/machine`: tee-proxy's external API as fce uses it: a machine's info, results and their signatures, direct actions, and what registering a machine and proving it available take.
- `internal/stack`: a test machine's stack: its run directory, tee-proxy's config, the Compose file, and the run's state.
- `launcher/`: a copy of the scaffold's launcher, its own module `github.com/flare-foundation/fce-launcher` as the scaffold's is, standard library only; a test fails when the two differ.
  Images build the scaffold's copy.

Every check is required (`Err`: the extension can't work or be registered without it) or a recommendation (`Advice`); a recommendation fails unless the i-know-what-im-doing mode is on.
Conformance follows each rule's kind in the scaffold's `docs/extension-contract.md`.

## Commands

```bash
make lint         # golangci-lint (pinned in the Makefile), and every module's go.mod must be tidy
make test         # go test, every module, with cgo off, then on with the race detector
make install      # go install ./cmd/fce
make test-fork    # the on-chain commands on an anvil fork of Coston2; needs anvil, forge, Docker and the network
make test-live    # run, send, direct and stop on Coston2; sends real transactions
```

Before finishing a task, run `make lint` and `make test`; both must pass.
When a change touches `cmd/fce`, `conformance/` or `imagecheck/`, also install it (`make install`) and run `fce conformance` and `fce image-check` on the scaffold's template.
When it touches the on-chain commands, `internal/chain`, `internal/forge` or `internal/record`, also run `make test-fork`.
`make test-live` covers `run`, `stop`, `send` and `direct` on Coston2 with real funds; run it, and any command that sends a transaction to a live chain, only when the developer asks.

## Conventions

- Go: follow [GOAI.md](GOAI.md).
- Markdown: every sentence on its own line.
