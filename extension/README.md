<div align="center">
  <a href="https://flare.network/" target="blank">
    <img src="https://content.flare.network/Flare-2.svg" width="300" alt="Flare Logo" />
  </a>
  <br />
  <a href="CONTRIBUTING.md">Contributing</a>
  ·
  <a href="SECURITY.md">Security</a>
  ·
  <a href="CHANGELOG.md">Changelog</a>
</div>

# FCC extension scaffold

The starting point for building a Flare Confidential Compute (FCC) extension: an application that runs in a TEE beside tee-node, reached through its own contract on chain, or through direct actions.
Deployment and operations live in the separate [tooling repo](https://github.com/flare-foundation/fce-tooling).

> **Work in progress.**
> v2 is being rebuilt from scratch.
> The library, the template, three examples, the shared contract base, the reproducible image and the docs are in place, and the tooling repo deploys and registers extensions on a chain, runs test machines of them and sends them instructions and direct actions; setting up wallets with it comes later.
> The previous version (v1) is on `main`.

## Internal use before release

The scaffold and the tooling are tested internally first, from Flare's internal GitLab, before they are published on GitHub.
Until then, the links to `github.com/flare-foundation/fce-go-scaffold` and `github.com/flare-foundation/fce-tooling` in these docs don't resolve; use the internal GitLab repos in their place.

1. Clone both repos from the internal GitLab, side by side, as `scaffold` and `tooling`; the tooling's tests use the scaffold at `../scaffold`:

   ```bash
   git clone <the scaffold's GitLab repo> scaffold
   git clone <the tooling's GitLab repo> tooling
   ```

2. Install `fce` from the tooling clone with `make -C tooling install`; `go install github.com/flare-foundation/fce-tooling/cmd/fce@<version>` works only once it is published.
   Built at a tag such as `v0.1.0` with no local changes, `fce` is that release, and matches this scaffold's `fce-version`.
   Built anywhere else, it is a development build: it warns, and the checks still run.
3. Fork the scaffold as [docs/fork.md](docs/fork.md) describes, with the scaffold's GitLab repo as `upstream` in place of the GitHub URL.
4. Report issues and open merge requests on the internal GitLab, not on GitHub.

## What it gives you

- The [rules](docs/rules.md) an extension follows, each with what enforces it, and the [assumptions](docs/assumptions.md) behind them: who can do what.
- A template to turn into your extension, and examples, most showing more than one concept.
- A Go library for what every extension needs: serving tee-node, calling its sign port, decoding payloads strictly.
- A base for the extension's entrypoint contract: sending instructions, fees, checking a TEE's signature.
- A reproducible image: tee-node and the extension under a launcher, the same hash from the same code.
- The [extension contract](docs/extension-contract.md): the wire rules in any language, which `fce conformance` checks.

## Start

Fork the repository, then work in `template/`: [forking](docs/fork.md) says what a fork changes and what it leaves, and the [extension guide](docs/extension-guide.md) guides you from the template to your own extension.
The [glossary](docs/glossary.md) defines the terms the docs use.

## Layout

- `pkg/`: the Go library every extension uses.
  - `pkg/extension` serves the extension's API to tee-node: a handler per op pair, answering at once or through a background job.
  - `pkg/node` calls tee-node's sign port, to sign or decrypt with the TEE key, or with a wallet key the TEE holds.
  - `pkg/payload` decodes the extension's own payloads strictly: ABI only in its canonical encoding, JSON without unknown fields.
- `template/`: the minimal extension a fork turns into its own; see its [README](template/README.md).
- `examples/`: small extensions, laid out as the template, most showing more than one concept: [`direct`](examples/direct/README.md) provisions secrets through direct actions: authorized, safe to replay, encrypted to the TEE; runs hidden auctions, with bids encrypted to the TEE through its contract and the winner signed for it; and reports its admin as state, which its contract checks; [`external-api`](examples/external-api/README.md) fetches a price from three APIs over TLS, past tee-node's 2 s, with partial results, and signs it for its contract to check; [`wallets`](examples/wallets/README.md) signs EVM transactions with a wallet key tee-node keeps in the TEE, for a signer the wallet's project owner names, numbered by its contract.
- `sol/`: the shared Solidity base every extension's entrypoint contract inherits, with its tests and the mock FlareTeeManager extensions test against.
- `docs/`: [the docs](docs/README.md): architecture, assumptions, rules, the Go and contract guides, direct actions, testing, forking, the extension contract, and a [glossary](docs/glossary.md).
- `conformance/`: the format of each extension's conformance fixtures, which `fce`, the tooling repo's CLI, checks the extension against.
- `internal/`: test helpers: a fake sign port, and the check that keeps an extension's conformance fixtures current.
- `launcher/`: the language-agnostic PID 1 of the image; its own stdlib-only module.
- `fce-version`: the versions of `fce`, the tooling repo's CLI, this scaffold works with.
- `REPRODUCIBILITY.md`: what the image's hash depends on, and how to verify a registered one.

Forks leave the shared parts untouched: everything above but `template/` and `examples/`, and the `Makefile`, `.golangci.yml`, `renovate.json5` and the agent files.

## Requirements

- Go 1.26.9 or higher
- gcc, for the tests that run with cgo
- Foundry 1.7.1 or higher
- Docker with buildx, for the image: `make image`, `make repro` and `fce image-check`
- `fce`, the CLI of the [tooling repo](https://github.com/flare-foundation/fce-tooling), at a version `fce-version` names (`v0.1` is any v0.1.x), for conformance and the image checks

## Development

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
```

`fce` fails on recommendations too; the i-know-what-im-doing mode (`-i-know-what-im-doing`, or a file `fce-i-know-what-im-doing` in the extension's directory) turns them into warnings.

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.
