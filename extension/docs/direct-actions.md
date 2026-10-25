# Direct actions

A direct action reaches the extension only through tee-proxy, never from the chain: no fee, and no data providers' signatures.
In exchange, nobody vouches for it, so the extension decides who may send it and what a replay does.
The [direct example](../examples/direct/README.md) shows both.

## The path

1. A client posts `{"opType", "opCommand", "message"}` to the proxy's `POST /direct`, the op names as bytes32 and the message as hex.
   The proxy accepts it only when its operator enables `/direct`, and then from anyone holding its API key, or from anyone if the key is optional.
   It refuses system op types (`F_…`).
2. The proxy answers the action it queued, with a random id.
3. tee-node takes it from the proxy's direct queue, checks only that its op type isn't a system one and its tag isn't an instruction's, and calls the extension with channel `direct` and tag `submit`.
4. The client reads the signed result at the proxy's `GET /action/result/<id>?submissionTag=submit`, for 30 minutes by default.

Each TEE has its own proxy, so a direct action reaches only the TEE behind the proxy it was posted to.
The [template's README](../template/README.md#my_direct_command) has a sample body.

## In the extension

An op takes direct actions only when the extension registers it for them (in Go, `RegisterDirectHandler`), and each op pair belongs to one channel, never both.
So an op meant for the contract's callers can't be reached by posting to the proxy.
The rules every answer follows are in the [extension contract](extension-contract.md).

## Who sent it

Nothing tells the extension who posted a direct action.
The proxy's API key only opens `/direct`: it isn't forwarded, and everyone holding it shares it.
A tee-proxy fork, or a service in front of `/direct`, can add checks of its own, such as who may post; the extension still decides, by the rules it holds in the TEE.
So the authorization travels in the payload:

- **A signature from a key the extension trusts.**
  In the direct example, the contract's owner sets an admin on-chain, and the extension accepts secrets only when the admin signed them.
  Setting the trusted key on-chain keeps it out of the operator's hands and out of the image.
- **An authorization carried in the payload.**
  tee-node's own `F_POLICY` / `UPDATE_POLICY` direct action works this way: it carries the data providers' signatures over the new policy, so who delivers it doesn't matter.

Either way, sign more than the data:

- the TEE's address (its `teeId`), so a payload signed for one machine is refused by every other;
- the op pair, so a signature for one op can't authorize another.

Where several keys must sign, count distinct signers and accept only low-s signatures, and never use a signature's bytes as an identifier: a signature has a second valid form.

## Replays

The proxy and its operator see every direct action, and so may others on the way; any of them can post it again, at any later time.
So each direct action is one of:

- **Idempotent:** a second send, whenever it comes, changes nothing.
  The direct example's `PROVISION` stores a secret once: the same secret again succeeds, another is refused.
  Sending the same message twice isn't the test; a replay after the state moved on is.
  "Set the value to V" passes the first and fails the second: replayed after a later change, it sets the value back.
- **Protected against replay:** the payload carries what makes a second send fail.
  The direct example's `ROTATE` carries a counter, which must be above the last one accepted from that admin.

Memory is enough to hold the counters, and no clock is needed.
A restarted TEE has a new key, so a new address, which payloads signed before don't name; and the launcher never restarts the extension without tee-node.

Some actions are idempotent because another system refuses the repeat: signing the same transaction twice gives the same transaction, and the chain's nonce lets it through only once.

## Secrets

A direct action passes the proxy as it was posted, so a secret travels encrypted to the TEE's public key, with `fce encrypt`, and only the TEE can read it.
Only that TEE: a ciphertext for one machine is useless to every other, and to the same machine after a reboot, which brings a new key.
Results and logs are public, so the extension never repeats a secret in them.
