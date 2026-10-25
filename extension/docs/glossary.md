# Glossary

The terms these docs use; each term links to where it is covered.
Flare's [FCC overview](https://dev.flare.network/fcc/overview) introduces the platform; its developer guides describe v1 of this scaffold, on `main`.

- [**action**](extension-contract.md#messages): one request tee-node passes to the extension through `POST /action`, an instruction or a direct action.
  It has an id and a submission tag, which its result echoes.
- [**availability check**](assumptions.md#time): the data providers' attestation that a machine is up, runs an allowed TEE version, and reports a state its extension accepts.
  It puts a machine in production, and is valid for one reward epoch; anyone can run it.
- [**channel**](architecture.md#two-ways-in): how an action arrives: as an instruction, from the chain, or as a direct action, through tee-proxy.
  Each op pair belongs to one channel.
- [**code hash**](../REPRODUCIBILITY.md): the hash of an extension's image, which Confidential Space attests.
  The same code always gives the same hash, so anyone can rebuild an image and compare.
- [**Confidential Space**](https://cloud.google.com/confidential-computing/confidential-space/docs/confidential-space-overview): Google Cloud's TEE, where production machines run: a confidential VM that runs the image and attests its code hash.
  A debug image gives its operator root on the VM; a production image doesn't.
- [**conformance**](testing.md#fce): `fce conformance`, which builds an extension and checks it from the outside: its fixtures, then the rules every extension follows.
- [**cosigners**](https://dev.flare.network/fcc/data-providers): signers an instruction can require on top of the data providers, with their own threshold, such as a wallet's.
- [**data providers**](https://dev.flare.network/fcc/data-providers): Flare's weighted set of signers, who relay every instruction to the machines and sign it.
  tee-node accepts an instruction only when they signed it with more than half of the signing policy's weight.
- [**direct action**](direct-actions.md): an action posted to tee-proxy's `POST /direct`, skipping the chain: no fee, and no data providers' signatures.
  Nothing says who sent it, so the extension authorizes it from its payload.
- [**emergency pause**](contracts.md#4-the-owners): a stop the extension's owner sets in the registry: while it lasts, every send to the extension reverts, and `ExtensionBase` refuses its machines' signatures.
- [**entrypoint contract**](contracts.md#2-the-entrypoint-contract): the extension's own Solidity contract, the only sender of its instructions; it inherits `ExtensionBase`.
  The template's is `InstructionSender`.
- [**extension**](architecture.md): an application that runs in a TEE beside tee-node, reached through its entrypoint contract or by direct actions.
  Flare's dev hub calls it a Flare Compute Extension.
- [**extension contract**](extension-contract.md): the wire rules between tee-node and an extension, in any language.
  It is a document, not a smart contract; the smart contract is the entrypoint contract.
- [**extension id**](contracts.md#5-lifecycle): the number the registry gives an extension when it is registered; its entrypoint contract is bound to it once.
- [**`ExtensionBase`**](contracts.md#1-layout): the shared Solidity base, in `sol/`, every entrypoint contract inherits: sending, fees, the owner, and checking a TEE's signature.
- [**FCC**](https://dev.flare.network/fcc/overview): Flare Confidential Compute, Flare's platform for running code in TEEs, driven from the chain.
- [**`fce`**](testing.md#fce): the CLI for extension developers, from the tooling repo, [`fce-tooling`](https://github.com/flare-foundation/fce-tooling): it checks, builds, deploys and runs extensions.
  The file `fce-version` names the versions this scaffold works with.
- [**fees**](contracts.md#3-sending-and-fees): what a caller pays the entrypoint contract for an instruction: the protocol fee, set by Flare's governance and charged per TEE, plus the project fee, set by the contract's owner.
- [**fixture**](extension-contract.md#fixtures): one golden case in an extension's `conformance/` directory: an op's payload, and the result it must give.
- [**FlareTeeManager**](https://dev.flare.network/fcc/reference/IFlareTeeManager): Flare's contract for FCC on chain: it holds the registry, sends instructions, charges fees, and keeps wallets.
  It is a diamond, EIP-2535: one address serving many facets, each with its own interface.
- [**i-know-what-im-doing mode**](testing.md#fce): turns `fce`'s failed recommendations into warnings.
  It is the flag `-i-know-what-im-doing`, or a file `fce-i-know-what-im-doing` in the extension's directory, holding the reason.
- [**image**](architecture.md#the-image): the Docker image a machine runs: the launcher, tee-node and the extension.
  Built with `make image`, so its code hash is reproducible.
- [**instruction**](architecture.md#two-ways-in): an action that starts at the extension's entrypoint contract on chain, paying a fee, and reaches the machines signed by the data providers.
- [**launch policy**](architecture.md#ports): the image's labels that Confidential Space enforces at launch, such as which environment variables the operator may set.
- [**launcher**](architecture.md#the-image): the image's first process: it starts tee-node and the extension under different users, and stops both when either exits.
- [**long-running action**](extension-contract.md#long-running-results): an action answered with status 2, pending, at once; its partial and final results follow through the sign port.
- [**machine**](assumptions.md#a-tee-lives-once): one running copy of an extension's image, with its own TEE key, tee-proxy and state; the docs also call it a TEE.
- [**networks**](https://dev.flare.network/network/overview): Flare is the main network, and Songbird its canary network; Coston and Coston2 are their test networks.
  The docs' examples use Coston2.
- [**op pair**](extension-guide.md#2-add-an-op): what an action asks for: an op type, which groups commands, and an op command, a verb.
  Each is a name of at most 32 bytes; op types starting with `F_` are the system's.
- [**operator**](assumptions.md#the-operator): whoever runs a machine, its network and its tee-proxy.
  It can see, withhold, delay, reorder and repeat actions, but can't read a production TEE's memory.
- [**owners**](contracts.md#4-the-owners): the entrypoint contract's owner, who sets the project fee and withdraws it, the extension's owner in the registry, who decides what code its machines may run, and each machine's owner, who registered it.
- [**pause, suspend**](assumptions.md#time): take a machine out of production.
  The machine's owner can pause it; anyone can pause one whose availability check lapsed, and suspend one whose state its state verifier refused.
- [**payload**](extension-guide.md#2-add-an-op): the bytes an action carries for the extension, in the extension's own format: ABI for instructions, and often JSON for direct actions.
- [**platform**](../template/README.md#deploying): the kind of machine a TEE version is allowed on: `GCP_AMD_SEV` for Confidential Space, `TEST_PLATFORM` for test machines.
- [**production**](contracts.md#8-consuming-results): the status of a machine that receives instructions, and whose signatures `ExtensionBase` accepts.
  An availability check puts a machine there.
- [**registry**](contracts.md#5-lifecycle): FlareTeeManager's record of extensions: their owners, contracts, TEE versions and machines.
- [**Relay**](https://dev.flare.network/network/fsp/weights-and-signing): Flare's contract that stores the data providers' confirmed voting results and signing policies.
  Its latest random number picks the machine for a default send.
- [**result**](extension-contract.md#what-is-signed): the answer to an action, which tee-node signs with the TEE key and posts to the machine's tee-proxy.
  Only its `data`, id, submission tag and status are signed; its log isn't.
- [**reward epoch**](https://dev.flare.network/network/fsp/weights-and-signing): the period one signing policy holds: 3.5 days on Flare and Songbird, 6 hours on Coston and Coston2.
- [**sign port**](extension-contract.md#sign-port-api): tee-node's loopback API for the extension: sign or decrypt with the TEE key or a wallet key, and post results.
  It has no authentication.
- [**signing policy**](https://dev.flare.network/network/fsp/weights-and-signing#signing-policies-and-weight-systems): the data providers and their weights for one reward epoch.
- [**state**](extension-guide.md#5-state): what the extension reports at `GET /state`: bytes in its own encoding, named by a `stateVersion`.
  It lives in memory, and each availability check carries it.
- [**state verifier**](contracts.md#10-state-verifiers): a contract registered with the extension that decides, at each availability check, whether a machine's state is acceptable.
  Without one, only the empty state passes.
- [**status**](extension-contract.md#statuses): a result's number: 0 failed, 1 succeeded, 2 pending, 3 answered too late, 4 to 255 partial.
- [**submission tag**](extension-contract.md#messages): a label on each action and its result: `threshold` for instructions, `submit` for direct actions.
- [**TEE**](https://dev.flare.network/fcc/overview): a trusted execution environment: hardware that runs code isolated, even from its operator, and attests which code it runs.
  The docs also say a TEE for one machine.
- [**TEE info**](extension-guide.md#6-the-tee-key-and-wallet-keys): tee-node's signed description of its machine, served by its tee-proxy, including its public key and the extension's state.
- [**TEE key, teeId**](assumptions.md#a-tee-lives-once): the key tee-node generates in memory at every boot; it signs every result, and users encrypt to it.
  The teeId, the key's address, is the machine's identity, so a restarted machine is a new TEE.
- [**TEE version**](contracts.md#5-lifecycle): a code hash the extension's owner allows on a platform, with `fce add-version`; machines run only allowed versions.
  Disabling one is for good.
- [**tee-node**](architecture.md): Flare's program in the image that holds the TEE key, fetches actions from tee-proxy, checks instructions, calls the extension, and signs every result.
- [**tee-proxy**](architecture.md#two-ways-in): the server outside the TEE, one per machine, run by the operator: it queues actions for tee-node, takes direct actions, and serves results and TEE info.
- [**template**](../template/README.md): `template/`, the minimal extension a fork turns into its own.
- [**test machine**](assumptions.md#test-machines): tee-node in test mode, with `MODE` other than 0, outside any TEE, on `TEST_PLATFORM`.
  Its operator holds its key, so it is for testing only.
- [**voting round**](https://dev.flare.network/network/fsp/weights-and-signing#voting): one cycle of the data providers' voting; a new one starts every 90 seconds.
- [**wallet, wallet key**](../examples/wallets/README.md#wallets): a wallet holds keys tee-node generates and keeps inside TEEs; the extension has them sign or decrypt through the sign port, but never sees them.
  Wallets belong to wallet projects, each with an owner.
