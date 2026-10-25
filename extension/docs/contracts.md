# Contracts

An extension's entrypoint contract sends its instructions to its TEE machines, on chain.
The wire rules between tee-node and the extension are in [extension-contract.md](extension-contract.md); this page covers the Solidity side.

## 1. Layout

| Path | What | Who edits it |
|---|---|---|
| `sol/src/ExtensionBase.sol` | the base every entrypoint contract inherits: extension id, sending, fees, owner | nobody in a fork; fixes come from upstream |
| `sol/test/` | the base's own tests, and `MockFlareTeeManager`, which every extension's tests use | nobody in a fork |
| `<ext>/contracts/` | the extension's own Foundry project | the extension's author |
| `<ext>/contracts/src/` | the entrypoint contract, `InstructionSender` in the template | |
| `<ext>/contracts/script/` | `Deploy.s.sol` and `PostRegister.s.sol` | |
| `<ext>/contracts/test/` | the contract's forge tests, against `MockFlareTeeManager` | |
| `<ext>/contracts/foundry.toml`, `soldeer.lock` | compiler settings and pinned dependencies | |

- Each extension is its own Foundry project; `foundry.toml` remaps `sol/` in as `fcc-extension-scaffold/`.
  `sol/` is a Foundry project too, holding the base's tests.
- Dependencies come through Soldeer, pinned by `soldeer.lock`: flare-periphery for the Flare interfaces, OpenZeppelin for the owner role, forge-std for tests and scripts.
- Import names carry no version (`flare-periphery/…`), so a version bump touches only `foundry.toml` and the lockfile.
- An extension compiles `sol/` with its own dependencies, so it pins `sol/`'s at the same versions; `make lint` checks it.
- `forge build` doesn't fetch dependencies: run `forge soldeer install` first on a fresh clone.
  `make lint` and `make test` do it for `sol/` and every extension.

## 2. The entrypoint contract

```solidity
contract InstructionSender is ExtensionBase {
    bytes32 public constant OP_TYPE_MY_EXTENSION = bytes32("MY_EXTENSION");
    bytes32 public constant OP_COMMAND_MY_COMMAND = bytes32("MY_COMMAND");

    constructor(address flareTeeManager) ExtensionBase(flareTeeManager) {}

    function myCommand(bytes calldata message) external payable chargesFees returns (bytes32) {
        return _send(OP_TYPE_MY_EXTENSION, OP_COMMAND_MY_COMMAND, abi.encode(msg.sender, message));
    }
}
```

- The constructor takes the FlareTeeManager diamond, which serves every TEE facet, and refuses an address without code.
- Op constants must equal the Go side's names: Solidity's `bytes32("MY_EXTENSION")` and Go's `op.Type("MY_EXTENSION").Hash()` give the same bytes, UTF-8 right-padded to 32.
- Write one payable send function per instruction command, with the `chargesFees` modifier.
- tee-node doesn't pass the caller to the extension, so encode `msg.sender` into the payload when the extension needs it.
- Input a user encrypts to a TEE's key is public on chain: have its plaintext name the sender, and the extension check it against the `msg.sender` the contract encodes, or bind it to the sender another way, so nobody can send a copy as theirs.
  The [direct example](../examples/direct/README.md)'s `bid` does.
- Direct commands have no function here: clients post them to tee-proxy's `/direct`.
  A tee-proxy fork, or a service in front of `/direct`, can add checks of its own; the extension still authorizes every direct action itself.
- The platform refuses an empty op type, op command or message, and op types starting with `F_`, which are the system's: the chain takes them only from system senders, and `/direct` refuses them.

## 3. Sending and fees

Every function that calls `_send` carries `chargesFees`.
`msg.value` then pays for every instruction the function sends: each `_send` takes its price from what is left, and the contract keeps the rest, with the project fees.
Collected project fees never pay for an instruction.
`_send` outside such a function reverts with `NotCharging`, and a `chargesFees` function calling another reverts with `NestedCharge`.

`_send(opType, opCommand, message)` sends with the defaults:

