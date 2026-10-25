# Testing

Every layer below but end to end runs on a fresh clone, with no chain; the image layers need Docker.
End to end needs a chain, and runs by hand: nothing runs it automatically yet.

| Layer | Checks | Run |
|---|---|---|
| handlers | each op, called directly with a `DataFixed` or a `DirectInstruction` | `go test ./template/...` |
| fixtures | the committed conformance fixtures match the ones the tests build | `go test ./template/internal/app`; `-update` rewrites them |
| contract | the entrypoint contract against a mock FlareTeeManager, and each instruction fixture's payload sent through it | `forge test` in `template/contracts` |
| shared parts | the library, the launcher, the fixture format and the contract base | `make test` |
| lint | golangci-lint, tidy `go.mod` files, `forge fmt` and `forge lint` | `make lint` |
| conformance | the extension's binary from the outside, as tee-node calls it: its fixtures, then the rules every extension follows | `fce conformance ./template` |
| image | the image as attested: its Dockerfile, its config, hadolint, its binaries and a smoke run | `fce image-check ./template` |
| reproducibility | two builds without cache give the same hash | `fce repro ./template`, or `make repro` for every extension |
| end to end | deploy, register, a test machine and a round trip, on a chain | by hand with `fce`: `deploy`, `run`, then `send` or `direct`; see [end to end](#end-to-end) |

`make test` runs the Go and forge tests of the whole repository, and `make lint` its linters.
`fce` is the CLI of the [tooling repo](https://github.com/flare-foundation/fce-tooling), at a version `fce-version` names; its README says how to install it.

## Before a change lands

1. `make lint` and `make test`.
2. `fce conformance` on every extension directory, each directory with a `cmd/extension`.
3. When the change reaches the image, `make repro`, and `fce image-check` on every extension directory.
   [REPRODUCIBILITY.md](../REPRODUCIBILITY.md#what-the-hash-depends-on) lists what reaches it; docs, contracts and tests never do.

## Go tests

- `make test` runs every test twice: with cgo off, as the image builds the extension, and with cgo on and the race detector.
  go-ethereum's secp256k1 code differs between the two, so a test that passes in one mode only can still fail in the image.
- A plain `go test ./...` runs one mode, without the race detector, and skips `launcher/`, a module of its own.
- Test a handler by calling it directly; the envelope, the deadline and the result's fields are the library's.
- `internal/fakenode` is a fake sign port for tests: it signs and decrypts with a fixed test key, records the results a long-running job posts, and holds one wallet key.
  `fce conformance` starts one with the same keys.
- Stub outside services in the unit tests: the [external-api example](../examples/external-api/README.md#testing) stubs its sources over TLS.

## Fixtures

Each extension's `conformance/` holds its golden cases, one JSON file each: an op's payload, and the result and posts it must give.
[The extension contract](extension-contract.md#fixtures) describes the format.

- `internal/app/fixtures_test.go` builds them from Go values, and `golden.Check` compares them with the committed files: a missing, extra or out-of-date file fails, and `-update` rewrites them.
  Either way it fails on a set the runner would refuse, such as one op pair on both channels.
- `fce conformance` sends them in name order to one process, so a fixture may rely on the ones before it; number them to keep that order visible.
- `again` pins what every later send of the same message answers: the runner sends it again, so replays and instructions arriving again are tested.
- Encryption is randomized: encrypt a fixture's ciphertext once, and keep it as a constant.
- Conformance runs the real binary: a fixture can't depend on the network.

## Contract tests

- They run against `MockFlareTeeManager`, from `sol/`: it charges a fee per TEE, refuses what the platform refuses, and knows each TEE's extension and status; [contracts.md](contracts.md#6-testing) lists what it doesn't model.
- Each instruction command's forge test reads its conformance fixture and sends its payload through the contract, so the contract and the handler can't change the layout alone.
- A signature the extension makes in Go and the contract checks gets a shared vector too: both sides pin the same digest.

## fce

- `fce conformance <dir>` builds the extension as the image does, starts it against a fake sign port, and runs its fixtures and the checks every extension must pass; [the extension contract](extension-contract.md#conformance) lists them.
  After `--` it runs any command instead, so an extension in another language is checked the same way.
- `fce image-check <dir>` builds the image with `make image`, then checks the Dockerfile, the attested config, hadolint, the binaries the image runs, and a smoke run of the image as shipped.
- `fce repro <dir>` builds the image twice without cache, and fails when the hashes differ.
- A check is required or a recommendation.
  A required one always fails; a recommendation fails too, unless the developer turns on the i-know-what-im-doing mode: the flag `-i-know-what-im-doing`, or a file `fce-i-know-what-im-doing` in the extension's directory, with the reason as its text.
  Then it warns, and `fce` prints the reason.
  [rules.md](rules.md) says which rule each check covers.
- `fce conformance` passes its own environment to the extension, and takes `-env KEY=VALUE`.

## End to end

`fce deploy`, `fce add-version` and `fce governance` deploy and register an extension on a chain, and `fce status` compares the result with the deployment record they keep; the tooling repo's README describes them.
Try them on an anvil fork first, `anvil --fork-url <the network's RPC>` and `-rpc http://127.0.0.1:8545`, signing with one of the funded accounts anvil prints, imported with `cast wallet import`: it costs nothing, and nothing reaches the chain.
Then `fce run` starts a test machine of the extension, tee-node in test mode with the extension's image, and takes it to production; `fce send` calls the extension's contract and waits for each instruction's result, and `fce direct` posts a direct action to the machine; both check the result's signatures.
A test machine has no TEE: its operator holds its key, and while its version is allowed, contracts accept its signatures, so run it on an extension real users don't use ([test machines](assumptions.md#test-machines)).
Ctrl-C, or `fce stop` after `fce run -detach`, pauses the machine on chain and removes it.
`fce run` needs an indexer's database, a public URL, and the chain itself, not a fork, whose data providers deliver the machine's challenge and its instructions; the tooling repo's README describes what it takes.
