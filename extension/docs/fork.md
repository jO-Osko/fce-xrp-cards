# Forking

An extension starts as a fork of the scaffold.
The fork changes its own parts, and gets fixes to the shared ones by merging upstream.

## Start

You need Go 1.26.9 or higher, gcc, Foundry 1.7.1 or higher, Docker with buildx for the image, and `fce`, the CLI of the [tooling repo](https://github.com/flare-foundation/fce-tooling), at a version `fce-version` names.

```bash
git clone <your fork> && cd <your fork>
git remote add upstream https://github.com/flare-foundation/fce-go-scaffold.git
make lint test
fce conformance ./template
```

Then turn the template into your extension, as the [extension guide](extension-guide.md) and the [contracts guide](contracts.md) describe.

## What is yours

| Path | Whose |
|---|---|
| `template/` | yours: the ops, handlers and state, their tests and fixtures, the contract and its scripts, the README, the Dockerfile's `# per extension:` lines, and the deployment records `fce` keeps in `deployments/` |
| `examples/` | yours: keep them as references, or delete them; nothing else needs them |
| `go.mod`, `go.sum` | yours to add dependencies to; every extension in the fork shares them, and they reach every image |
| `README.md`, `CHANGELOG.md`, `CODEOWNERS`, `SECURITY.md`, `CONTRIBUTING.md`, `LICENSE` | yours to rewrite |
| `pkg/`, `sol/`, `conformance/`, `internal/`, `launcher/`, `docs/`, `fce-version`, `REPRODUCIBILITY.md`, `Makefile`, `.golangci.yml`, `renovate.json5`, `AGENTS.md`, `CLAUDE.md`, `GOAI.md` | shared: leave them untouched |

A local edit to a shared part makes later merges conflict there.
If your extension needs a change in one, propose it upstream, as the scaffold's [CONTRIBUTING.md](https://github.com/flare-foundation/fce-go-scaffold/blob/main/CONTRIBUTING.md) describes.

`make` finds every directory with a `cmd/extension`, so deleting `examples/` needs no other edit.
For a second extension, copy `template/` and rename the copy as [the directory](#the-directory) below shows.

## Renames

Nothing needs renaming: imports of `pkg/` resolve inside the module, so a fork builds as it is.
Each rename below is optional, and changes the image's hash.

### The module path

For a fork other modules import, such as `github.com/acme/my-extension`:

```bash
old=github.com/flare-foundation/fce-go-scaffold
new=github.com/acme/my-extension
go mod edit -module "$new"
grep -rl --include='*.go' "$old" . | xargs perl -pi -e "s#\Q$old\E#$new#g"
make lint test
```

- It edits imports in the shared parts too: run the replacement again after each merge from upstream, which brings the old path back, and expect conflicts on import lines.
- `launcher/` is a module of its own, which nothing imports; it keeps its path.
- `fcc-extension-scaffold/` in each `foundry.toml` is the Solidity import name of `sol/`, not the module path; it stays.

### The contract

The template's entrypoint contract is `InstructionSender`; to name it after the extension, such as `MyExtension`:

```bash
cd template/contracts
git mv src/InstructionSender.sol src/MyExtension.sol
git mv test/InstructionSender.t.sol test/MyExtension.t.sol
grep -rl InstructionSender src script test | xargs perl -pi -e 's/InstructionSender/MyExtension/g'
forge test
```

The deployment output's `instructionSender` key and `PostRegister.s.sol`'s `INSTRUCTION_SENDER` keep their names: they name the contract's role in the registry, which the deployment tooling reads.

### The directory

To move `template/` to a directory named after the extension, such as `my-extension/`:

```bash
git mv template my-extension
grep -rl --include='*.go' '/template/' . | xargs perl -pi -e 's#/template/#/my-extension/#g'
perl -pi -e 's#template/#my-extension/#g' my-extension/Dockerfile
make lint test
fce conformance ./my-extension
```

- The Dockerfile names the directory only in its `# per extension:` lines, which the second replacement changes.
- Upstream's changes to `template/`, its Dockerfile above all, then merge only as far as git follows the rename.
  After each merge, compare your Dockerfile with upstream's outside the `# per extension:` lines: `git diff upstream/main:template/Dockerfile HEAD:my-extension/Dockerfile`.

## Merging upstream

```bash
git fetch upstream
git merge upstream/main
make lint test
fce conformance ./template
```

- A conflict in a shared part means a local edit there: take upstream's side, and propose the edit upstream.
- If the merge changes `fce-version`, install an `fce` it names.
- A merge that reaches the image changes its hash: run `fce repro` and `fce image-check` again, and register the new image as a new TEE version.
  [REPRODUCIBILITY.md](../REPRODUCIBILITY.md#what-the-hash-depends-on) lists what reaches it; a tee-node bump in `go.mod` always does.
  Then disable the old one, `fce disable-version -hash <old code hash>`: until then its machines stay in production, and operators can start new ones; disabling pauses them, for good.