| Parameter | Default | When to change it |
|---|---|---|
| TEEs | one, from `getRandomTeeIds(extensionId, 1)`, among the extension's machines in production | to reach the one TEE input was encrypted to, as the direct example's `bid` does, the machines holding a wallet's key, as the wallets example's `signTransaction` does, or to have several machines answer the same instruction; see the last section |
| `cosigners`, `cosignersThreshold` | none, `0` | to require further signers on top of the data providers, such as a wallet's own, which the wallets example passes for its TEEs to check |
| `claimBackAddress` | the caller | the platform describes it as an address that can claim back the fee if the instructions are not executed; FlareTeeManager only records it today |

`_send(teeIds, opType, opCommand, message)` sends to the TEEs you choose, with the other defaults.
For anything else, build the parameters yourself and call `_send(teeIds, params)`.

- The pick depends on the Relay's latest random number.
  So while the extension's machines stay the same, every default send in one voting round goes to the same machine, and anyone can call `getRandomTeeIds` to see which.
- With no machine in production, `quoteFee` and every default send revert with `TooMany()`.
- If any TEE you pass isn't in production right now, the whole send reverts: with `TeeMachineNotAvailable`, or with `TeeNotFound` for an address that isn't a TEE at all.
- While the extension is under an emergency pause, every send reverts with `EmergencyPauseActive`.
- The TEEs you choose must be distinct: a repeat reverts with `DuplicateTee`, since the platform would charge it once and keep the rest.

The price of an instruction is the protocol fee plus the project fee:

- **Protocol fee:** governance sets a fee per op pair, or a default one, which is never 0, and it is charged per TEE.
  `IOperationFees.calculateFeeByTeeIds` computes it.
  The platform keeps everything it is sent: it goes to the current reward epoch's rewards, with no refund on chain.
- **Project fee:** `projectFee`, set by the owner, kept by the contract; 0 by default, so the caller then pays exactly the protocol fee.
- `quoteFee(opType, opCommand)` returns the price for one TEE.
  Callers pay at least that; the contract keeps any excess.
  A command sent to several TEEs needs its own quote function.

| A call that pays | Does |
|---|---|
| less than the price of its instructions | reverts with `InsufficientFee(price, available)` at the first `_send` it can't pay for |
| the price or more | forwards exactly the protocol fee per instruction, and keeps the project fees and any excess |

The owner can change the project fee at any time; telling callers about it is the owner's to do.
Governance can change the protocol fee too.
A call paying a quote the price has since risen above reverts with `InsufficientFee`; whatever a call pays above the price stays with the contract, a margin or a price that fell included.

## 4. The owners

The deployer's key ends up owning two things, and the second matters more.

- **The contract.** The deployer owns it, and with it the project fee, its withdrawal, and binding the extension id.
  - `setProjectFee(fee)` changes the fee and emits `ProjectFeeSet`.
  - `withdraw(to)` sends the contract's whole balance, the collected project fees and any excess callers paid, to `to`.
    The owner decides where they go: `to` can be a contract that splits them, or one that runs a fee-distribution protocol.
  - Ownership moves in two steps (OpenZeppelin `Ownable2Step`): `transferOwnership(next)`, then `acceptOwnership()` from `next`.
  - `renounceOwnership()` leaves the contract without an owner for good: nobody can then change the project fee or withdraw.
    Unless the application wants exactly that, set the project fee to 0 and withdraw first.
- **The extension, in the registry.** Whoever called `ExtensionManager.register` owns it, and `fce deploy` registers with the deployer's key.
  It decides what code the extension's machines may run: it adds versions, any code hash, the test hash included, and disables them.
  It also re-points the extension to other contracts, sets its governance, emergency-pauses it, and allows machine owners.
  So whoever holds it can put machines of its choice into production, and the contract then trusts them.
  It moves in two steps too: `proposeNewOwner(extensionId, next)` on ExtensionManager, then `confirmOwnership(extensionId)` from `next`.
  `next` can be a multisig or another contract, but must be on the platform's owner allowlist, unless governance allows every owner.
  Unlike the contract's, this ownership can't be renounced.
