# Rules

Every must and must-not of an extension, each with what enforces it.
They hold for tee-node v0.0.29, the version the scaffold pins.
The wire rules are in full in the [extension contract](extension-contract.md), and the trust model behind the rest in [assumptions](assumptions.md).

| Enforced by | Means |
|---|---|
| library | `pkg/`, the Go library, does it, or refuses at startup or dispatch; an extension in another language does it itself |
| contract base | `ExtensionBase`, which every entrypoint contract inherits, reverts |
| conformance (required) | `fce conformance` fails |
| conformance (recommendation) | `fce conformance` fails, unless the developer turns on the i-know-what-im-doing mode; then it warns |
| image check (required) | `fce image-check` fails |
| image check (recommendation) | `fce image-check` fails, unless the i-know-what-im-doing mode is on; then it warns |
| make repro | two builds without cache must give the same image hash |
| the template's tests | the tests a fork copies from the template catch it, as long as the fork keeps them |
| doc only | nothing checks it: it is the author's to follow |

## Answering tee-node

| Rule | Enforced by | More |
|---|---|---|
| Serve `POST /action` and `GET /state` on `EXTENSION_PORT`, where tee-node calls them. | library, conformance (required) | [process](extension-contract.md#process) |
| Listen on loopback only, `127.0.0.1`, never on every interface. | library, image check (recommendation) | [process](extension-contract.md#process) |
| Answer `POST /action` with 200 and an `ActionResult` within 2 s, the whole body included. | library, conformance (required) | [required by tee-node](extension-contract.md#required-by-tee-node) |
| Echo the action's id and submission tag in its result. | library, conformance (required) | [required by tee-node](extension-contract.md#required-by-tee-node) |
| Answer `GET /state` with 200 and a `TeeState`: `stateVersion` and `state`. | library, conformance (required) | [required by tee-node](extension-contract.md#required-by-tee-node) |
| Answer work that takes longer with `status 2` at once, then post partial results from status 4 up to 255, and one final result, `1` or `0`, through the sign port's `/result`; status 3 is tee-node's. | library; conformance (required) for the posts a fixture expects | [long-running results](extension-contract.md#long-running-results) |
| Answer every error you handle with 200, `status 0`, a log of `error: <message>` and empty `data`; only a body that isn't an action answers 400. | library, conformance (recommendation) | [conventions](extension-contract.md#conventions) |
| Give every answer all nine fields, `version` `1.0.0`, an empty `additionalResultStatus`, and Content-Type `application/json`. | library, conformance (recommendation) | [conventions](extension-contract.md#conventions) |
| Answer an unknown op pair, a pair on the other channel, an undecodable message and an op type starting with `F_` with `status 0`; the `F_` ones before any handler. | library, conformance (recommendation) | [conventions](extension-contract.md#conventions) |
| Ignore fields of tee-node's envelopes you don't know: a later tee-node may add some. | library, conformance (recommendation) | [conventions](extension-contract.md#conventions) |
| Stop on SIGTERM, and exit with code 0. | conformance (recommendation) | [process](extension-contract.md#process) |
| Keep messages and state within tee-node's limits: in raw bytes, about 50 KiB from a contract, 2.5 MiB for a direct action, and 5 MiB of state. | library | [encodings and limits](extension-contract.md#encodings-and-limits) |
| Keep results under tee-proxy's 25 MiB, measured as tee-node posts them; the library refuses any over `WithMaxResultSize`, 10 MiB by default, which is yours to set. | library | [fast or long-running](extension-guide.md#4-fast-or-long-running) |

## Handlers

| Rule | Enforced by | More |
|---|---|---|
| Never name an op type starting with `F_`, and keep op names within 32 bytes. | library | [add an op](extension-guide.md#2-add-an-op) |
| Give each op pair one channel, instructions or direct actions, never both, and take direct actions only for the ops meant for them. | library | [add an op](extension-guide.md#2-add-an-op) |
| Decode your own payloads strictly: ABI only in its canonical encoding, JSON without unknown fields or anything after the value. | library (`pkg/payload`), the template's tests | [add an op](extension-guide.md#2-add-an-op) |
| Honour `ctx`: a handler past its deadline stops, instead of changing state whose result nobody gets. | doc only | [fast or long-running](extension-guide.md#4-fast-or-long-running) |
| Synchronize shared state yourself: `/action` calls and `GET /state` overlap, and waiting for your locks counts against the deadline. | doc only | [add an op](extension-guide.md#2-add-an-op) |
| Check a replay counter and record it, with the change it guards, in one critical section: two copies of one action can run at once. | doc only | [add an op](extension-guide.md#2-add-an-op) |
| Check everything before the first write: a refused action changes nothing, not even what `GET /state` reports. | doc only | [add an op](extension-guide.md#2-add-an-op) |
| Bound background jobs yourself: the library runs as many as handlers start. | doc only | [fast or long-running](extension-guide.md#4-fast-or-long-running) |

## Results and output

| Rule | Enforced by | More |
|---|---|---|
| Put everything a consumer relies on in `data`: tee-node signs only `data`, the id, the submission tag and the status. | doc only | [what is signed](extension-contract.md#what-is-signed) |
| Keep secrets out of results and logs, which are public: wrap any error that could carry one in `PublicError`; the library's own errors never quote a payload. | doc only | [errors](extension-guide.md#3-errors) |
| Never write secrets to stdout or stderr: on a debug image or a test machine, the operator reads them. | doc only | [assumptions](assumptions.md#what-the-operator-sees) |

## Instructions and direct actions

| Rule | Enforced by | More |
|---|---|---|
| Have the contract encode `msg.sender` into the payload when the extension needs the caller: tee-node doesn't pass it. | doc only | [the entrypoint contract](contracts.md#2-the-entrypoint-contract) |
| Make each instruction idempotent, or refuse it when it arrives again, and have the contract number instructions where order matters: the operator can deliver one again, or after a newer one. | doc only | [direct example](../examples/direct/README.md#set_admin) |
| Authorize each direct action from its payload, signed over the TEE's address and the op pair: nothing says who posted it. | doc only | [who sent it](direct-actions.md#who-sent-it) |
| Make each direct action idempotent, or refuse it replayed: anyone who saw it can post it again, at any later time. | doc only | [replays](direct-actions.md#replays) |
| Never answer a direct action under the tags `threshold` or `end`, which are the instructions'. | library, conformance (recommendation) | [conventions](extension-contract.md#conventions) |

## Keys and signatures

| Rule | Enforced by | More |
|---|---|---|
| Never let a user choose the bytes the TEE key signs: `/sign` signs in tee-node's own domain, so build the message yourself, opening with a tag of your own. | doc only | [the TEE key](extension-guide.md#6-the-tee-key-and-wallet-keys) |
| Sign and hash only unambiguous encodings, such as `abi.encode`, never concatenated variable-length fields or JSON, and hash bytes as received, never a re-encoding. | doc only | [the TEE key](extension-guide.md#6-the-tee-key-and-wallet-keys) |
| Raise a sign-port signature's `V` from 0 or 1 to 27 or 28 before a contract checks it. | contract base (`_teeSigner`) | [consuming results](contracts.md#8-consuming-results) |
| Build what a wallet key signs yourself, and check the key's type and the instruction's cosigners against the wallet's: tee-node signs with any key the TEE holds. | doc only | [wallets example](../examples/wallets/README.md#in-the-tee) |
| Have input a user encrypts on chain name its owner, and check it against the `msg.sender` the contract encodes: anyone can send a copy. | doc only | [the entrypoint contract](contracts.md#2-the-entrypoint-contract) |
| Give input users encrypt on chain a fixed length, padding what varies: a ciphertext is as long as its plaintext, and public. | doc only | [direct example](../examples/direct/README.md#bid) |
| Count an M-of-N authorization by distinct signers, accept only low-s signatures, and never use a signature's bytes as an identifier. | doc only | [who sent it](direct-actions.md#who-sent-it) |

## State

| Rule | Enforced by | More |
|---|---|---|
| Report the zero state, a zero `stateVersion` and no bytes, unless the extension registers a state verifier contract: without one, the registry refuses any other. | doc only | [state](extension-guide.md#5-state) |
| Name the state's encoding with `stateVersion`. | doc only | [state](extension-guide.md#5-state) |
| Never let a state verifier revert, which blocks every availability check of the machine, and decide what it says to tee-node's state-unavailable answer and to a machine that just booted. | doc only | [direct example](../examples/direct/README.md#state) |
| Design for losing state: a restart wipes it and brings a new TEE identity, and each machine has its own. | doc only | [assumptions](assumptions.md#a-tee-lives-once) |
| Bind what results depend on into the signed result or the verified state when a contract configures each machine: an instruction may never arrive. | doc only | [direct example](../examples/direct/README.md#state) |

## Outside services

| Rule | Enforced by | More |
|---|---|---|
| Call outside services over `https` only, verified against the CA bundle in the image, following no redirects, with a timeout and a cap on each answer. | doc only | [external-api example](../examples/external-api/README.md#trusting-the-sources) |
| Let no caller or operator input pick an outbound host, and send a credential only to the host it was issued for. | doc only | [external-api example](../examples/external-api/README.md#trusting-the-sources) |
| Decode outside answers strictly, check that they answer what was asked, and refuse a timestamp from the future where you rely on one. | doc only | [external-api example](../examples/external-api/README.md#trusting-the-sources) |

## Settings

| Rule | Enforced by | More |
|---|---|---|
| List every variable the extension reads in the launch-policy label and in the launcher's `--env`. | image check (recommendation), which compares the two lists but can't see what the code reads | [your own variables](extension-guide.md#8-your-own-environment-variables) |
| Fail at startup on a missing or malformed setting: no fallback default, stub or test mode in the image's binary, and no zero where zero turns a check off. | doc only; the library does it for its own variables | [your own variables](extension-guide.md#8-your-own-environment-variables) |
| Have the operator set `CHAIN_ID` and `EXTENSION_ID` at launch when the extension binds signatures to them: values set through tee-node's port 5500 don't reach the extension. | doc only | [assumptions](assumptions.md#the-operator) |

## The contract

| Rule | Enforced by | More |
|---|---|---|
| Give every function that calls `_send` the `chargesFees` modifier. | contract base | [sending and fees](contracts.md#3-sending-and-fees) |
| Name each TEE once per send: the platform charges a repeat once and keeps the rest. | contract base | [sending and fees](contracts.md#3-sending-and-fees) |
| Never send an empty op type, op command or message, or to a TEE out of production: the platform reverts the whole send. | the template's tests, whose mock refuses them | [the entrypoint contract](contracts.md#2-the-entrypoint-contract) |
| Keep the op names and each payload's layout the same in the contract and the extension. | the template's tests, which send the fixture's payload through the contract | [testing](contracts.md#6-testing) |
| Check a TEE's signature against the registry: a production TEE of this extension, while the extension isn't emergency-paused. | contract base (`_teeSigner`) | [consuming results](contracts.md#8-consuming-results) |
| Secure the deployer's key, or move both the contract and the extension to another owner, before production: the registry owner decides what code the machines run, the contract owner sets the project fee and withdraws it. | doc only | [the owners](contracts.md#4-the-owners) |
| Write anything the contract stores about a TEE's identity after the TEE is registered, and again after it relaunches. | doc only | [lifecycle](contracts.md#5-lifecycle) |

## Consumers of results

| Rule | Enforced by | More |
|---|---|---|
| Time out pending results: a restart in the middle of a job leaves the action pending forever. | doc only | [long-running results](extension-contract.md#long-running-results) |
| Bind a signed result to its request, such as an instruction id the contract recorded, and bound how late it may land by block time: the TEE's clock is no trust anchor. | doc only | [consuming results](contracts.md#8-consuming-results) |
| Accept tee-node's own signature on a result only with the tag `threshold`, status 1 and an instruction id the contract recorded. | doc only | [consuming results](contracts.md#8-consuming-results) |
| Check `getAvailabilityCheckValidity` where freshness matters: a machine stays in production after its availability check lapses. | doc only | [consuming results](contracts.md#8-consuming-results) |
| Require answers from several distinct TEEs where one machine must not decide: with one, whoever submits chooses among the answers. | doc only | [assumptions](assumptions.md#several-tees) |

## The image

| Rule | Enforced by | More |
|---|---|---|
| Ship an image that boots and keeps running. | image check (required) | [architecture](architecture.md#the-image) |
| Bake `MODE=0` into the image: without it, tee-node attests in test mode. | image check (required) | [architecture](architecture.md#the-image) |
| Label the image with a title and a version, the version at most 32 bytes: it is registered as a `bytes32`. | image check (required) | [start from the template](extension-guide.md#1-start-from-the-template) |
| Change the Dockerfile only in its `# per extension:` lines. | doc only | [start from the template](extension-guide.md#1-start-from-the-template) |
| Build the image with `make image` or `fce image`, never a plain `docker build`, which gives another hash. | make repro, image check (recommendation) | [reproducibility](../REPRODUCIBILITY.md#build-with-make-image) |
| Pin every image by digest, give every `COPY` from the repository `--chmod`, use no `ADD` and no unpinned `# syntax=`, and declare `ARG SOURCE_DATE_EPOCH` before the first `FROM`. | image check (recommendation) | [reproducibility](../REPRODUCIBILITY.md#traps) |
| List tee-node's variables in the launch-policy label, and never `MODE`, `SIGN_PORT` or `EXTENSION_PORT`, which the image fixes. | image check (recommendation) | [ports](architecture.md#ports) |
| Expose only tee-node's port 5500. | image check (recommendation) | [ports](architecture.md#ports) |
| Keep every launcher argument in `ENTRYPOINT`, with no `CMD`, and never set `allow_cmd_override=true` or `log_redirect=always`. | image check (recommendation) | [assumptions](assumptions.md#what-the-operator-sees) |
| Run the extension as a non-root user with no capabilities. | image check (recommendation) | [architecture](architecture.md#the-image) |
| Run tee-node at the version `go.mod` requires. | the Dockerfile refuses a `replace` of it; image check (recommendation) | [reproducibility](../REPRODUCIBILITY.md#traps) |
| Leave the shared parts untouched, and propose changes to them upstream. | doc only | [forking](fork.md) |
