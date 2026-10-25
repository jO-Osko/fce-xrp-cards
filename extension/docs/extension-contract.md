# Extension contract

What an extension must do to work with tee-node, in any language.

## Scope

The rules hold for tee-node v0.0.29, the version the scaffold pins in `go.mod`.
Limits and behaviour may change with a tee-node release; the scaffold re-checks them on every bump.

Each rule is one of two kinds:

- **Required**: tee-node or tee-proxy depends on it; breaking it breaks the extension, and `fce conformance` fails it.
- **Convention**: the scaffold's library follows it, so every extension behaves alike; tee-node doesn't depend on it.
  `fce conformance` fails an extension that breaks it, unless the developer turns on the i-know-what-im-doing mode; then it warns.

The **Checked** column says whether the conformance runner checks the rule.
Rules it can't check are for the author to follow.

## Process

The image runs two processes: tee-node and the extension.
They talk over loopback, and neither port is reachable from outside.

| Rule | Kind | Checked |
|---|---|---|
| The extension serves HTTP on `127.0.0.1:EXTENSION_PORT`. tee-node calls `http://localhost:EXTENSION_PORT`. | required (reachable there); loopback only is a convention | yes (reachable) |
| The extension calls tee-node's sign port on `SIGN_PORT`, at `127.0.0.1`. | required (the port); `127.0.0.1` rather than a name is a convention | — |
| `EXTENSION_PORT` and `SIGN_PORT` are parsed as tee-node parses them: a decimal number, not trimmed, from 1 to 65535, not `5500` (tee-node's config port), and not equal to each other. Unset or empty takes tee-node's defaults, `8889` and `8888`. Any other value fails startup. | convention | — |
| tee-node may start listening after the extension: a refused connection to the sign port is retried. | convention | — |
| On SIGTERM the extension stops and exits with code 0. | convention | yes |

## Required by tee-node

tee-node calls the extension on two endpoints only: `POST /action` and `GET /state`.

| Rule | Checked |
|---|---|
| `POST /action` answers 200 with an `ActionResult` JSON object within 2 s, counted until tee-node has read the whole body. | yes |
| Any other status code becomes a `status 0` result whose log is `extension error: unexpected status code: <code>, response: <body>`, so the body ends up public. | — |
| An answer that takes longer than 2 s becomes a `status 3` result, which tee-node posts itself; the Go library answers a handler that runs past 1.5 s with a `status 0` failure first. | — |
| The result echoes the action's `data.id` as `id` and its `data.submissionTag` as `submissionTag`. | yes |
| `GET /state` answers 200 with a `TeeState` JSON object: `stateVersion` (bytes32) and `state` (bytes). Any other answer records the state as unavailable. | yes |

### Statuses

| Status | Meaning | Set by |
|---|---|---|
| `0` | failed; final | the extension, or tee-node on a broken answer |
| `1` | succeeded; final | the extension |
| `2` | pending: more results follow through the sign port's `/result` | the extension |
| `3` | the extension missed tee-node's 2 s | tee-node only |
| `4`–`255` | partial results of a pending action, in increasing order | the extension, through `/result` |

tee-proxy keeps one result per action id and submission tag.
A final result (`0` or `1`) is never replaced.
A status of `2` or more is replaced only by a higher status, or by a final one.

tee-node calls `POST /action` once per action, with no retry, and tee-proxy queues an action again only when delivering it failed.
So an id arrives again only when the operator delivers it again, or when the data providers sign the same instruction twice.

### What is signed

tee-node signs `keccak256(keccak256(data) ‖ id ‖ keccak256(submissionTag) ‖ status)`, bound to the chain, before posting the result to tee-proxy.
`log`, `opType`, `opCommand`, `additionalResultStatus` and `version` are not signed, so tee-proxy could change them.
Everything a consumer relies on goes in `data`.

### Messages

| Channel | `data.type` | `data.submissionTag` | `data.message` |
|---|---|---|---|
| instruction | `instruction` | `threshold` | a `DataFixed` object; the payload is its `originalMessage` |
| direct | `direct` | `submit`, unless the application adds its own | a `DirectInstruction` object: `opType`, `opCommand`, `message` |

- **Instructions** come from the chain.
  tee-node has already checked the data providers' signatures and the threshold, and the cosigners' against the set and threshold the instruction names.
  That set is the contract's choice: an op that needs particular cosigners checks the instruction names them, as the wallets example checks a wallet's own.
  It answers the instruction's `end` phase itself, and gives any other tag `status 0`, so the extension only ever sees `threshold`.
  `DataFixed.instructionId` equals the action id.
  tee-node doesn't remember instructions: the operator can deliver one again, or after a newer one, while its signing policy is the active one or the one before.
  There is no sender field: a contract that needs the caller encodes `msg.sender` into the payload.
- **Direct actions** come from tee-proxy's `POST /direct`, which an operator may enable, with or without an API key.
  tee-node checks only that the op type doesn't start with `F_`, and that the submission tag isn't an instruction's, `threshold` or `end`; it forwards any other tag, and tee-proxy's `/direct` sets `submit`.
  The extension authorizes direct actions itself, and keeps each one idempotent or protected against replay; see [direct actions](direct-actions.md).
  The action id is random, made up by tee-proxy, which returns it to the poster: the result can be fetched only with it.

### Encodings and limits

- Op types and commands are UTF-8 names right-padded to 32 bytes.
  Op types starting with `F_` are reserved for the system.
  tee-node handles some itself and refuses the rest, on both channels, so none reaches the extension.
- Bytes are `0x`-prefixed hex; empty bytes are `"0x"`; hashes are full width.
- tee-node fetches each action with a 10 MiB cap, and cuts the `/state` response at 10 MiB.
- An instruction's message is at most 100 KiB, a direct action's at most 10 MiB.
  Payloads are hex inside JSON, so in raw bytes that's about 50 KiB from a contract, about 2.5 MiB for a direct action, and about 5 MiB of state.

## Conventions

| Rule | Checked |
|---|---|
| Every error the extension handles answers 200 with `status 0`, a log of `error: <message>` and empty `data`, with the id and submission tag echoed, so the result attaches to its action. | yes |
| A body that isn't an action (invalid JSON, `null`, a zero id, over 10 MiB) answers 400: the extension can't echo the id, so tee-node builds the failed result itself. | yes (invalid JSON) |
| Other paths answer 404, other methods 405. | yes |
| Every answer carries all nine fields: `id`, `submissionTag`, `status`, `log`, `opType`, `opCommand`, `additionalResultStatus`, `version`, `data`. | yes |
| Every 200 answer has Content-Type `application/json`. | yes |
| `log` is `ok` (status 1), `pending` (2), `partial` (4–255) or `error: <message>` (0). It is public and unsigned, so it never contains secrets. | partly: the `error: ` prefix on refusals; fixtures pin the rest |
| `version` is `1.0.0`: tee-node's result encoding version, not the extension's. | yes |
| `additionalResultStatus` is empty: tee-node neither uses nor signs it for an extension. | yes |
| Each op pair belongs to one channel, instructions or direct actions, never both; on the other channel it gets `status 0`. An op type may have commands on both. | yes (the fixtures' pairs) |
| An op pair with no handler gets `status 0`. | yes |
| An op type starting with `F_` gets `status 0`, before any handler, though tee-node refuses them first. | yes (`F_XRP` / `PAY`) |
| A direct action carries `submit`, as tee-proxy's `/direct` sets it. This is offered, not required: an application may use its own direct tags if it adapts tee-proxy to match, both its `/direct` and its result query. The extension accepts `submit` and the tags it adds (in Go, `extension.WithDirectTags`), and answers any other direct tag with `status 0`. It never adds `threshold` or `end`, which `extension.WithDirectTags` ignores: they are the instructions' tags, and tee-proxy picks a direct action's id, so a direct result with one could pass as an instruction's; tee-node refuses them first. | partly: a fixture's `submissionTag` is accepted; each direct fixture, tagged `threshold`, is refused |
| The extension's own payloads are decoded strictly; node-defined envelopes leniently, so a field a later tee-node adds is ignored. | yes (envelopes) |
| A direct action is idempotent, so a second send at any later time changes nothing, or protected against replay: anyone who saw it can post it again. | no; a fixture's `again` pins what a second send answers |
| An instruction is idempotent or protected against replay too, and numbered by its contract where order matters: the operator can deliver one again, or after a newer one. | no; a fixture's `again` pins what a second send answers |

## Long-running results

An action that needs more than 2 s answers `status 2` at once, with data if it has any, and continues in the background.
It posts each further result to the sign port's `POST /result`, as a full `ActionResult` with the same id, submission tag and op pair:

1. partial results with statuses from `4` up to `255`, each higher than the last;
2. the final result, `1` or `0`.

tee-node checks nothing in a posted result: it signs whatever it gets and forwards it to tee-proxy.

| `/result` answers | Meaning |
|---|---|
| 200 | forwarded |
| 400 | not a valid `ActionResult`; unknown fields are refused |
| 500 | tee-node has no proxy URL yet, or couldn't sign |
| 502 | forwarding failed; tee-node also posts an unsigned failure with a zero id, which attaches to no action |

- Re-posting a result is safe: tee-proxy ignores a result that doesn't advance the stored one.
- Posts may land out of order; tee-proxy keeps the highest.
- A restart in the middle of a job leaves the action pending forever, so consumers time out pending results.
- tee-proxy keeps instruction results for 14 days and direct-action results for 30 minutes by default, renewed on each post; the operator configures both.
- tee-proxy refuses a result body over 25 MiB, measured as tee-node posts it: signed, with `data` hex-encoded.
  tee-node uploads each result with a 2 s timeout, so the practical limit also depends on the link to tee-proxy.

## Sign-port API

tee-node serves these to the extension on `127.0.0.1:SIGN_PORT`, without authentication.
Bodies are JSON.
In `/sign` and `/decrypt` bodies, bytes are **base64**, not hex.
`/result` takes an `ActionResult` encoded as in `/action` answers: bytes and hashes are `0x`-prefixed hex.

| Endpoint | Request | Answer |
|---|---|---|
| `POST /sign` | `{"message"}` | `{"message", "signature"}`: the TEE key signs `keccak256(message)` with the Ethereum signed-message prefix; `V` is 0 or 1 |
| `POST /decrypt` | `{"encryptedMessage"}` | `{"decryptedMessage"}`: ECIES on secp256k1, AES-128, SHA-256, with the TEE key |
| `POST /result` | an `ActionResult` | see [Long-running results](#long-running-results) |
| `GET /key-info/{walletId}/{keyId}` | | the key's existence proof: its type, signing algorithm and public key (64 bytes, X‖Y), and the wallet's admins and cosigners |
| `POST /sign/{walletId}/{keyId}` | `{"message"}` | `{"message", "signature"}`: a wallet key signs by its algorithm, with no prefix; an EVM key signs `keccak256(message)`, `V` 0 or 1 |
| `POST /decrypt/{walletId}/{keyId}` | `{"encryptedMessage"}` | `{"decryptedMessage"}`: ECIES with a wallet key; EVM and XRP keys only |

- `/sign` uses the same signing domain as tee-node's own signatures.
  Never let a user choose the bytes the TEE key signs.
- Users encrypt to the TEE's public key, which tee-node publishes in its TEE info; `fce encrypt` does it.
  A restarted TEE has a new key, so older ciphertexts can no longer be decrypted.
- 400 means the node refused the input: an empty message, or a ciphertext it can't decrypt.
  Its body may echo the input, so it never goes into a public log.
- `walletId` is 32 bytes of hex, `keyId` a decimal; 404 means the TEE doesn't hold that key.
- The wallet-key endpoints serve any key the TEE holds: who may use which wallet is for the extension and its contract to decide.

## Conformance

The runner, `fce conformance` from the tooling repo, checks an extension from the outside.
Given an extension directory, it builds the Go binary as the image does and uses the directory's `conformance/` fixtures; after `--`, it runs any command, in any language:

```bash
fce conformance [-env KEY=VALUE]... <dir>...
fce conformance [-fixtures <dir>] [-env KEY=VALUE]... -- <command> [args...]
```

1. It starts a fake sign port with a fixed test key, which signs, decrypts and records `/result` posts, and one wallet key: wallet `keccak256("fakenode wallet")`, key 0, an EVM key with no cosigners.
2. It starts the command with `EXTENSION_PORT` and `SIGN_PORT` set to free loopback ports, and waits up to 10 s for `GET /state` to answer 200.
3. It runs the checks below, waiting up to 10 s for the posts long-running fixtures expect.
4. It sends SIGTERM and requires exit code 0 within 5 s.

`-startup`, `-postwait` and `-shutdown` change those limits.
The extension's output is printed only when something fails.

### Fixtures

Each `*.json` file in the fixtures directory is one golden case for one of the extension's ops; the Go package [`conformance/`](../conformance) reads and writes them:

| Field | Meaning |
|---|---|
| `description` | what the case shows |
| `channel` | `instruction` or `direct` |
| `submissionTag` | optional, direct only: the tag the action arrives with; `submit` when absent |
| `opType`, `opCommand` | the op pair, as names |
| `message` | the payload, hex: an instruction's `originalMessage`, or a direct action's `message` |
| `result` | the expected `status`, `log` and `data` |
| `again` | optional, for an op that changes state: the `status`, `log` and `data` every later send of the same message gets; `result` when absent |
| `posts` | for a long-running op, the expected `/result` posts, each with `status`, `log` and `data` |

The runner builds the action around the payload, and checks the fields the extension doesn't choose itself: the echoed id and submission tag, the op pair, the version and an empty `additionalResultStatus`.
Field order and whitespace don't matter; `data` must match byte for byte.
Posts are compared in status order, partials then the final, since they may land out of order.
The runner sends the fixtures in name order to one process, so a fixture may rely on the ones before it; numbered names keep that order visible.

The runner refuses a fixtures directory that is missing or holds no fixture, and a fixture set that:

- has a `status 2` result without posts, or posts after any other status;
- has a `status 2` in `again`;
- sets `submissionTag` on an instruction;
- uses an op type starting with `F_`;
- puts one op pair on both channels.

### Checks

| Check | Expects |
|---|---|
| `GET /state` | 200, JSON, with `stateVersion` and `state` |
| each fixture | its result, and its posts |
| each fixture with `again`, sent again | its `again` |
| each fixture with an unknown field at every level | the same result, or its `again` |
| each fixture's op pair on the other channel | a refusal |
| each direct fixture, tagged `threshold` | a refusal |
| an unknown op pair, on each channel | a refusal |
| `F_XRP` / `PAY`, on each channel | a refusal |
| an undecodable message, on each channel | a refusal |
| a body that isn't an action | 400 |
| `GET /action`, `POST /state`, an unknown path | 405, 405, 404 |
| posts | exactly the posts the fixtures expect, in status order, once none has arrived for 200 ms |

A refusal is 200 with `status 0`, a log starting with `error: `, and empty `data`.
Every `/action` answer must arrive within 2 s, be JSON, and carry all nine fields.