- Secure the deployer's key, or move both, for example to a multisig, before production; the template's contract carries a `NOTE` saying so.

## 5. Lifecycle

| Step | How | Notes |
|---|---|---|
| deploy | `Deploy.s.sol` | writes `{"instructionSender": "0x…"}` to `DEPLOY_OUTPUT`, with `stateVerifier` too if the extension has one |
| register | `ExtensionManager.register(stateVerifier, instructionSender)` | returns the extension id |
| bind the id | `PostRegister.s.sol`: `setExtensionId(id)` | needed before the first send |
| send | the contract's send functions | callers pay at least `quoteFee` |
| retire a version | `ExtensionManager.disableCodeHashPlatforms(extensionId, codeHash, platforms)`, or `fce disable-version` | for good: pauses its machines in production, and the hash can never be allowed again |

`fce deploy`, the tooling repo's CLI, runs the first three, and keeps the addresses and the extension id in the extension's `deployments/<chain>.json`:

```bash
fce deploy -chain coston2 -account <name> ./template
```

It runs the scripts with these variables; to run them by hand, add your usual forge signer options:

```bash
cd template/contracts
DEPLOY_OUTPUT=deployments/out.json forge script script/Deploy.s.sol:Deploy --rpc-url "$RPC_URL" --broadcast
INSTRUCTION_SENDER=0x… EXTENSION_ID=… forge script script/PostRegister.s.sol:PostRegister --rpc-url "$RPC_URL" --broadcast
```

- `Deploy.s.sol` finds FlareTeeManager in Flare's contract registry, at the same address on every Flare network; `FLARE_TEE_MANAGER` overrides it, and a chain without the registry, such as anvil, needs it.
- `DEPLOY_OUTPUT` must be under `contracts/deployments/`, scratch space git ignores: forge may write only there, and creates only that directory itself.
- `setExtensionId(id)` binds the contract to its extension, once.
  Only the owner may call it, and only with an id whose registered instructions sender is this contract.
  Anyone may register an extension naming the contract; binding theirs would hand the contract their machines.
  `fce deploy` runs `PostRegister.s.sol` every time, with `STATE_VERIFIER` too if there is one, so running it again must change nothing.
- **Re-pointing:** `ExtensionManager.setExtensionContracts(extensionId, stateVerifier, instructionSender)` moves the extension to a new contract version and keeps its id.
  The new contract needs its own `setExtensionId`, and the old one can no longer send.
  A new state verifier checks the machines already running too: each must report a state it accepts, or fail its next availability check.
- Anything a contract stores about a TEE's identity, such as its address, has to be written after the TEE is registered, and again whenever it relaunches with a new key.
  The template stores none.

## 6. Testing

```bash
make test                                  # Go tests, then forge test for sol/ and every extension
cd sol && forge test                       # the shared base alone
cd template/contracts && forge test        # the template's contract alone
```

`forge lint` builds without bytecode into the project's `out/` and `cache/`: afterwards `forge build` keeps those artifacts, `forge test` finds no tests and still passes, and `forge script` fails.
Run `forge clean` after a `forge lint` of your own; `make lint` lints in a throwaway directory, and `make test` runs `forge test --force`.

- The tests run against `MockFlareTeeManager`, imported as `fcc-extension-scaffold/test/MockFlareTeeManager.sol`.
  Like the facets the contract calls, it charges a fee per TEE, reverts with `FeeTooLow` below it, charges a repeated TEE once, keeps everything it is sent, lets only the registered sender send, and reverts with `TooMany()` when asked for more TEEs than are in production (`setTees` sets them).
  It refuses an empty op type, op command or message, and a send to a TEE out of production, as the platform does.
  It knows each TEE's extension and status (`setTee`), reverting with `TeeNotFound()` for an unknown one, and the emergency pause flag (`setExtensionEmergencyPaused`).
  It doesn't model system senders or system op types, the emergency pause or TEEs of another extension on a send, or the grace window after an emergency pause.
