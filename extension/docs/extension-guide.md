# Extension guide

From the template to your own extension, in Go.
The wire rules every extension follows, in any language, are in [extension-contract.md](extension-contract.md).

[contracts.md](contracts.md) covers the contract, and [rules.md](rules.md) lists every rule with what enforces it.

## 1. Start from the template

Fork the scaffold, then work in [`template/`](../template):

- `template/cmd/extension/main.go`: the entrypoint, about 15 lines of explicit wiring.
- `template/internal/app`: your code: op constants, handlers, state, and their tests.
- `template/conformance/`: golden fixtures for your ops.
- `template/contracts/`: the entrypoint contract, which sends your instructions; see [contracts.md](contracts.md).

Change the template; leave the shared parts alone: `pkg/`, `sol/`, `conformance/`, `internal/`, `launcher/`, `docs/`, `fce-version`, `REPRODUCIBILITY.md`, the `Makefile`, `.golangci.yml`, `renovate.json5`, and the agent files.
A fork gets fixes to them by merging upstream, and local edits there make those merges conflict.
If you need a change in a shared part, propose it upstream.

Renaming the module path is optional: imports of `pkg/` resolve inside the module, so a fork builds without it.
[Forking](fork.md) shows the renames, and how to merge upstream.

## 2. Add an op

An op is a pair: an op type, which groups commands, and an op command, a verb.
An extension may have several op types.
Each pair belongs to one channel, instructions or direct actions, never both.

| | Instruction | Direct action |
|---|---|---|
| Sent by | your contract, on chain | a client, to tee-proxy's `POST /direct` |
| Checked by tee-node | the data providers' signatures and threshold | only that its op type isn't a system one, `F_` |
| Who may send it | decided on chain | anyone the proxy lets in; your handler authorizes |
| Payload | ABI, as the contract encodes it | your choice; JSON in the template |

Add the constants, write the handler, and register it:

```go
const OPCommandGetPrice op.Command = "GET_PRICE" // instructions only

func Register(s *extension.Server) {
	// …
	s.RegisterInstructionHandler(OPTypeMyExtension, OPCommandGetPrice, getPrice)
}

// abi.encode(string pair), as the contract builds it
var getPriceArgs = abi.Arguments{{Name: "pair", Type: abiType("string")}}

func getPrice(ctx context.Context, i *instruction.DataFixed) ([]byte, extension.Run, error) {
	var pair string
	if err := payload.DecodeABI(getPriceArgs, i.OriginalMessage, &pair); err != nil {
		return nil, nil, extension.PublicError("invalid payload", err)
	}
	// …
	return result, nil, nil
}
```

- Registration panics on a mistake: a pair registered twice, an op type starting with `F_`, or a name over 32 bytes.
- An instruction's payload is `i.OriginalMessage`; `i` also carries the instruction id, the TEE id and the cosigners.
  There is no sender: if you need the caller, your contract encodes `msg.sender` into the payload.
  tee-node doesn't remember instructions: the operator can deliver one again, or after a newer one, while its signing policy is the active one or the one before.
  So make it idempotent, or refuse replays; where order matters, have the contract number them, as the [direct example](../examples/direct/README.md)'s `setAdmin` does.
- A direct action's payload is `d.Message`.
  Anyone the proxy lets in can send it, and the handler can't tell who did, so check an authorization the payload carries.
  Anyone who saw it can send it again, so make it idempotent, or refuse replays.
  [Direct actions](direct-actions.md) and the [direct example](../examples/direct/README.md) show how.
- A direct action arrives with the `submit` tag, which tee-proxy's `/direct` sets.
  If you adapt the proxy to send your own tags, accept them with `extension.WithDirectTags` in `main.go`, and give the fixtures a `submissionTag`; results echo the tag, handlers don't see it.
  `threshold` and `end` stay the instructions' tags: `extension.WithDirectTags` ignores them.
- Decode your own payloads strictly, with [`pkg/payload`](../pkg/payload).
  `payload.DecodeABI` accepts only the canonical encoding: go-ethereum's decoder alone accepts trailing bytes, dirty padding, out-of-range integers, and offsets that point anywhere, even at one region many times over, which can exhaust memory.
  `payload.DecodeJSON` refuses unknown fields and anything after the value.
  Their errors never quote the payload.
