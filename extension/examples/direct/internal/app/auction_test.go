package app

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startPayload(t *testing.T, n uint64) []byte {
	t.Helper()
	b, err := startArgs.Pack(n)
	require.NoError(t, err)
	return b
}

func bidPayload(t *testing.T, n, number uint64, bidder common.Address, ciphertext []byte) []byte {
	t.Helper()
	b, err := bidArgs.Pack(n, number, bidder, ciphertext)
	require.NoError(t, err)
	return b
}

func endPayload(t *testing.T, n, bids uint64) []byte {
	t.Helper()
	b, err := endArgs.Pack(n, bids)
	require.NoError(t, err)
	return b
}

// bidPlain is the plaintext a bidder encrypts, the amount zero-padded.
func bidPlain(n uint64, bidder common.Address, amount string) string {
	return rawBidPlain(n, bidder, pad(amount))
}

func rawBidPlain(n uint64, bidder common.Address, amount string) string {
	return `{"tag":"AUCTION_BID_V1","round":` + strconv.FormatUint(n, 10) + `,"bidder":"` + bidder.Hex() + `","amount":"` + amount + `"}`
}

// pad zero-pads a shorter amount to amountDigits.
func pad(amount string) string {
	return strings.Repeat("0", max(amountDigits-len(amount), 0)) + amount
}

func bidResult(t *testing.T, n, number uint64, bidder common.Address, valid bool) []byte {
	t.Helper()
	b, err := bidResultArgs.Pack(n, number, bidder, valid)
	require.NoError(t, err)
	return b
}

// endResult is END's answer, signed by the fake sign port's key.
func endResult(t *testing.T, n, bids uint64, winner common.Address, amount int64) []byte {
	t.Helper()
	msg, err := resultMessage(n, bids, winner, big.NewInt(amount))
	require.NoError(t, err)
	sig, err := signPort(t).SignTEE(context.Background(), msg)
	require.NoError(t, err)
	sig[crypto.RecoveryIDOffset] += 27
	b, err := resultArgs.Pack(n, bids, winner, big.NewInt(amount), sig)
	require.NoError(t, err)
	return b
}

func start(st *state, msg []byte) ([]byte, error) {
	out, _, err := st.start(context.Background(), &instruction.DataFixed{OriginalMessage: msg})
	return out, err
}

func bid(st *state, msg []byte) ([]byte, error) {
	out, _, err := st.bid(context.Background(), &instruction.DataFixed{OriginalMessage: msg})
	return out, err
}

func end(st *state, msg []byte) ([]byte, error) {
	out, _, err := st.end(context.Background(), &instruction.DataFixed{OriginalMessage: msg})
	return out, err
}

// bidAs bids amount as bidder, as the contract's bid numbered number sends it,
// and returns whether the TEE found it valid.
func bidAs(t *testing.T, st *state, n, number uint64, bidder common.Address, amount string) bool {
	t.Helper()
	out, err := bid(st, bidPayload(t, n, number, bidder, encrypt(t, bidPlain(n, bidder, amount))))
	require.NoError(t, err)
	return out[len(out)-1] == 1
}

// outcome decodes END's result, checking the TEE signed it for the contract.
func outcome(t *testing.T, out []byte) (n, bids uint64, winner common.Address, amount *big.Int) {
	t.Helper()
	v, err := resultArgs.Unpack(out)
	require.NoError(t, err)
	n, bids, winner, amount = v[0].(uint64), v[1].(uint64), v[2].(common.Address), v[3].(*big.Int) //nolint:forcetypeassert // the types resultArgs declares
	sig := v[4].([]byte)                                                                           //nolint:forcetypeassert // the type resultArgs declares

	msg, err := resultMessage(n, bids, winner, amount)
	require.NoError(t, err)
	require.Len(t, sig, crypto.SignatureLength)
	assert.Contains(t, []byte{27, 28}, sig[crypto.RecoveryIDOffset], "V as ecrecover takes it")
	sig[crypto.RecoveryIDOffset] -= 27
	pub, err := crypto.SigToPub(accounts.TextHash(crypto.Keccak256(msg)), sig)
	require.NoError(t, err)
	assert.Equal(t, tee, crypto.PubkeyToAddress(*pub))
	return n, bids, winner, amount
}

