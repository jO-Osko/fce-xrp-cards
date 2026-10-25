package app

import (
	"bytes"
	"context"
	"flag"
	"testing"

	"github.com/flare-foundation/fce-go-scaffold/internal/golden"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/fce-go-scaffold/conformance"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the conformance fixtures")

// fixturesDir is the extension's conformance directory, relative to this package.
const fixturesDir = "../../conformance"

// Secrets encrypted to the fake sign port's key with fce encrypt. Encryption is
// randomized, so they are constants: regenerating them would change the fixtures.
var (
	ciphertextA = hexutil.MustDecode("0x04412c2196b007bdf819e0f031767f744cc3affe89589e85bbc0c22f85c6324bd95e26dea9d8a8f68a4124d765bdcd6915debb86685d2cf90f3076ab2db6837b2741d89ec274310ebf7152949967a38a090acda9766eb2abd7d900741634ba63e35fb8bf434dbb9b75433d535072dc213105ec3434e94c7849515191795f0e767c0c")
	ciphertextB = hexutil.MustDecode("0x041073337a96808ac267edd60eeb912b51f426f55284404b1dc7398db1f47b43c4b348b6d966a4ce5371eaf9c3139e3849952f742c730f94b8f5370dfd9ac0b460e98fc420dbc3d9f86b93de453749e53ea00e6668cfa8230fc9a3aad88d1ae35dc0c6be3a0b30128d5662a51810f5fe12486d660415a09ca0dc9a99230ac3246b2a")
)

const secretA, secretB = "https://a.example", "https://b.example"

// Bids in round 1, encrypted the same way.
var (
	bidAlice1000 = hexutil.MustDecode("0x041856dafe8e55fbfd2ab4c133705a1e19074f694737ab3cf6a01f89ab9915323efb960d2be48647c9372b7558a7301e118646899271ed6b62c4308dc2232561f03618f3a2088cc0c03d7599bd8fb54a829d5d4b64360b8fbdb3b74848d736ab210c07c34cf62ac636c3d688fb2096b4e895d4f3184f5abe3c4efea5c9c6a0af5d8b89572c339904f70df6d72606c8ce7b6223a6d727450ebde1b4a8ccd1fae7d331b7dfd2be8acd00c26f6a37b9ca99961c933479696487f3ef8c7f909eb52ffafcf784e85a02e3a407eb202bd9c872e5c5ba0f5e8b173a1a669405af3953beaf574c59f98b4213fc01d87c9489c754c2288943c0edccef41218e1b99f68c92d8aeb6b27cc0dbb61124f564529ad55b8531ef3baa318e59acb8083f52497df81e3de7")
	bidBob1500   = hexutil.MustDecode("0x04e73deccbf5d2592ce9bf82b3de549b1ee9465c5575a899203f39017c5a2829a190af64f38400380bb1996e0abecdf66b442d8a77e572bb15898344b4f64e8bdd34aa7d599007d76dc20ea5dc571d3d595ff74ca51856837f6da24729d14a31147dc447ca9035f3a229d879d4371266697dce68f4fce7fe0223c2f008ef7b5a0207126dea8d1b10195ec2f2348b61122f358840e93cc755b7295e05bc8055b34878bc8556b054fd7d485eb02bee2a59121992c9f38164e1f8072760468b1a7a6e4370ecb34fc259217012e153c2526529ec32746d0a8f4113e18c0d5d91b2be9b7205235d40880b349826a06d48d491f33cbcc469a4a1ebcf522f0f7e160bad68771d4e9981f7d2634d4b77a46c74f0d41b639b4685a45b4252dac676c3214cf074c9")
	bidAlice2000 = hexutil.MustDecode("0x04fb7dcff0fc17aef6bfe660f874d43908ed438f07ec7b9ac3414c9a8746bba276eee3b95be0c94f7693fc956acfa5e34b38bbc103bbf28874aa98df9dc7d25b33aad39d8525b50505d072450a559e32b7fdd69e5a68a69440c6ced5e59016d4e1f4f17ad733412a8aeaa54a680934dd559a4efef186bfdd6a7dac9a6b444c81b656cfbc40c8eff07c70fb7200b68ef29e1fd7ab7c91ab222b5d473b542ba6343b968fca25add9ec8e4d587c2df243caf565efa1d0008887875135a870b376714aeee9ef851c784a95963d81588fae01d949714fda43d3210cda98c9acc17d018476778675f704346297342541e1b8a9c480286581d6c6df7fef0e7c96222cb61ea5e19e0c1eee5903ba31a9868554724903cddbfd67b8cb3e72183d5503b0d2490693")
)

