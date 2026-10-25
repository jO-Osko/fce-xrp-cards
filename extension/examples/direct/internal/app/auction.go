package app

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
	"github.com/flare-foundation/fce-go-scaffold/pkg/node"
	"github.com/flare-foundation/fce-go-scaffold/pkg/payload"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
)

// Refusals of START, BID and END.
var (
	errNotStarted = extension.PublicError("round not started", nil)
	errEnded      = extension.PublicError("round ended", nil)
)

// bidTag opens every bid's plaintext, so no other ciphertext to this TEE, such
// as the admin's secrets, counts as a bid.
const bidTag = "AUCTION_BID_V1"

// resultTag opens every result the TEE signs, the contract's RESULT_TAG: it
// keeps the signature from meaning anything else, such as one of tee-node's own.
var resultTag = func() (h common.Hash) {
	copy(h[:], "AUCTION_RESULT_V1")
	return h
}()

// maxBidCiphertext is the contract's MAX_BID_SIZE; a bid's plaintext is under 200 bytes.
const maxBidCiphertext = 256 + eciesOverhead

// amountDigits is a bid amount's width, zero-padded: a ciphertext is public and
// as long as its plaintext, so a free-form amount would show its size.
const amountDigits = 78

var (
	// startArgs is abi.encode(round), as the contract's startRound builds it.
	startArgs = abi.Arguments{{Name: "round", Type: abiType("uint64")}}
	// bidArgs is abi.encode(round, number, bidder, ciphertext), as the contract's bid builds it.
	bidArgs = abi.Arguments{
		{Name: "round", Type: abiType("uint64")},
		{Name: "number", Type: abiType("uint64")},
		{Name: "bidder", Type: abiType("address")},
		{Name: "ciphertext", Type: abiType("bytes")},
	}
	// bidResultArgs is BID's result: abi.encode(round, number, bidder, valid).
	bidResultArgs = abi.Arguments{
		{Name: "round", Type: abiType("uint64")},
		{Name: "number", Type: abiType("uint64")},
		{Name: "bidder", Type: abiType("address")},
		{Name: "valid", Type: abiType("bool")},
	}
	// endArgs is abi.encode(round, bids), as the contract's endRound builds it.
	endArgs = abi.Arguments{
		{Name: "round", Type: abiType("uint64")},
		{Name: "bids", Type: abiType("uint64")},
	}
	// outcomeArgs is a round's outcome, as its signed message commits to it:
	// abi.encode(bids, winner, amount).
	outcomeArgs = abi.Arguments{
		{Name: "bids", Type: abiType("uint64")},
		{Name: "winner", Type: abiType("address")},
		{Name: "amount", Type: abiType("uint256")},
	}
	// resultArgs is END's result: abi.encode(round, bids, winner, amount, signature),
	// what the contract's submitResult takes.
	resultArgs = append(abi.Arguments{{Name: "round", Type: abiType("uint64")}},
		append(slices.Clone(outcomeArgs), abi.Argument{Name: "signature", Type: abiType("bytes")})...)
	// messageArgs is what the TEE signs: abi.encode(resultTag, round, keccak256(outcome)).
	messageArgs = abi.Arguments{
		{Name: "tag", Type: abiType("bytes32")},
		{Name: "round", Type: abiType("uint64")},
		{Name: "outcomeHash", Type: abiType("bytes32")},
	}
)

// bidPlaintext is what a bidder encrypts. The ciphertext is public on chain:
// naming the bidder keeps anyone else from sending a copy as their own, and
// naming the round keeps it from counting in another.
type bidPlaintext struct {
	Tag    string         `json:"tag"`
	Round  uint64         `json:"round"`
	Bidder common.Address `json:"bidder"`
	Amount string         `json:"amount"` // decimal, amountDigits wide: a uint256 doesn't fit a JSON number
}

// round is what the TEE keeps of an auction round: the leading bid, never a
// beaten one, and which bids it has seen, so the end can tell none is missing.
type round struct {
	started bool
	next    uint64              // the lowest bid number not seen yet, from 1
	ahead   map[uint64]struct{} // bid numbers above next, seen already
	leader  leader
	bids    uint64 // the count END named
	result  []byte // END's answer; nil until the round ends
}

// leader is a round's leading bid; amount nil: no valid bid yet.
type leader struct {
	bidder common.Address
	amount *big.Int
	number uint64
}

// beatenBy reports whether a valid bid takes the lead: ties go to the earlier bid.
func (l leader) beatenBy(amount *big.Int, number uint64) bool {
	if l.amount == nil {
		return true
	}
	c := amount.Cmp(l.amount)
	return c > 0 || c == 0 && number < l.number
}

func (r *round) seen(n uint64) bool {
	_, ok := r.ahead[n]
	return n < r.next || ok
}

func (r *round) see(n uint64) {
	if n < r.next {
		return
	}
	r.ahead[n] = struct{}{}
	for {
		if _, ok := r.ahead[r.next]; !ok {
			return
		}
		delete(r.ahead, r.next)
		r.next++
	}
}

// round returns round n, opening its record: its start or a bid may come
// first. st.mu must be held.
func (st *state) round(n uint64) *round {
	r := st.rounds[n]
	if r == nil {
		r = &round{next: 1, ahead: map[uint64]struct{}{}}
		st.rounds[n] = r
	}
	return r
}

