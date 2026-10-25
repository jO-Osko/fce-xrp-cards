# AGENTS.md

Guidance for AI coding agents.
Humans start at [README.md](README.md).

## What this repo is

The FCC extension scaffold, or a fork of it.
Developers fork the scaffold to build a Flare Confidential Compute extension.
Deployment and operations live in the separate [tooling repo](https://github.com/flare-foundation/fce-tooling).

> **Work in progress.**
> v2 is being rebuilt from scratch.
> So far the Go library, the template with its contract and Dockerfile, three examples (`examples/direct`, `examples/external-api`, `examples/wallets`), the shared contract base, the conformance fixture format, the launcher and the docs exist; conformance and the image checks run with `fce`, the tooling repo's CLI, which also deploys and registers extensions on a chain, runs a test machine of one, and sends it instructions and direct actions; setting up wallets comes later.

## Building an extension

Read [docs/rules.md](docs/rules.md) first: every must and must-not of an extension, each with what enforces it.
[docs/README.md](docs/README.md) indexes the other docs.
Start in `template/internal/app`: the op constants, the handlers and their tests.
A fork changes the template: its ops, handlers and state, its tests and fixtures, its README, and the lines its Dockerfile marks `# per extension:`.
After changing a fixture, run `go test ./template/internal/app -update`.
Its contract is in `template/contracts`, a Foundry project: the entrypoint contract, its scripts and its tests.
`examples/` holds small extensions, laid out as the template, most showing more than one concept; read the one for the concept at hand, such as `examples/direct` for direct actions, secrets, a hidden auction users bid in through the contract, instructions arriving again or out of order, and a state verifier, `examples/external-api` for calling outside services, long-running results and a signed result a contract checks, or `examples/wallets` for wallet keys: signing EVM transactions for a signer the wallet's project owner names.

## Shared parts

Unless the task is to change the scaffold itself, leave these untouched:

- `pkg/`: the Go library every extension uses.
  - `pkg/extension` serves the extension's API to tee-node: a handler per op pair, answering at once or through a background job.
  - `pkg/node` calls tee-node's sign port, to sign or decrypt with the TEE key, or with a wallet key the TEE holds.
  - `pkg/payload` decodes the extension's own payloads strictly: ABI only in its canonical encoding, JSON without unknown fields.
- `sol/`: `ExtensionBase.sol`, the base of every extension's entrypoint contract, with its tests and the mock FlareTeeManager extensions test against.
- `conformance/`: the format of each extension's conformance fixtures, which `fce`, the tooling repo's CLI, checks the extension against.
- `internal/`: test helpers: a fake sign port, and the check that keeps an extension's conformance fixtures current.
- `docs/`: the docs every extension shares.
- `launcher/`: the language-agnostic PID 1 of the image; its own stdlib-only module, tests included, so no testify there.
- `fce-version`: the `fce` versions this scaffold works with.
- `REPRODUCIBILITY.md`: what the image's hash depends on, and how to verify a registered one.
- `Makefile`, `.golangci.yml`, `renovate.json5`: the build and lint recipes, and the dependency updates; the `Makefile` pins BuildKit, which the image's hash depends on.
- `AGENTS.md`, `CLAUDE.md`, `GOAI.md`.

Forks get fixes to them by merging upstream, and local edits make those merges conflict.
If a fork needs a change there, propose it upstream, as the scaffold's [CONTRIBUTING.md](https://github.com/flare-foundation/fce-go-scaffold/blob/main/CONTRIBUTING.md) describes.

## Commands

```bash
make lint         # golangci-lint (pinned in the Makefile), every module's go.mod must be tidy, and forge fmt and forge lint for sol/ and every extension's contracts
make test         # go test, every module, with cgo off, then on with the race detector; then forge test for sol/ and every extension's contracts
make tidy         # go mod tidy, every module
make image        # build each extension's image as attested, load it as fce/<dir>, and print its config digest
make repro        # build each image twice without cache; the config digests must match
```

With `fce`, the tooling repo's CLI, at an extension directory:

```bash
fce conformance ./template   # build the extension and check it from the outside: its fixtures, then the rules every extension follows
fce image-check ./template   # build its image with make image, then check it from the outside: Dockerfile, config, hadolint, binaries, a smoke run
fce image ./template         # make image, for that extension
fce repro ./template         # make repro, for that extension
fce deploy -chain coston2 -account <name> ./template   # deploy and register it; keeps template/deployments/coston2.json
fce status -chain coston2 ./template                   # compare that record with the chain
fce run -chain coston2 -account <name> -indexer-db <file> ./template            # a test machine in production, until Ctrl-C
fce send -chain coston2 -account <name> ./template 'myCommand(bytes)' 0x1234    # call the contract and wait for the result
fce direct -chain coston2 ./template MY_EXTENSION MY_DIRECT_COMMAND 0x…          # a direct action to that machine
```

`fce deploy`, `add-version`, `governance`, `disable-version`, `run`, `stop` and `send` send transactions; on a live chain, run them only when the developer asks.

`fce` fails on recommendations too; the i-know-what-im-doing mode (`-i-know-what-im-doing`, or a file `fce-i-know-what-im-doing` in the extension's directory) turns them into warnings.
Never turn that mode on unless the developer asks for it.

Before finishing a task, run `make lint`, `make test`, and `fce conformance` on every extension directory (each directory with a `cmd/extension`); all must pass.
When a change touches what goes into the image (a Dockerfile, `go.mod` / `go.sum`, `pkg/`, `launcher/`, an extension's Go code), also run `make repro`, and `fce image-check` on every extension directory.
A plain `go test ./...` skips the `launcher/` module, runs in one cgo mode only, and runs without the race detector.

## Conventions

- Go: follow [GOAI.md](GOAI.md), and here also:
  - lint with `make lint`, not `golangci-lint run` alone: it covers every module and the contracts;
  - errors and logs never quote a payload, a secret or a key;
  - a function that can block takes a `context.Context` first, and returns once it is cancelled;
  - tests call `t.Parallel()`, unless they must run alone.
- Solidity: Flare's style; `forge fmt` checks the layout, not the names:
  - every parameter and named return value starts with `_`, and so does every internal or private function;
  - immutables and other state variables are mixedCase, constants SCREAMING_SNAKE_CASE;
  - `import { X } from "…";`;
  - `require(condition, Error())`, not `if (…) revert Error()`.
- Markdown: every sentence on its own line.

## Changing the scaffold itself

Also follow the scaffold's [CONTRIBUTING.md](https://github.com/flare-foundation/fce-go-scaffold/blob/main/CONTRIBUTING.md).
