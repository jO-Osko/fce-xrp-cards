# Docs

The [glossary](glossary.md) defines the terms these pages use.

In reading order:

| Page | What it covers |
|---|---|
| [Architecture](architecture.md) | how tee-node, the extension and the launcher share one image, and the two ways an action arrives |
| [Assumptions](assumptions.md) | who can do what: the trust model, and the platform facts to design around |
| [Rules](rules.md) | every must and must-not of an extension, each with what enforces it |
| [Extension guide](extension-guide.md) | from the template to your own extension, in Go |
| [Contracts](contracts.md) | the entrypoint contract: sending, fees, the owner, checking a TEE's signature |
| [Direct actions](direct-actions.md) | actions that skip the chain: who sent them, replays, secrets |
| [Testing](testing.md) | every test layer, and what to run before a change lands |
| [Forking](fork.md) | what a fork changes and what it leaves, the optional renames, and merging upstream |
| [Extension contract](extension-contract.md) | the wire rules between tee-node and an extension, in any language, and the conformance fixtures |

Outside `docs/`: [REPRODUCIBILITY.md](../REPRODUCIBILITY.md) on the image's hash, the [template](../template/README.md), and the examples: [direct](../examples/direct/README.md), [external-api](../examples/external-api/README.md) and [wallets](../examples/wallets/README.md).
