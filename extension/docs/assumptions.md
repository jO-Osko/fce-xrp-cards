# Assumptions

The trust model an extension runs under, and the platform facts to design around.
It describes a production machine, tee-node with `MODE=0` on a Confidential Space production image; a debug image or a [test machine](#test-machines) gives the operator far more.
They hold for tee-node v0.0.29, and build on tee-node's own security model, its `docs/security.md`.
The rules that follow from them are in [rules.md](rules.md).

## Who can do what

| Party | Can | Can't |
|---|---|---|
| Confidential Space | run the image isolated, and attest its hash | — |
| tee-node | track the signing policy, check the data providers' and cosigners' signatures on every instruction, sign every result, hold the TEE key and wallet keys; it trusts the extension, in the same image | — |
| the extension | serve every action tee-node doesn't handle itself; have the TEE key and every wallet key the TEE holds sign or decrypt anything: the sign port has no authentication | read tee-node's memory or environment: it runs as another user |
| the operator | run the machine, its network and its proxy; see, withhold, delay, reorder and deliver again every action; read every result; reboot the machine | read a production TEE's memory or keys; forge an instruction; change the image without changing its hash |
| the data providers | sign any instruction, with a majority of the signing policy's weight; tee-node accepts it | act alone, below that majority |
| cosigners | add their own threshold to an instruction, on top of the data providers' | — |
| a direct action's sender | post any direct action, holding tee-proxy's API key, or without one if the key is optional | be told apart from any other sender: the extension authorizes from the payload |
| a contract's caller | send what the extension's contract lets through, paying the fee | send instructions to the extension other than through its contract |
| an outside service | answer anything, late or never; so the extension cross-checks sources, as the external-api example takes the median of three | — |

So the extension's own code decides everything the TEE signs, and everything it accepts.

## Test machines

- tee-node in test mode, `MODE` other than 0, runs outside a TEE, on any host: it reports a fixed code hash on `TEST_PLATFORM`, whatever the image, and attests nothing.
- Its operator holds its key and reads its memory, so can sign or decrypt anything as that TEE.
- Once the extension's owner allows the test hash, `fce add-version -test`, such machines reach production like any other.
- Then `_teeSigner` accepts their signatures, and the random pick can send them users' instructions, with any input users encrypt to them.
- So keep test versions to an extension real users don't use, or disable the test hash, `fce disable-version -test`, before they arrive; that is for good.

## The operator

- Every action and result passes the operator's proxy.
  It can withhold or delay any of them, and deliver an instruction again, or after a newer one, while its signing policy is the active one or the one before.
  It can post a direct action it has seen again, at any time.
- tee-proxy picks a direct action's id, and can change a result's unsigned fields: `log`, the op names, `additionalResultStatus` and `version`.
- The operator hands tee-node its first signing policy, unsigned; the availability check compares its hash with the chain's.
- Whoever reaches tee-node's port 5500 can re-point it at another proxy, at any time.
  The initial owner, the extension id, the chain id and the governance can be set there once each, unless the launch policy set them first.
  Values set there reach tee-node only: the launcher passes the extension its environment, so an extension reading `CHAIN_ID` or `EXTENSION_ID` sees them only when they were set at launch.
- The operator sets only the variables the image's launch policy lists; setting any other aborts the launch.
- Every outbound call crosses the operator's network: TLS, verified against the CA bundle in the image, is what keeps an answer honest.

## What the operator sees

- Results, partial results and logs are public on the proxy; only `data`, the id, the submission tag and the status are signed.
- Input users encrypt to a TEE's key stays public wherever it was posted, on chain for good; only that TEE's key opens it.
  Posted to the proxy instead, it passes the operator, who can keep a copy.
- On a production image, the extension's stdout and stderr stay in the TEE: Confidential Space sends them out only from a debug image, unless the image's launch policy sets `log_redirect=always`.
- A debug image gives its operator root on the VM: the output, the memory, the keys and the sign port.
  A data provider's verifier admits one only when set to (`ALLOW_TEE_DEBUG`), which its shipped Coston and Coston2 settings aren't; a test machine needs no such setting.

## A TEE lives once

- tee-node generates the TEE key in memory at boot; the TEE's identity, its `teeId`, is the key's address.
  All state lives in memory, the extension's included.
- A machine that stops is gone: Confidential Space reboots the VM, or ends the workload, by its restart policy.
  Either way the next boot has a new key, a new `teeId` and empty state, and can only be registered as a new TEE.
- The launcher never restarts the extension alone: anyone able to crash it could otherwise reset its state, nonces and replay protection included, under the same identity.
- Ciphertexts encrypted to the old key can't be opened any more, and payloads signed for the old `teeId` name a TEE that no longer exists.
- Wallet keys go with the machine, and come back only by a restore onto another TEE.
- Each machine has its own state: instructions go to the machines the contract picks, at random by default, and a new machine starts empty.
- An upgrade is a new image, so a new hash: it is registered as a new TEE version, and runs on new machines, with new identities and empty state.

## Several TEEs

- An instruction sent to several TEEs has one id and a fee per TEE; each machine answers at its own proxy, signed with its own key.
- Every TEE it names must be in production, or the whole send reverts.
- The platform never compares or combines the answers, and they can differ: each machine has its own state, and fetches on its own.
- Every answer is public on its proxy, so a consumer that accepts any single one lets whoever submits choose among them.

## Time

- A machine stays in production after its availability check lapses, until someone pauses it, and the random pick still chooses it.
  A check is valid for one reward epoch: 6 hours on Coston and Coston2, 3.5 days on Songbird and Flare.
- So a machine in production needs its availability check run every period; anyone can run it, and `fce run` does for test machines.
  Once it lapses, anyone can pause the machine, and after a longer lapse it can't be confirmed again.
- Confidential Space documents no guarantee for the TEE's clock.
  An instruction carries its block's timestamp, signed by the data providers; block time is the clock to bound by.
- A validator may set a block's timestamp up to 10 s ahead of its own clock.

## Signing

- `/sign` signs like tee-node itself: EIP-191 over `keccak256(message)`.
  tee-node's own signatures cover a 96-byte preimage, tagged and bound to the chain, so an extension that signs 96 bytes a user chose can be made to sign one of tee-node's, such as the result of any instruction.
- A wallet key signs whatever the extension passes, hashed by its algorithm, with no prefix.
  tee-node checks only an instruction's own cosigners, never the wallet's.
  Those are the contract's choice, so an extension signing with a wallet key checks they are the wallet's own set and threshold, as the wallets example does.
- The sign port signs for any program inside the image: the TEE key can be used there, never taken out.
