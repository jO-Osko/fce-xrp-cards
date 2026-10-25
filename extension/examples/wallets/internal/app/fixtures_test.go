package app

import (
	"flag"
	"testing"

	"github.com/flare-foundation/fce-go-scaffold/internal/golden"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/fce-go-scaffold/conformance"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the conformance fixtures")

// fixturesDir is the extension's conformance directory, relative to this package.
const fixturesDir = "../../conformance"

// fixtures are the golden cases `fce conformance` checks the binary against,
// with the fake sign port's wallet key. Signing is deterministic, so a signed
// transaction is the same every time; the unit tests check what it holds.
func fixtures(t *testing.T) []conformance.Fixture {
	t.Helper()
	w := &wallets{keys: signPort(t)}
	signed := func(msg []byte) conformance.Expected {
		raw, _, err := w.signTransaction(t.Context(), instr(msg))
		require.NoError(t, err)
		return conformance.Expected{Status: 1, Log: "ok", Data: raw}
	}
	refused := func(log string) conformance.Expected {
		return conformance.Expected{Status: 0, Log: "error: " + log}
	}
	noChain := transfer()
	noChain.ChainID.SetUint64(0)
	transfer0 := signPayload(t, walletID, keyID, 0, transfer())
	call0 := signPayload(t, walletID, keyID, 0, call())

	fs := []conformance.Fixture{
		{
			Name: "1-sign-transfer", Description: "signs a transfer of 1 ether on anvil, nonce 0, with the wallet key; sent again, the same bytes",
			Message: transfer0, Result: signed(transfer0),
		},
		{
			Name: "2-sign-call", Description: "signs a contract call on Coston2, with data and no value; nonce 0 too, as each chain counts its own",
			Message: call0, Result: signed(call0),
		},
		{
			Name: "3-sign-without-chain-id", Description: "refuses a transaction without a chain id",
			Message: signPayload(t, walletID, keyID, 0, noChain), Result: refused("chain id required"),
		},
		{
			Name: "4-sign-another-wallet", Description: "refuses a wallet whose key this TEE doesn't hold",
			Message: signPayload(t, common.HexToHash("0x01"), keyID, 0, transfer()), Result: refused("wallet key not on this TEE"),
		},
		{
			Name: "5-sign-another-key", Description: "refuses another key of the wallet, which this TEE doesn't hold",
			Message: signPayload(t, walletID, keyID+1, 0, transfer()), Result: refused("wallet key not on this TEE"),
		},
		{
			Name: "6-sign-not-canonical", Description: "refuses a payload with a trailing byte",
			Message: append(transfer0, 0), Result: refused("invalid payload"),
		},
	}
	for i := range fs {
		fs[i].Channel, fs[i].OPType, fs[i].OPCommand = types.Instruction, OPTypeWallets, OPCommandSignTransaction
	}
	return fs
}

// TestFixtures keeps the committed fixtures in step with fixtures:
// `go test ./examples/wallets/internal/app -update` rewrites them.
func TestFixtures(t *testing.T) {
	t.Parallel()
	require.NoError(t, golden.Check(fixturesDir, fixtures(t), *update))
}
