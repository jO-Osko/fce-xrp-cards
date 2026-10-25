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

If your fork needs a change in a shared part (`pkg/`, `sol/`, `launcher/`, `conformance/`, `internal/`, `docs/`, `fce-version`, `REPRODUCIBILITY.md`, the `Makefile`, `.golangci.yml`, `renovate.json5`, or the agent files), propose it here instead of editing it in the fork.
Forks get library fixes by merging upstream, and local edits there can make those merges conflict.

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

Use the Go version specified in [go.mod](go.mod) or newer, and have gcc and Foundry 1.7.1 or newer installed.
`make lint` and `make test` also run forge for `sol/` and every extension's contracts.
The image targets, `make image` and `make repro`, need Docker with buildx; they create a pinned BuildKit builder on first use.
Conformance and the image checks run with `fce`, the CLI of the [tooling repo](https://github.com/flare-foundation/fce-tooling), at a version `fce-version` names; install it with `make install` in a clone of that repo.
`fce-version` holds a version prefix such as `v0.1`, and `fce` refuses a scaffold that names another.
Raise it when a change needs a newer `fce`, for example a launcher flag the image checks must read.
The scaffold's own extensions follow every recommendation: none has an `fce-i-know-what-im-doing` file, and the checks run without `-i-know-what-im-doing`.
The scaffold commits no deployment records, `<extension>/deployments/*.json`: every fork would inherit them, and `fce` would take the scaffold's deployment for the fork's.
Each example's `Dockerfile` equals `template/Dockerfile` outside its `# per extension:` blocks, and `make drift` checks it: a change to the template's goes into every example's.

The repo has two Go modules, the root and `launcher/`.
Run the `make` targets from the root: they cover both, while a plain `go test ./...` skips `launcher/`.

`make test` runs every test twice, with `CGO_ENABLED=0` and with `CGO_ENABLED=1`.
The second run uses the race detector, which needs cgo.
The image builds the extension and the launcher without cgo, while `go test` uses cgo wherever gcc is installed, and some dependencies behave differently between the two.

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
