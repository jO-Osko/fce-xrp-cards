# Contributing

Thank you for considering improving our source code.
All contributions are welcome.

## Issues

_Sensitive security-related issues should be reported as described in [SECURITY.md](SECURITY.md), not in an issue._

To share ideas, considerations, or concerns, open an issue.
Before filing an issue, make sure it has not been raised already.
In the issue, answer the following questions:

- What is the issue?
- Why is it an issue?
- How do you propose to change it?

The fixture format, the fake sign port and `launcher/` are copies of the [extension scaffold](https://github.com/flare-foundation/fce-go-scaffold)'s, and the image checks mirror its image.
Propose a change to them there first.

### Pull request

Before opening a pull request, open an issue on why the request is needed.

To contribute: fork the repo, make your improvements, commit and open a pull request.
The maintainers will review the request.

The request must:

- Reference the relevant issue,
- Follow [GOAI.md](GOAI.md),
- Put every sentence on its own line in Markdown files,
- Be well documented,
- Be well tested,
- Compile,
- Pass all the tests,
- Pass all the linters,
- Be opened against the `main` branch.

## Setting the environment

Use the Go version specified in [go.mod](go.mod) or newer, and have gcc installed.
The tests use the scaffold's library as their reference extension, and `go.mod` points at a checkout of the [scaffold](https://github.com/flare-foundation/fce-go-scaffold) beside this repo, `../scaffold`.

The repo has two Go modules, the root and `launcher/`.
Run the `make` targets from the root: they cover both, while a plain `go test ./...` skips `launcher/`.

`make test` runs every test twice, with `CGO_ENABLED=0` and with `CGO_ENABLED=1`.
The second run uses the race detector, which needs cgo.
go-ethereum has cgo-only code that behaves differently without it.

A change to `cmd/fce`, `conformance/` or `imagecheck/` also needs `make install`, then `fce conformance` and `fce image-check` on the scaffold's template.
A change to the on-chain commands, `internal/chain`, `internal/forge` or `internal/record` also needs `make test-fork`, which takes Foundry (anvil and forge), Docker and the network.
`make test-live` sends real transactions on Coston2; the [README](README.md#development) lists what it needs.

Tidy the dependencies:

```bash
make tidy
```

Run all tests:

```bash
make test
```

Run the linters:

```bash
make lint
```

The Makefile pins golangci-lint and runs it with `go run`, so there is nothing to install.
`make lint` also fails if a module's `go.mod` or `go.sum` needs `make tidy`.