// start opens a round. Bids don't wait for it: instructions reach the TEE in
// the order their votes finalize, so a bid can come first. The end does. It
// answers the payload, abi.encode(round).
func (st *state) start(_ context.Context, i *instruction.DataFixed) ([]byte, extension.Run, error) {
	var n uint64
	if err := payload.DecodeABI(startArgs, i.OriginalMessage, &n); err != nil {
		return nil, nil, extension.PublicError("invalid payload", err)
	}
	if n == 0 {
		return nil, nil, extension.PublicError("invalid payload", errors.New("round 0"))
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	st.round(n).started = true
	return i.OriginalMessage, nil, nil
}

// bid counts a bid in its round. Every bid the contract numbers counts as
// seen, valid or not, so a malformed one can't keep the round from ending;
// only a valid one can take the lead. It answers whether the bid is valid,
// never its amount: abi.encode(round, number, bidder, valid).
func (st *state) bid(ctx context.Context, i *instruction.DataFixed) ([]byte, extension.Run, error) {
	var p struct {
		Round      uint64
		Number     uint64
		Bidder     common.Address
		Ciphertext []byte
	}
	if err := payload.DecodeABI(bidArgs, i.OriginalMessage, &p); err != nil {
		return nil, nil, extension.PublicError("invalid payload", err)
	}
	if p.Round == 0 || p.Number == 0 || len(p.Ciphertext) > maxBidCiphertext {
		return nil, nil, extension.PublicError("invalid payload", errors.New("out of range"))
	}
	// anyone paying the fee gets here
	amount, err := st.openBid(ctx, p.Ciphertext, p.Round, p.Bidder)
	if err != nil {
		return nil, nil, err
	}
	out, err := bidResultArgs.Pack(p.Round, p.Number, p.Bidder, amount != nil)
	if err != nil {
		return nil, nil, extension.PublicError("encoding the result", err)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if r := st.rounds[p.Round]; r != nil && r.result != nil {
		if !r.seen(p.Number) {
			return nil, nil, errEnded
		}
		return out, nil, nil // delivered again: counted already
	}
	r := st.round(p.Round)
	r.see(p.Number)
	if amount != nil && r.leader.beatenBy(amount, p.Number) {
		r.leader = leader{bidder: p.Bidder, amount: amount, number: p.Number}
	}
	return out, nil, nil
}

// openBid returns a bid's amount, or nil for an invalid bid: a ciphertext the
// TEE key can't open, or a plaintext that isn't a bid by this bidder for this
// round. Any other failure is an error, leaving the bid unseen: counting it
// invalid would drop a valid bid unnoticed. Its refusals never carry the
// plaintext: they are public.
func (st *state) openBid(ctx context.Context, ciphertext []byte, round uint64, bidder common.Address) (*big.Int, error) {
	plain, err := st.key.Decrypt(ctx, ciphertext)
	if errors.Is(err, node.ErrBadRequest) {
		return nil, nil
	}
	if err != nil {
		return nil, extension.PublicError("decrypting the bid", err)
	}
	var b bidPlaintext
	if err := payload.DecodeJSON(plain, &b); err != nil || b.Tag != bidTag || b.Round != round || b.Bidder != bidder {
		return nil, nil
	}
	amount, ok := parseUint256(b.Amount)
	if !ok || len(b.Amount) != amountDigits || amount.Sign() == 0 {
		return nil, nil
	}
	return amount, nil
}

// end closes a round and names its winner, signed for the contract's
// submitResult. It answers only once it has the start and every bid the
// contract numbered: a bid withheld, lost or still on its way shows as a
// refusal, never as a result without it. It answers
// abi.encode(round, bids, winner, amount, signature), the same every time;
// with no valid bid, the winner and the amount are zero.
func (st *state) end(ctx context.Context, i *instruction.DataFixed) ([]byte, extension.Run, error) {
	var p struct {
		Round uint64
		Bids  uint64
	}
	if err := payload.DecodeABI(endArgs, i.OriginalMessage, &p); err != nil {
		return nil, nil, extension.PublicError("invalid payload", err)
	}

	// only the owner gets here, and the sign port is on loopback: st.mu stays held
	st.mu.Lock()
	defer st.mu.Unlock()
	r := st.rounds[p.Round]
	switch {
	case r == nil || !r.started:
		return nil, nil, errNotStarted
	case r.result != nil && p.Bids == r.bids:
		return r.result, nil, nil
	case r.result != nil:
		return nil, nil, errEnded
	case r.next <= p.Bids:
		return nil, nil, extension.PublicError(fmt.Sprintf("bid %d missing", r.next), nil)
	}

	winner, amount := r.leader.bidder, r.leader.amount
	if amount == nil {
		amount = new(big.Int)
	}
	msg, err := resultMessage(p.Round, p.Bids, winner, amount)
	if err != nil {
		return nil, nil, extension.PublicError("encoding the result", err)
	}
	sig, err := st.key.SignTEE(ctx, msg)
	if err != nil {
		return nil, nil, extension.PublicError("signing the result", err)
	}
	sig[crypto.RecoveryIDOffset] += 27 // the sign port gives V 0 or 1; ecrecover wants 27 or 28
	out, err := resultArgs.Pack(p.Round, p.Bids, winner, amount, sig)
	if err != nil {
		return nil, nil, extension.PublicError("encoding the result", err)
	}
	r.bids, r.result = p.Bids, out
	return out, nil, nil
}

// resultMessage is what the TEE signs for a round's outcome.
func resultMessage(n, bids uint64, winner common.Address, amount *big.Int) ([]byte, error) {
	b, err := outcomeArgs.Pack(bids, winner, amount)
	if err != nil {
		return nil, err
	}
	return messageArgs.Pack(resultTag, n, crypto.Keccak256Hash(b))
}

// parseUint256 parses a uint256 in decimal digits.
func parseUint256(s string) (*big.Int, bool) {
	if s == "" || len(s) > 78 {
		return nil, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil, false
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	return v, ok && v.BitLen() <= 256
}