- The library doesn't serialize calls.
  An instruction, a direct action and `GET /state` can run at the same time; synchronize shared state yourself.
  Time spent waiting for your own locks counts against the deadline.
  Two copies of one action can run at once too, so check a replay counter and record it, with the change it guards, under one lock, as the direct example's `ROTATE` does.
- Check everything before the first write: a refused action changes nothing, not even what `GET /state` reports, or whoever can send one moves your state.

## 3. Errors

A handler's error becomes the result's log, `error: <text>`, which is public and unsigned.
Wrap anything that could carry a secret, such as decrypted data, keys, or an outside service's answer:

```go
return nil, nil, extension.PublicError("invalid payload", err)
```

Only `invalid payload` reaches the log; `err` stays available through `errors.Is` and `errors.As`.
The library's own errors never echo payload bytes.
Don't write secrets to stdout or stderr either: on a debug image or a test machine, the operator reads them.

## 4. Fast or long-running

A handler has `extension.HandlerTimeout`, 1.5 s, to answer; tee-node waits 2 s in all.
After that the library answers `status 0` with `error: deadline exceeded`, and a result the handler returns later is lost.
Honour `ctx`, so a late handler stops instead of changing state nobody will hear about.

The handler decides per action:

| It returns | The action gets |
|---|---|
| an error | `status 0` at once; for example, a failed check |
| data | `status 1` at once; for example, a cached answer |
| data and a `Run` | `status 2` at once, then the `Run` continues in the background |

```go
return []byte("accepted"), func(ctx context.Context, p *extension.Progress) ([]byte, error) {
	for _, part := range parts {
		if err := p.Post(ctx, encode(part)); err != nil { // status 4, 5, …
			return nil, err
		}
	}
	return final, nil // status 1, or 0 with an error
}, nil
```

- `p.Post` posts a partial result and waits until tee-node takes it.
  Statuses end at 255, so a job has 252 partials.
  To keep working while a post is in flight, call `p.Post` from your own goroutine; statuses follow call order.
- Long-running mode needs the sign port: `main.go` passes `extension.WithSignPort`.
- These defaults are yours to set, in `main.go`:
  - `extension.WithPostWindow`: how long each post retries before the result is dropped and logged; one minute by default.
  - `extension.WithMaxResultSize`: the largest result tee-node may post; 10 MiB by default, measured as tee-node posts it, signed with `data` hex-encoded.
    A larger answer or final result becomes `status 0`, and a larger partial isn't posted.
    tee-proxy refuses more than 25 MiB.
- The library doesn't limit how many jobs run at once; bound them yourself if you need to.
- Nor does it limit how long a job runs: bound it yourself, with `context.WithTimeout` on the `Run`'s context, and return the context's error as the failure.
  The external-api example bounds each source it fetches this way.
- Jobs stop when the server stops.
  A restart in the middle of a job leaves the action pending forever, so consumers must time out pending results.
- A panic in a `Run` becomes a final `status 0`; a panic in a goroutine you start yourself ends the process.
- The [external-api example](../examples/external-api/README.md) works this way: it fetches from three APIs in the background, posting each answer as a partial result.

## 5. State

`GET /state` reports the extension's state, which tee-node includes in its signed TEE info.
Without a state function the library serves the zero state: a zero `stateVersion` and no bytes.

```go
s.RegisterStateFunc(func(ctx context.Context) (common.Hash, []byte, error) {
	return stateVersion, encodedState, nil
})
```

- You choose the encoding of the bytes; `stateVersion` names it.
- The registry accepts a non-empty state only from an extension with a registered state verifier contract.
  At each availability check the verifier decides whether the machine may stay in production; a machine whose state it refuses can be suspended by anyone.
  The [direct example](../examples/direct/README.md) reports the admin its contract set, and the contract, as the verifier, checks it.
- State lives in memory: a restart wipes it, and each machine has its own.
- The state function has the same deadline as a handler, and the response is capped at 10 MiB.