const (
	plainAlice1000 = `{"tag":"AUCTION_BID_V1","round":1,"bidder":"0x00000000000000000000000000000000000A11cE","amount":"000000000000000000000000000000000000000000000000000000000000000000000000001000"}`
	plainBob1500   = `{"tag":"AUCTION_BID_V1","round":1,"bidder":"0x0000000000000000000000000000000000000B0b","amount":"000000000000000000000000000000000000000000000000000000000000000000000000001500"}`
	plainAlice2000 = `{"tag":"AUCTION_BID_V1","round":1,"bidder":"0x00000000000000000000000000000000000A11cE","amount":"000000000000000000000000000000000000000000000000000000000000000000000000002000"}`
)

func TestCiphertexts(t *testing.T) {
	t.Parallel()
	n := signPort(t)
	for ct, want := range map[*[]byte]string{
		&ciphertextA: secretA, &ciphertextB: secretB,
		&bidAlice1000: plainAlice1000, &bidBob1500: plainBob1500, &bidAlice2000: plainAlice2000,
	} {
		got, err := n.Decrypt(context.Background(), *ct)
		require.NoError(t, err)
		assert.Equal(t, want, string(got))
	}
}

// fixtures are the golden cases `fce conformance` checks the binary against.
// They run in name order in one process, so each relies on the ones before.
func fixtures(t *testing.T) []conformance.Fixture {
	t.Helper()
	const name = "api-key"
	provisionA := provisionPayload(t, adminKey, tee, name, ciphertextA)
	setAdminMsg := setAdminPayload(t, 1, admin)
	tampered := bytes.Clone(ciphertextA)
	tampered[len(tampered)-1] ^= 1
	return []conformance.Fixture{
		{
			Name: "01-provision-without-admin", Description: "refuses admin actions until the owner sets the admin",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandProvision,
			Message: provisionA, Result: conformance.Expected{Status: 0, Log: "error: no admin"},
		},
		{
			Name: "02-set-admin", Description: "sets the admin, as the contract's first setAdmin sends it; sent again, it is refused, as an instruction delivered again",
			Channel: types.Instruction, OPType: OPTypeSecrets, OPCommand: OPCommandSetAdmin,
			Message: setAdminMsg, Result: conformance.Expected{Status: 1, Log: "ok", Data: setAdminMsg},
			Again: &conformance.Expected{Status: 0, Log: "error: nonce not above the last", Data: []byte{}},
		},
		{
			Name: "03-provision", Description: "stores a secret, answering only its name; sent again, it succeeds and changes nothing",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandProvision,
			Message: provisionA, Result: conformance.Expected{Status: 1, Log: "ok", Data: []byte(`{"name":"api-key"}`)},
		},
		{
			Name: "04-provision-another-value", Description: "refuses to overwrite a provisioned secret",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandProvision,
			Message: provisionPayload(t, adminKey, tee, name, ciphertextB), Result: conformance.Expected{Status: 0, Log: "error: provisioned with another value"},
		},
		{
			Name: "05-rotate", Description: "replaces a secret; sent again, it is refused as a replay",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandRotate,
			Message: rotatePayload(t, adminKey, tee, 1, name, ciphertextB),
			Result:  conformance.Expected{Status: 1, Log: "ok", Data: []byte(`{"name":"api-key","counter":1}`)},
			Again:   &conformance.Expected{Status: 0, Log: "error: counter not above the last", Data: []byte{}},
		},
		{
			Name: "06-rotate-not-provisioned", Description: "refuses to rotate a secret never provisioned",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandRotate,
			Message: rotatePayload(t, adminKey, tee, 2, "other", ciphertextB), Result: conformance.Expected{Status: 0, Log: "error: not provisioned"},
		},
		{
			Name: "07-rotate-another-tee", Description: "refuses a payload signed for another TEE",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandRotate,
			Message: rotatePayload(t, adminKey, other, 3, name, ciphertextA), Result: conformance.Expected{Status: 0, Log: "error: not for this TEE"},
		},
		{
			Name: "08-rotate-not-admin", Description: "refuses a payload someone else signed",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandRotate,
			Message: rotatePayload(t, otherKey, tee, 3, name, ciphertextA), Result: conformance.Expected{Status: 0, Log: "error: not signed by the admin"},
		},
		{
			Name: "09-provision-replayed-after-rotate", Description: "03-provision again, after 05-rotate: refused, so the replay changes nothing",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandProvision,
			Message: provisionA, Result: conformance.Expected{Status: 0, Log: "error: provisioned with another value"},
		},
		{
			Name: "10-provision-not-decryptable", Description: "refuses a ciphertext the TEE key can't decrypt",
			Channel: types.Direct, OPType: OPTypeSecrets, OPCommand: OPCommandProvision,
			Message: provisionPayload(t, adminKey, tee, "other", tampered), Result: conformance.Expected{Status: 0, Log: "error: can't decrypt"},
		},
		{
			Name: "11-set-admin-older", Description: "refuses a setAdmin numbered at or below the last applied, as one delivered after a newer one",
			Channel: types.Instruction, OPType: OPTypeSecrets, OPCommand: OPCommandSetAdmin,
			Message: setAdminPayload(t, 1, other), Result: conformance.Expected{Status: 0, Log: "error: nonce not above the last"},
		},
		{
			Name: "12-bid-before-start", Description: "counts alice's bid, the contract's first in round 1, though the round's start hasn't arrived: instructions arrive in the order their votes finalize; answers only whether it is valid; sent again, the same answer",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandBid,
			Message: bidPayload(t, 1, 1, alice, bidAlice1000), Result: conformance.Expected{Status: 1, Log: "ok", Data: bidResult(t, 1, 1, alice, true)},
		},
		{
			Name: "13-start", Description: "opens round 1, as the contract's first startRound sends it; sent again, the same answer",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandStart,
			Message: startPayload(t, 1), Result: conformance.Expected{Status: 1, Log: "ok", Data: startPayload(t, 1)},
		},
		{
			Name: "14-bid", Description: "counts bob's bid, which takes the lead",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandBid,
			Message: bidPayload(t, 1, 2, bob, bidBob1500), Result: conformance.Expected{Status: 1, Log: "ok", Data: bidResult(t, 1, 2, bob, true)},
		},
		{
			Name: "15-bid-copied", Description: "counts alice's ciphertext, public on chain, bid by bob, as invalid",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandBid,
			Message: bidPayload(t, 1, 3, bob, bidAlice1000), Result: conformance.Expected{Status: 1, Log: "ok", Data: bidResult(t, 1, 3, bob, false)},
		},
		{
			Name: "16-bid-not-a-bid", Description: "counts a ciphertext to this TEE that isn't a bid, such as the admin's secret, as invalid",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandBid,
			Message: bidPayload(t, 1, 4, alice, ciphertextA), Result: conformance.Expected{Status: 1, Log: "ok", Data: bidResult(t, 1, 4, alice, false)},
		},
		{
			Name: "17-end-bid-missing", Description: "refuses to end round 1 without bid 5, as one withheld, lost or still on its way",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandEnd,
			Message: endPayload(t, 1, 5), Result: conformance.Expected{Status: 0, Log: "error: bid 5 missing"},
		},
		{
			Name: "18-bid-raised", Description: "counts alice's higher bid, which takes the lead",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandBid,
			Message: bidPayload(t, 1, 5, alice, bidAlice2000), Result: conformance.Expected{Status: 1, Log: "ok", Data: bidResult(t, 1, 5, alice, true)},
		},
		{
			Name: "19-end", Description: "ends round 1, naming alice the winner at 2000, signed for the contract; sent again, the same answer",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandEnd,
			Message: endPayload(t, 1, 5), Result: conformance.Expected{Status: 1, Log: "ok", Data: endResult(t, 1, 5, alice, 2000)},
		},
		{
			Name: "20-end-not-started", Description: "refuses to end a round whose start hasn't arrived",
			Channel: types.Instruction, OPType: OPTypeAuction, OPCommand: OPCommandEnd,
			Message: endPayload(t, 2, 0), Result: conformance.Expected{Status: 0, Log: "error: round not started"},
		},
	}
}

// TestFixtures keeps the committed fixtures in step with fixtures:
// `go test ./examples/direct/internal/app -update` rewrites them.
func TestFixtures(t *testing.T) {
	t.Parallel()
	require.NoError(t, golden.Check(fixturesDir, fixtures(t), *update))
}