- **The conformance fixture is the payload vector.**
  The forge test reads `../conformance/my-command.json`, decodes the sender and message from its payload, sends them through `myCommand`, and checks that the contract builds exactly that op pair and payload.
  The Go side generates the fixture, and its handler decodes the same bytes, so neither side can change the layout alone.
  `foundry.toml` grants read access to `../conformance` for this.

## 7. Networks

- The interfaces come from flare-periphery's `coston2/` directory; its `coston/` and `songbird/` copies are identical.
- flare-periphery doesn't ship them for Flare mainnet yet.

## 8. Consuming results

A result lands on the TEE's proxy, signed by the TEE; off chain, read it there.
On chain, `_teeSigner(digest, signature)` checks a TEE's signature: it recovers the signer as the sign port signs, EIP-191 over `digest`, then asks FlareTeeManager that the signer is a production TEE of this extension (`getExtensionId`, `getTeeMachineStatus`) and that the extension isn't emergency-paused (`isExtensionEmergencyPaused`), and reverts otherwise.

- **Sign a message of your own.**
  The extension builds it, opening with a tag of its own, and signs it with `SignTEE`; the contract recomputes it and passes its keccak256 to `_teeSigner`.
  The [external-api example](../examples/external-api/README.md) signs its price this way.
- **Or check tee-node's own signature on the result.**
  tee-node signs every result over `keccak256(abi.encode(bytes32("TEE_ACTION_RESULT"), chainId, h))`, where `h` is the keccak256 of `keccak256(data)`, the id, `keccak256(submissionTag)` and the status, packed; EIP-191, `V` 0 or 1.
  Accept it only with the tag `threshold`, status 1, and an instruction id the contract recorded: tee-proxy picks a direct action's id.
  It is tee-node's format for its proxy, not a published interface, so it may change.
- **Bind the signature to the request, and bound how late it may land.**
  tee-node doesn't remember instructions, so the operator can deliver one again, or hold it back; the external-api example binds its price to the instruction id, and refuses it 10 minutes after the request.
- **Production isn't freshly attested.**
  A machine stays in production after its availability check lapses, until someone pauses it; check `getAvailabilityCheckValidity` too if that matters.
- **A test machine passes too.**
  `_teeSigner` checks neither the platform nor the code hash, so while the test hash is allowed, a test machine's signature passes, though its operator holds its key; see [test machines](assumptions.md#test-machines).
- **Time out pending results.**
  A TEE that restarts in the middle of a long-running job leaves its result pending forever.

## 9. Several TEEs

`_send(teeIds, …)` sends one instruction to every TEE named: one id, and a fee per TEE.
Each machine answers at its own proxy, signed with its own key, and the platform never compares the answers.

- The answers can differ: each machine has its own state, and fetches on its own.
- Every answer is public, so a contract that accepts any single one lets whoever submits choose among them.
  Where one machine must not decide, require answers from several distinct TEEs.
- The [wallets example](../examples/wallets/README.md) sends to every TEE holding a copy of a wallet's one key; each signs the same transaction, so any answer will do.
  A wallet with several keys needs answers from several proxies, combined; the example requires one-key wallets, and can't run on a chain until fce sets up wallets.

## 10. State verifiers

What an extension keeps as state, how it uses it, and keeping it safe are the extension's to decide.
A non-empty `/state` needs a state verifier contract, registered with the extension; the template registers none, so its `/state` stays empty.
At each availability check, the registry asks the verifier about the state the TEE reported.

- Refusing it fails the check, and lets anyone holding the proof suspend the machine.
- A revert makes every availability check of the machine revert, so a verifier never reverts.
- tee-node reports a `stateVersion` of all ones and no bytes when the extension's `/state` fails, and a machine that just booted reports its empty state: the verifier decides what both mean.
- It is told the TEE's id, so it can compare the state with what the contract sent that machine.

The [direct example](../examples/direct/README.md#state)'s contract is one: it checks each machine has applied the admins it set for it.