func TestStart(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t)) // no admin needed
	msg := startPayload(t, 1)
	for range 2 {
		out, err := start(st, msg)
		require.NoError(t, err)
		assert.Equal(t, msg, out, "the same answer every time")
	}
	assert.True(t, st.rounds[1].started)

	for _, msg := range [][]byte{startPayload(t, 0), append(startPayload(t, 2), 0)} {
		_, err := start(st, msg)
		require.EqualError(t, err, "invalid payload")
	}
	assert.Len(t, st.rounds, 1)
}

// The highest valid bid wins at its own amount; a lower bid stands but doesn't lead.
func TestAuction(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t))
	_, err := start(st, startPayload(t, 1))
	require.NoError(t, err)
	assert.True(t, bidAs(t, st, 1, 1, alice, "1000"))
	assert.True(t, bidAs(t, st, 1, 2, bob, "1500"))
	assert.True(t, bidAs(t, st, 1, 3, alice, "1200"))
	assert.Equal(t, leader{bidder: bob, amount: big.NewInt(1500), number: 2}, st.rounds[1].leader, "only the leader is kept")

	out, err := end(st, endPayload(t, 1, 3))
	require.NoError(t, err)
	n, bids, winner, amount := outcome(t, out)
	assert.Equal(t, uint64(1), n)
	assert.Equal(t, uint64(3), bids)
	assert.Equal(t, bob, winner)
	assert.Equal(t, big.NewInt(1500), amount)

	again, err := end(st, endPayload(t, 1, 3))
	require.NoError(t, err)
	assert.Equal(t, out, again, "the same answer every time")

	// another round runs on its own
	_, err = start(st, startPayload(t, 2))
	require.NoError(t, err)
	assert.True(t, bidAs(t, st, 2, 1, alice, "1"))
	out, err = end(st, endPayload(t, 2, 1))
	require.NoError(t, err)
	_, _, winner, amount = outcome(t, out)
	assert.Equal(t, alice, winner)
	assert.Equal(t, big.NewInt(1), amount)
}

// Instructions reach the TEE in the order their votes finalize: bids may come
// before the start, and out of order. The end waits for the start and every bid.
func TestAnyArrivalOrder(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t))
	_, err := end(st, endPayload(t, 1, 0))
	require.ErrorIs(t, err, errNotStarted)

	assert.True(t, bidAs(t, st, 1, 3, bob, "500"))
	assert.True(t, bidAs(t, st, 1, 1, alice, "500"))
	_, err = end(st, endPayload(t, 1, 3))
	require.ErrorIs(t, err, errNotStarted, "bids came, the start didn't")

	_, err = start(st, startPayload(t, 1))
	require.NoError(t, err)
	_, err = end(st, endPayload(t, 1, 3))
	require.EqualError(t, err, "bid 2 missing")
	_, err = end(st, endPayload(t, 1, 4))
	require.EqualError(t, err, "bid 2 missing", "names the first missing")
	assert.Nil(t, st.rounds[1].result, "a refused end changes nothing")

	assert.True(t, bidAs(t, st, 1, 2, bob, "500"))
	out, err := end(st, endPayload(t, 1, 3))
	require.NoError(t, err)
	_, _, winner, amount := outcome(t, out)
	assert.Equal(t, alice, winner, "a tie goes to the lowest bid number, whichever came first")
	assert.Equal(t, big.NewInt(500), amount)
}

// A bid delivered again is counted once, and answered the same.
func TestBidAgain(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t))
	msg := bidPayload(t, 3, 1, alice, encrypt(t, bidPlain(3, alice, "7")))
	first, err := bid(st, msg)
	require.NoError(t, err)
	assert.Equal(t, bidResult(t, 3, 1, alice, true), first, "the result never holds the amount")
	again, err := bid(st, msg)
	require.NoError(t, err)
	assert.Equal(t, first, again)
	assert.Equal(t, uint64(2), st.rounds[3].next)
	assert.Empty(t, st.rounds[3].ahead)
}

