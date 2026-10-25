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

# FCC extension tooling

`fce`, the CLI for Flare Confidential Compute extensions built from the [extension scaffold](https://github.com/flare-foundation/fce-go-scaffold).

> **Work in progress.**
> So far it checks and builds extensions: conformance, the image, its reproducibility and the image checks; it encrypts secrets to a TEE; it deploys and registers extensions on a chain; and it runs a test machine of an extension and sends it instructions and direct actions.
> Setting up wallets comes later.

## Internal use before release

The tooling and the scaffold are tested internally first, from Flare's internal GitLab, before they are published on GitHub.
Until then, the links to `github.com/flare-foundation/fce-go-scaffold` and `github.com/flare-foundation/fce-tooling` in these docs don't resolve; use the internal GitLab repos in their place.

1. Clone both repos from the internal GitLab, side by side, as `scaffold` and `tooling`; `go.mod` points the tests at `../scaffold`, and `make test-fork` registers its template:

   ```bash
   git clone <the scaffold's GitLab repo> scaffold
   git clone <the tooling's GitLab repo> tooling
   ```

2. Install `fce` with `make install` in this repo, as [Install](#install) shows; `go install github.com/flare-foundation/fce-tooling/cmd/fce@<version>` works only once it is published.
   Built at a tag such as `v0.1.0` with no local changes, `fce` is that release.
   Built anywhere else, it is a development build: a fork's `fce-version` accepts it with a warning.
3. Run it on a fork of the scaffold, as the scaffold's README describes.
4. Report issues and open merge requests on the internal GitLab, not on GitHub.

## Install

```bash
make install      # go install ./cmd/fce
```

## Commands

```bash
fce conformance [-i-know-what-im-doing] [flags] dir...
fce conformance [-i-know-what-im-doing] [flags] [-fixtures dir] -- command [args...]
fce image dir...
fce repro dir...
fce image-check [-i-know-what-im-doing] dir...
fce encrypt -pubkey key [-tee address] < plaintext
fce deploy (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] dir
fce add-version (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] (-platform names | -test) [-i-know-what-im-doing] dir
fce governance (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] -signers addresses -threshold n dir
fce disable-version (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] (-hash codehash | -test) [-platform names] dir
fce status (-chain name | -rpc url) dir
fce run (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] -indexer-db file [-url url] [-port n] [-detach] [-i-know-what-im-doing] dir
fce stop (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] dir
fce send (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] [-value wei] [-timeout duration] dir (signature [args...] | calldata)
fce direct (-chain name | -rpc url) [-timeout duration] dir opType opCommand [message]
fce version
```

Each `dir` is an extension directory in a scaffold fork, such as `./template`.
The fork's `fce-version` file names the `fce` versions it works with, as a version prefix such as `v0.1`; `fce` refuses a fork that names another, and a development build only warns.

- `fce conformance` builds the extension as its image does, or runs the command after `--`, starts it against a fake sign port, sends it each fixture as tee-node would, runs the checks every extension must pass, and stops it with SIGTERM.
  The fixture format and the rules are in the scaffold's `docs/extension-contract.md`.
- `fce image` and `fce repro` run the fork's `make image` and `make repro` for the extension: the build recipe is the fork's, so anyone can rebuild a registered hash from the fork alone.
- `fce image-check` builds the image with `make image`, then checks it from the outside: its Dockerfile and attested config, hadolint, the binaries it runs, and a smoke run of the image as shipped.
  It needs Docker, and Go to build the probe it runs inside the image's containers.
- `fce encrypt` encrypts stdin to a TEE's public key, as tee-node's sign port decrypts it (ECIES on secp256k1), and prints the ciphertext as hex.
  The key is `teeInfo.publicKey` from the TEE's proxy, `curl -s <proxy>/info | jq -c .teeInfo.publicKey`, or its x and y as 64 bytes of hex; with `-tee`, it refuses a key that isn't that TEE's.
  The plaintext comes from stdin, so it stays out of shell history.
- `fce deploy` deploys the extension's contracts with its `contracts/script/Deploy.s.sol`, registers them with FlareTeeManager, allows the deployer to own the extension's machines, and runs its `contracts/script/PostRegister.s.sol`.
  Run again, it finishes what is left, and changes nothing once all is done.
- `fce add-version` builds the image with `make image` and allows its config digest, under the image's version label, on the platforms `-platform` names, such as `GCP_AMD_SEV`.
  The fork must have no uncommitted changes, so others can rebuild the image; that is a recommendation.
  `-test` allows tee-node's fixed test code hash on `TEST_PLATFORM` instead, for a machine in test mode; that hash says nothing about the code.
  Its machines have no TEE and their operators hold their keys, yet contracts accept their signatures: use it on an extension real users don't use, or disable it, `fce disable-version -test`, before they arrive; `fce add-version -test` and `fce status` warn.
- `fce governance` sets the signers that govern the extension's machines, and how many of them an action needs; a machine's `GOVERNANCE_*` settings must match them.
- `fce disable-version` disables a code hash, `-hash` or `-test`, on all its platforms or those `-platform` names, and pauses its machines in production there.
  It is for good: the registry can't enable a code hash again, nor allow it as a new version.
- `fce status` compares the extension's deployment record with the chain.
- `fce run` starts a test machine of the extension, takes it to production, and shows its logs; Ctrl-C pauses it on chain and removes it.
  With `-detach`, it returns once the machine is in production, and `fce stop` pauses and removes it.
  [Running a machine](#running-a-machine) describes both.
- `fce send` calls the extension's contract, at the address in its deployment record, with a function's signature and its arguments, such as `'myCommand(bytes)' 0x1234`, or with calldata, such as `cast calldata` prints for tuples and arrays.
  It finds the fee by simulating the call, raising the value until `ExtensionBase`'s `InsufficientFee` no longer reverts it; `-value` sets it instead.
  Then it waits for the result of each instruction the call sent, at each machine it went to, checks the TEE's and the proxy's signatures, and prints it; a call that sends none, such as submitting a result, ends once mined.
- `fce direct` posts a direct action to the machine `fce run` started, the op type and command as names, such as `MY_EXTENSION MY_DIRECT_COMMAND`, and the message as hex; then it waits for the result as `fce send` does.

## Deploying

`-chain` names a Flare network, `flare`, `songbird`, `coston` or `coston2`, and uses its public RPC; `-rpc` takes your own.
fce finds FlareTeeManager in Flare's contract registry.

The commands that send transactions sign with a key from a Foundry keystore.
Import it once with `cast wallet import <name> --interactive`, then pass `-account <name>`, or `-keystore <file>`.
fce asks for the password, or reads it from `-password-file`, and hands forge the same keystore; the key itself never goes in an argument or the environment.
The deployer owns the contracts and the extension.

fce keeps what it did in the extension's deployment record, `<dir>/deployments/<chain>.json`: the chain, FlareTeeManager, the contracts, the extension id, the versions it allowed, each with the commit its image was built from, and the governance.
Commit it: it publishes the extension's addresses with its code, and the next commands read it.
On anvil the record is `anvil-<chain>.json`, which the scaffold's `.gitignore` leaves out.

`fce deploy` runs `forge soldeer install`, then the extension's scripts in `<dir>/contracts`, with these variables:

| Script | Reads | Writes |
|---|---|---|
| `Deploy.s.sol` | `FLARE_TEE_MANAGER` | JSON to `DEPLOY_OUTPUT`, a path under `deployments/`: `instructionSender`, and `stateVerifier` if the extension has one |
| `PostRegister.s.sol`, optional | `EXTENSION_ID`, `INSTRUCTION_SENDER`, and `STATE_VERIFIER` if there is one | nothing fce reads; it runs on every `fce deploy`, so running it again must change nothing |

## Running a machine

`fce run` is for testing and trying out an extension, not for running it in production.
Its machine runs tee-node in test mode, so the extension needs a test version, `fce add-version -test`, and a governance, `fce governance`; and the account must be an allowed machine owner, as the deployer is.
It runs on the chain itself, not a fork: the chain's data providers deliver the machine's challenge and its instructions, and a fork has none.

It needs:

- Docker with Compose.
- An indexer's database.
  tee-proxy reads the chain's signing policies and voters from a flare-system-c-chain-indexer database, and fce runs no indexer: a new one would take too long to catch up.
  `-indexer-db` names a TOML file with the database's `host`, `port`, `database`, `username` and `password`; keep it private.
- A public URL, where the data providers and the availability check reach the machine.
  fce opens a cloudflared quick tunnel by default; `-url` takes your own, which must forward to tee-proxy's external API, on the `127.0.0.1` port `-port` sets.

`fce run` checks the chain first, builds the image with `make image`, and starts tee-node with it, tee-proxy and Redis.
Only tee-proxy's external API leaves the stack: on `127.0.0.1`, and through the tunnel if fce opened one.
tee-proxy signs with a key fce makes for the run, and takes direct actions with an API key fce makes too.
Both keys and the indexer's password stay in the run's directory, `fce/run` under your user cache directory, readable only by you.

fce then registers the machine, which attests the chain's challenge; asks one of the system's machines to check that it is available; and puts it in production.
Registering and the availability check pay fees, and each step pays gas.
Each tee-node start makes a new key, so each run registers a new machine.

A machine stays in production after it stops running, and sends keep being routed to it, so stopping pauses it on chain before removing the stack.
If pausing fails, fce keeps the run's directory, and `fce stop` tries again; `fce run` refuses to start until then.
It also refuses while another machine of the extension is in production; that is a recommendation.

## Checks

Each check is either required or a recommendation.
A required check covers what the extension can't work or be registered without; it always fails.
A recommendation fails too, unless the developer turns on the i-know-what-im-doing mode, with the flag `-i-know-what-im-doing` or a file `fce-i-know-what-im-doing` in the extension's directory; then it only warns.
`fce` says when the mode is on, and prints the file's text as the reason.
`fce repro` has no recommendations: it fails when the two builds differ.

## Development

```bash
make lint         # golangci-lint (pinned in the Makefile), and go.mod must be tidy
make test         # go test with cgo off, then on with the race detector
make test-fork    # deploy, add-version, governance, disable-version, status and send on an anvil fork of Coston2
make test-live    # deploy, run, send, direct and stop the template on Coston2, with real funds
```

Requirements: Go 1.26.9 or higher, and gcc for the cgo run.
The runner's tests use the scaffold's library as their reference extension; `go.mod` points at a local `../scaffold` checkout for now.
`make test-fork` registers that checkout's extensions on a fork, so it also needs Foundry (anvil and forge), Docker and the network; `FCE_FORK_URL` and `FCE_SCAFFOLD` override the chain and the checkout.
`make test-live` sends real transactions on Coston2, deploying a new extension each time: it needs a funded keystore account, `FCE_LIVE_KEYSTORE`, its password file, `FCE_LIVE_PASSWORD_FILE`, and an indexer database file as `fce run` takes, `FCE_LIVE_INDEXER_DB`.

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.
