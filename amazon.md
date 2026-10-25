# The Credit Server on AWS: TEEs, SEV-SNP and the trust that makes it fly

A credit card asks people to trust whoever holds the books.
This card asks them to trust code instead: the Credit Server is a **Flare Confidential Compute (FCC) extension**, a program that runs inside a **trusted execution environment (TEE)**, where even the machine's operator can neither read its memory nor change what it runs.
This document explains why the TEE is the core of the design, and why running those TEEs on **AWS, on AMD SEV-SNP instances**, is what turns a working demo into something card issuers, banks and their auditors can sign off on.

The short version:

- **The TEE makes the Credit Server's code the policy.** Positions, limits and card payments are decided by an image whose **code hash** anyone can rebuild and check, and every result is signed by a key that never leaves the TEE.
- **FCC makes that verifiable on chain.** The extension's owner allows a code hash as a **TEE version**; a machine only reaches **production** after it attests that hash and passes the data providers' **availability check**; contracts accept only signatures from machines in production.
- **AWS makes it acceptable to the card world.** AMD's silicon proves what runs, AWS's certificate chain proves where it runs, and AWS's compliance programmes (PCI DSS among them) are already accepted by the issuers, processors and banks a card has to work with.

Each layer covers what the others can't: AMD proves the code, AWS vouches for the infrastructure, and Flare enforces on chain which code may act.