func TestEnded(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t))
	_, err := start(st, startPayload(t, 1))
	require.NoError(t, err)
	msg := bidPayload(t, 1, 1, alice, encrypt(t, bidPlain(1, alice, "7")))
	want, err := bid(st, msg)
	require.NoError(t, err)
	out, err := end(st, endPayload(t, 1, 1))
	require.NoError(t, err)

	again, err := bid(st, msg)
	require.NoError(t, err)
	assert.Equal(t, want, again, "a bid counted already, delivered again")
	_, err = bid(st, bidPayload(t, 1, 2, bob, encrypt(t, bidPlain(1, bob, "9"))))
	require.ErrorIs(t, err, errEnded)
	_, err = end(st, endPayload(t, 1, 2))
	require.ErrorIs(t, err, errEnded)

	after, err := end(st, endPayload(t, 1, 1))
	require.NoError(t, err)
	assert.Equal(t, out, after, "nothing moved the result")
}

func TestNoValidBid(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t))
	_, err := start(st, startPayload(t, 1))
	require.NoError(t, err)
	out, err := end(st, endPayload(t, 1, 0))
	require.NoError(t, err)
	_, bids, winner, amount := outcome(t, out)
	assert.Zero(t, bids)
	assert.Equal(t, common.Address{}, winner)
	assert.Zero(t, amount.Sign())

	_, err = start(st, startPayload(t, 2))
	require.NoError(t, err)
	assert.False(t, bidAs(t, st, 2, 1, alice, "0"))
	out, err = end(st, endPayload(t, 2, 1))
	require.NoError(t, err)
	_, _, winner, _ = outcome(t, out)
	assert.Equal(t, common.Address{}, winner)
}

// An invalid bid counts as seen, so the round can end, but never leads.
func TestInvalidBids(t *testing.T) {
	t.Parallel()
	otherCT, err := ecies.Encrypt(rand.Reader, ecies.ImportECDSAPublic(&otherKey.PublicKey), []byte(bidPlain(1, alice, "1")), nil, nil)
	require.NoError(t, err)
	tooBig := "115792089237316195423570985008687907853269984665640564039457584007913129639936" // 2^256

	tests := []struct {
		name       string
		ciphertext []byte
	}{
		{"another bidder's ciphertext", encrypt(t, bidPlain(1, bob, "1"))},
		{"for another round", encrypt(t, bidPlain(2, alice, "1"))},
		{"the admin's secret", encrypt(t, "https://a.example")},
		{"another tag", encrypt(t, `{"tag":"OTHER","round":1,"bidder":"`+alice.Hex()+`","amount":"`+pad("1")+`"}`)},
		{"an unknown field", encrypt(t, `{"tag":"AUCTION_BID_V1","round":1,"bidder":"`+alice.Hex()+`","amount":"`+pad("1")+`","x":1}`)},
		{"the round as a string", encrypt(t, `{"tag":"AUCTION_BID_V1","round":"1","bidder":"`+alice.Hex()+`","amount":"`+pad("1")+`"}`)},
		{"no amount", encrypt(t, rawBidPlain(1, alice, ""))},
		{"an unpadded amount", encrypt(t, rawBidPlain(1, alice, "1"))},
		{"an amount too wide", encrypt(t, rawBidPlain(1, alice, "0"+pad("1")))},
		{"a zero amount", encrypt(t, bidPlain(1, alice, "0"))},
		{"a negative amount", encrypt(t, bidPlain(1, alice, "-1"))},
		{"a hex amount", encrypt(t, bidPlain(1, alice, "0x10"))},
		{"an amount over uint256", encrypt(t, bidPlain(1, alice, tooBig))},
		{"encrypted to another key", otherCT},
		{"empty", []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := newState(tee, signPort(t))
			_, err := start(st, startPayload(t, 1))
			require.NoError(t, err)
			out, err := bid(st, bidPayload(t, 1, 1, alice, tt.ciphertext))
			require.NoError(t, err)
			assert.Equal(t, bidResult(t, 1, 1, alice, false), out)
			assert.Nil(t, st.rounds[1].leader.amount)

			_, err = end(st, endPayload(t, 1, 1))
			require.NoError(t, err, "counted as seen")
		})
	}
}