## 6. The TEE key and wallet keys

[`pkg/node`](../pkg/node) calls tee-node's sign port:

```go
tee := node.New(env.SignPort)
sig, err := tee.SignTEE(ctx, msg)          // EIP-191 over keccak256(msg)
plain, err := tee.Decrypt(ctx, ciphertext) // ECIES with the TEE key
addr, err := tee.TEEAddress(ctx)           // the teeId; once, at startup
```

- `SignTEE` signs in the same domain as tee-node's own signatures.
  Never let a user choose the bytes it signs: build the message yourself, in a format only your extension uses.
  Encode it unambiguously, with `abi.encode`, never by concatenating variable-length fields, and hash bytes as received, never a re-encoding.
  The [external-api example](../examples/external-api/README.md) signs its price this way, for its contract to check.
- Users encrypt to the TEE's public key, which tee-node publishes in its TEE info.
  The [direct example](../examples/direct/README.md) takes such input both ways: the admin's secrets as direct actions, users' bids through its contract.
  A ciphertext is as long as its plaintext, and public: give such input a fixed length, padding what varies, as the direct example's bids do.
  A restarted TEE has a new key, so older ciphertexts can no longer be decrypted.
- A call retries while tee-node isn't listening yet, until `ctx` ends.
- `node.ErrBadRequest` means tee-node refused the input, for example a ciphertext it can't decrypt.

The wallet keys the TEE holds are reached the same way:

```go
info, err := tee.KeyInfo(ctx, walletID, keyID)                     // its type, algorithm and public key, the wallet's cosigners
sig, err := tee.SignWallet(ctx, walletID, keyID, msg)              // by the key's algorithm, no prefix
plain, err := tee.DecryptWallet(ctx, walletID, keyID, ciphertext) // ECIES with the wallet key
```

- tee-node generates the keys and keeps them; the extension learns a wallet's id and key id only from its payloads.
- tee-node signs with any key the TEE holds, and checks only an instruction's own cosigners.
  So the contract decides who may use which wallet, and the extension checks the key's type and the instruction's cosigners against the wallet's.
  The [wallets example](../examples/wallets/README.md) signs EVM transactions this way.
- `node.ErrNotFound` means the TEE doesn't hold that key.

## 7. Testing

```bash
go test ./template/...                    # handler tests, and the fixtures are current
go test ./template/internal/app -update   # rewrite the fixtures after changing them
make lint test                            # the linters, and the Go and forge tests
fce conformance ./template                # build the binary and check it from the outside
```

- Test a handler by calling it directly with a `DataFixed` or a `DirectInstruction`, as the template's tests do; the envelope is the library's job.
- `internal/app/fixtures_test.go` builds the fixtures from Go values, and `golden.Check`, from the shared `internal/golden`, compares them with the committed files.
  Without `-update` it fails when a committed fixture is missing, extra or out of date.
  Either way it fails on a fixture set the runner would refuse, such as one op pair on both channels.
- `fce conformance ./template`, with `fce` the tooling repo's CLI, builds the binary and checks it from the outside: your fixtures, then the rules every extension follows.
- If you deviate from a recommendation on purpose, add `fce-i-know-what-im-doing` to the extension's directory, with the reason as its text; `fce` then warns instead of failing, and prints the reason.
- The contract's forge tests read the instruction fixtures, so the contract and the handler can't drift apart; see [contracts.md](contracts.md).
- [Testing](testing.md) covers every layer, and what to run before a change lands.

## 8. Your own environment variables

Read them in `main.go`, where the template has a `TODO`, with `os.LookupEnv`, so an unset variable and an empty one can be told apart.
Fail at startup when one is missing or malformed: the image's binary has no fallback default, stub or test mode to fall back on, and a zero that would turn a check off is refused.
The image passes the extension only the variables it lists, so each one needs a line in `template/Dockerfile`: in the launch-policy label, which lets the operator set it, and in the launcher's `--env` list, which passes it on.
`fce conformance` passes its own environment through, so `MY_VAR=value fce conformance ./template` works; it also takes `-env KEY=VALUE`.