> **Where this stands.** FCC's production machines run today on Google Cloud's Confidential Space, the platform `GCP_AMD_SEV`, and this repository's demo runs the Credit Server as a local mock TEE (the real extension is in `extension/`, see the playbook's Part D).
> AWS SEV-SNP is not an FCC platform yet.
> This document makes the case for adding it, and lists what that takes in [What it takes](#what-it-takes).

## 1. Why the card needs a TEE

The Credit Server is the card's brain. For every cardholder it keeps, in the `CARD` ledger:

- the **XRP collateral** credited from XRPL (`CREDIT_DEPOSIT`), what is free and what is locked;
- the **card debt**, reserved at the terminal in under a second (`RESERVE`) against a **75% LTV** cap, and accruing 12% a year;
- every **card payment**, the merchant, the amount, the time;
- the **lending pool**: LP deposits and shares (`LP_DEPOSIT`, `LP_REDEEM`), the borrow index, the TEE's cut, what merchants and the treasury are owed;
- the **daily settlement** (`SETTLE_DAILY`, `SETTLE_CONFIRM`), and the hand-off to XLS-66 loans (`CONVERT_REQUEST`, `CREDIT_RLUSD_REPAYMENT`).

Run that on an ordinary server and its operator is the bank: it can read every cardholder's spending, raise one user's limit, lock another's collateral, or skim the pool, and nobody outside can tell.
The usual answer is regulation and audits after the fact.

A TEE gives a stronger one, before the fact:

- **Confidential.** The extension's memory is encrypted by the CPU. On a production machine the operator sees neither the ledger nor the keys; card activity never touches a public chain.
- **Integrity of code.** The TEE attests the hash of the image it booted. If the code changes, the hash changes, and the machine is no longer one the extension allows.
- **Keys that can't be taken.** tee-node generates the **TEE key** in memory at boot; its address is the machine's identity, the **teeId**. It signs every result, and users can encrypt to it. Through the **sign port** it can also hold **wallet keys**, so an XRPL key that signs repayments or pays out LP withdrawals can live inside the TEE and nowhere else.

That last point is where the demo has to go next.
Today the watcher holds the Credit Server's XRPL key for the repayment accounts, and the LP deposit, pool and issuer keys (README, Current Limits).
In an FCC extension they become wallet keys inside the TEE, used only for what the extension's code allows: the agreed `LoanSet`, the tagged repayment of exactly its principal, `LoanPay` for amounts the user funded, payouts the pool owes.
The operator runs the machine but can't spend a cent.

## 2. What FCC adds: the code is the policy

A TEE alone proves what runs on one machine. **FCC ties that proof to a chain**, so anyone, and any contract, can rely on it:

1. **A reproducible image.** The Credit Server, tee-node and the launcher build into one image with `make image`. The build is reproducible: the same commit gives the same **code hash** on any machine, so an auditor rebuilds it from source and compares (`extension/REPRODUCIBILITY.md`).
2. **TEE versions in the registry.** The extension's owner allows that code hash on a **platform** with `fce add-version`. **FlareTeeManager**'s registry records it. Code the owner hasn't allowed can't join, and a version disabled is disabled for good.
3. **Attested registration.** A machine registers by attesting the chain's challenge from inside the TEE: the platform's report binds its code hash to its TEE key.
4. **The availability check.** Flare's **data providers**, the weighted set that signs every instruction, check that the machine is up, runs an allowed TEE version, and reports a state its extension accepts. That puts it in **production**, for one reward epoch at a time.
5. **Signed results, checked on chain.** Every result is signed by the TEE key. `ExtensionBase` accepts signatures only from machines in production, so a contract that consumes the Credit Server's answers (a settlement, an LP payout) knows they came from allowed code in a TEE.

The two ways in match the card's two speeds:

- **Direct actions** for the card network's hot path: `RESERVE` at the terminal, `GET_POSITION`, straight to the machine's tee-proxy, with no fee and no block time.
- **Instructions** through the extension's entrypoint contract, `InstructionSender`, for what should leave a trail on chain: `SETTLE_DAILY` and `SETTLE_CONFIRM`, signed by the data providers before tee-node accepts them.

And FCC is honest about the operator. It can still see, withhold, delay, reorder and repeat actions, and reboot the machine (`extension/docs/assumptions.md`).
What it cannot do is read a production TEE's memory or keys, forge an instruction, or change the image without changing its hash.
So the design is built for exactly that: idempotent transaction hashes, nonces on settlement, reverts instead of silent failures.

## 3. Why AWS, and why SEV-SNP

### AMD SEV-SNP: the hardware root

**SEV-SNP** (Secure Encrypted Virtualization, Secure Nested Paging) runs a whole VM as the TEE.
The AMD secure processor encrypts the guest's memory with a key the host never sees, and SNP adds integrity: the hypervisor can't remap, replay or corrupt guest pages.
At launch the secure processor **measures** what is loaded into the guest. On request it produces an **attestation report**, signed by a key rooted in AMD, that carries:

- the **launch measurement**: what booted;
- the guest **policy**, including whether debugging is disabled;
- **report data** chosen by the guest: for FCC, the TEE key and the chain's challenge, which binds the report to one teeId and one registration.

It is the same family FCC already uses: Confidential Space on Google Cloud is the `GCP_AMD_SEV` platform.
SEV-SNP on AWS keeps the hardware story identical: same vendor, same memory encryption, same kind of report. So the verifier logic and the threat model carry over.

### AWS: the certificates and the trust behind them

On AWS, SEV-SNP is available on EC2 instances in the M6a, C6a and R6a families.
Their attestation reports are signed with a **VLEK**, a versioned endorsement key that AMD issues to the cloud provider. Its certificate chains to AMD's root through AMD's own keys.
So one report proves two things at once: **AMD silicon** measured this VM, and that silicon sits in **AWS's fleet**.

That certificate chain is half of what AWS brings. The other half is the trust the card's counterparties already place in AWS:

- **Compliance they already accept.** A credit card lives inside PCI DSS, and AWS is a PCI DSS Level 1 service provider, with SOC 1, SOC 2 and ISO 27001 reports and more. An issuer, an acquirer or the underwriting bank behind the XLS-66 loans (README, "Who provides the money") can put the Credit Server through procurement on infrastructure their auditors already know. That is the difference between a pilot and a vendor assessment that takes a year.
- **Physical and operational security.** The AMD guarantees assume nobody attacks the chip itself; AWS's data-centre controls are what make that assumption reasonable.
- **Where the card world already runs.** Processors, card platforms and banks' cloud workloads are heavily on AWS. `RESERVE` has to answer within a card authorization window (and tee-node gives an action 2 seconds), so running the TEE in the same regions as the processor matters.
- **Scale and regions.** Machines can run where the cardholders and the regulators are, and more of them can run when the card grows.

### Two clouds, one extension

FCC does not pick a cloud for a TEE version: the owner allows a code hash **per platform**.
With an AWS platform beside `GCP_AMD_SEV`, the same Credit Server image could be allowed on both, its machines registered on both, and the data providers' availability check would keep both honest.

- **No single provider to stop the card.** An outage, an account suspension or a regional incident at one cloud doesn't take the extension down.
- **Less trust in any one company.** Each machine's report is checked against its own platform's root. A consumer that wants more can require answers from machines on both.

## 4. How the trust chain would work on AWS

Following FCC's registration and availability flow, with AWS SEV-SNP as the platform:

1. **Build.** `make image` builds the Credit Server image reproducibly; its code hash is published with the commit.
2. **Allow.** The extension's owner runs `fce add-version` for that hash on the AWS platform, beside `GCP_AMD_SEV`.
3. **Boot.** The operator starts an SEV-SNP instance from a measured, minimal VM image whose launch measurement covers the container's code hash, with debugging disabled in the guest policy. The launcher starts tee-node and the extension; tee-node generates the TEE key.
4. **Attest.** At registration, tee-node asks the AMD secure processor for a report whose report data commits to its TEE key and the chain's challenge.
5. **Verify.** The verifier checks the report's signature up the chain: VLEK, then AMD's ASK, then AMD's root (ARK). Then it checks that the measurement matches an allowed version, the policy forbids debugging, and the report data matches the TEE key being registered.
6. **Production.** The data providers' availability check puts the machine in production for the reward epoch. From then on `RESERVE`, settlements and payouts are signed by a key that provably lives in that AWS-hosted TEE, running that code.

Every link is checkable by someone who trusts neither the operator nor the card company: AMD's certificates, the registry on chain, the image rebuilt from source.

## What it takes

AWS SEV-SNP is a new FCC platform, not a configuration switch. Adding it needs:

- **A platform in the registry and the verifier.** FlareTeeManager and the data providers' verifier need an AWS SEV-SNP platform: parsing its reports, checking the VLEK chain to AMD's root, and mapping launch measurements to allowed code hashes.
- **A runtime that does what Confidential Space does.** On Google Cloud, Confidential Space measures and launches the container, enforces the image's **launch policy** (which environment variables the operator may set), keeps a production image's logs inside the TEE, and offers no debug access. On AWS that becomes a minimal VM image whose launch measurement pins the kernel, the initial ramdisk and the container's code hash. It also needs the same launch policy and log rules, and an SNP guest policy with debugging off.
- **A measurement anyone can recompute.** As with the image's code hash, the VM's launch measurement has to be reproducible from published firmware and sources, so auditors can tie a report back to code.
- **Extension changes the card needs anyway.** The XRPL keys move into the TEE as wallet keys; reads such as `GET_POSITION` become authenticated; and `CREDIT_DEPOSIT` and the LP calls check XRPL themselves instead of trusting the watcher (README, Current Limits).

## What a TEE does not do

The case is stronger stated precisely:

- **The operator still controls availability.** It can delay or drop actions and reboot the machine. FCC's answer is several machines, on several clouds, and the availability check, not secrecy.
- **A TEE lives once.** The TEE key and all state live in memory; a rebooted machine is a new TEE with a new teeId and empty state. Keeping the ledger across machines needs state that is replicated or restored onto another TEE. The demo leaves that out of scope (README, Out of scope: backups).
- **The roots of trust are AMD and the cloud.** SEV-SNP trusts AMD's keys and microcode, and the hardware is only as safe as the data centre around it. Running on more than one cloud, and keeping firmware up to date, narrows that; it never removes it.
- **The code is only as good as the code.** The TEE proves which code runs, not that it is correct. That is why the image is reproducible, the extension is checked with `fce conformance`, and the version is registered in the open.

## Why this makes it fly

A card on XRPL needs three kinds of trust at once.
Cardholders need privacy and fair limits. LPs need honest pool accounting. Issuers, processors and banks need infrastructure their compliance teams accept.

FCC answers the first two by making the Credit Server's code, not its operator, the authority, and proving on chain which code that is.
AWS SEV-SNP answers the third, and strengthens the first two: AMD-rooted attestation with AWS's certificate chain, on the cloud the card industry already trusts.
Together they let a confidential, verifiable credit card reach production without asking anyone to trust a black box.