func TestBidInvalidPayload(t *testing.T) {
	t.Parallel()
	ct := encrypt(t, bidPlain(1, alice, "1"))
	for name, msg := range map[string][]byte{
		"round 0":                     bidPayload(t, 0, 1, alice, ct),
		"bid 0":                       bidPayload(t, 1, 0, alice, ct),
		"a ciphertext over the limit": bidPayload(t, 1, 1, alice, make([]byte, maxBidCiphertext+1)),
		"trailing bytes":              append(bidPayload(t, 1, 1, alice, ct), 0),
	} {
		st := newState(tee, signPort(t))
		_, err := bid(st, msg)
		require.EqualError(t, err, "invalid payload", name)
		assert.Empty(t, st.rounds, name)
	}
	for _, msg := range [][]byte{append(endPayload(t, 1, 0), 0), startPayload(t, 1)} {
		_, err := end(newState(tee, signPort(t)), msg)
		require.EqualError(t, err, "invalid payload")
	}
}

// failingKey is the fake sign port, down for decrypting or for signing.
type failingKey struct {
	next          TEEKey
	decrypt, sign bool
}

var errDown = errors.New("connection refused")

func (k *failingKey) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	if k.decrypt {
		return nil, errDown
	}
	return k.next.Decrypt(ctx, ciphertext)
}

func (k *failingKey) SignTEE(ctx context.Context, msg []byte) ([]byte, error) {
	if k.sign {
		return nil, errDown
	}
	return k.next.SignTEE(ctx, msg)
}

// A sign port that fails leaves the bid unseen: counted as invalid, a valid
// bid would be dropped unnoticed.
func TestBidSignPortDown(t *testing.T) {
	t.Parallel()
	k := &failingKey{next: signPort(t), decrypt: true}
	st := newState(tee, k)
	_, err := start(st, startPayload(t, 1))
	require.NoError(t, err)
	_, err = bid(st, bidPayload(t, 1, 1, alice, encrypt(t, bidPlain(1, alice, "7"))))
	require.EqualError(t, err, "decrypting the bid")
	require.ErrorIs(t, err, errDown)
	_, err = end(st, endPayload(t, 1, 1))
	require.EqualError(t, err, "bid 1 missing")
}

// An end the TEE can't sign changes nothing: sent again, it ends the round.
func TestEndSigningFails(t *testing.T) {
	t.Parallel()
	k := &failingKey{next: signPort(t), sign: true}
	st := newState(tee, k)
	_, err := start(st, startPayload(t, 1))
	require.NoError(t, err)
	_, err = end(st, endPayload(t, 1, 0))
	require.EqualError(t, err, "signing the result")
	assert.Nil(t, st.rounds[1].result)

	k.sign = false
	_, err = end(st, endPayload(t, 1, 0))
	require.NoError(t, err)
}

// The contract's test pins the same bytes: the tag, and the digest of one message.
func TestResultSharedVector(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0x41554354494f4e5f524553554c545f5631000000000000000000000000000000", resultTag.Hex())
	msg, err := resultMessage(1, 3, common.HexToAddress("0xb0b"), big.NewInt(1500))
	require.NoError(t, err)
	assert.Equal(t, "0x6af0090a5f67bf593389af127ad0a3ec8bafe2be4890cdfd0230e4e020ff9ac4", hexutil.Encode(crypto.Keccak256(msg)))
}

// The contract's MAX_BID_SIZE pins the same.
func TestBidSharedVector(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 369, maxBidCiphertext)
}

// Every valid bid's ciphertext has the same length, whatever its amount, and fits.
func TestBidLength(t *testing.T) {
	t.Parallel()
	maxUint := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	small := encrypt(t, bidPlain(1, alice, "1"))
	large := encrypt(t, bidPlain(1, alice, maxUint.String()))
	assert.Len(t, small, len(large))
	longest := encrypt(t, bidPlain(1<<64-1, alice, maxUint.String()))
	assert.LessOrEqual(t, len(longest), maxBidCiphertext)

	st := newState(tee, signPort(t))
	_, err := bid(st, bidPayload(t, 1, 1, alice, large))
	require.NoError(t, err)
	assert.Equal(t, maxUint, st.rounds[1].leader.amount)
}
