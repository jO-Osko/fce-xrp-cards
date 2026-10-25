package app

import (
	"context"
	"errors"
	"math/big"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/flare-foundation/fce-go-scaffold/internal/fakenode"
	"github.com/flare-foundation/fce-go-scaffold/pkg/node"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/structs/wallet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// the fake sign port's one wallet key, which conformance runs with
var (
	walletID = fakenode.WalletID()
	keyID    = fakenode.WalletKeyID
)

// anvil's chain id: a transaction signed for it can be sent to a local anvil
const anvil = 31337

// transfer sends 1 ether on anvil.
func transfer() txFields {
	return txFields{
		ChainID:              big.NewInt(anvil),
		To:                   common.HexToAddress("0x0000000000000000000000000000000000000b0b"),
		Value:                big.NewInt(params.Ether),
		Data:                 []byte{},
		Gas:                  21_000,
		MaxFeePerGas:         big.NewInt(2 * params.GWei),
		MaxPriorityFeePerGas: big.NewInt(params.GWei),
	}
}

// call calls a contract on Coston2, with data and no value.
func call() txFields {
	return txFields{
		ChainID:              big.NewInt(114),
		To:                   common.HexToAddress("0x00000000000000000000000000000000000c0de0"),
		Value:                new(big.Int),
		Data:                 common.FromHex("0xa9059cbb000000000000000000000000000000000000000000000000000000000000b0b00000000000000000000000000000000000000000000000000000000000000064"),
		Gas:                  100_000,
		MaxFeePerGas:         big.NewInt(50 * params.GWei),
		MaxPriorityFeePerGas: new(big.Int),
	}
}

func signPayload(t *testing.T, walletID common.Hash, keyID, nonce uint64, tx txFields) []byte {
	t.Helper()
	b, err := signArgs.Pack(walletID, keyID, nonce, tx)
	require.NoError(t, err)
	return b
}

// instr is an instruction with msg, as the contract sends it: the fake wallet has no cosigners.
func instr(msg []byte) *instruction.DataFixed {
	return &instruction.DataFixed{
		InstructionID:   common.HexToHash("0x1d"),
		TeeID:           fakenode.Address(),
		OPType:          OPTypeWallets.Hash(),
		OPCommand:       OPCommandSignTransaction.Hash(),
		OriginalMessage: msg,
	}
}

// signPort serves the fake sign port and returns a client for it.
func signPort(t *testing.T) *node.Client {
	t.Helper()
	srv := httptest.NewServer(fakenode.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return node.New(port)
}

func decodeTx(t *testing.T, raw []byte) *types.Transaction {
	t.Helper()
	var tx types.Transaction
	require.NoError(t, tx.UnmarshalBinary(raw))
	return &tx
}

func TestSignTransaction(t *testing.T) {
	t.Parallel()
	w := &wallets{keys: signPort(t)}

	for _, f := range []txFields{transfer(), call()} {
		msg := signPayload(t, walletID, keyID, 7, f)
		raw, run, err := w.signTransaction(t.Context(), instr(msg))
		require.NoError(t, err)
		assert.Nil(t, run)

		tx := decodeTx(t, raw)
		assert.Equal(t, uint8(types.DynamicFeeTxType), tx.Type())
		assert.Equal(t, f.ChainID, tx.ChainId())
		assert.Equal(t, uint64(7), tx.Nonce())
		assert.Equal(t, f.To, *tx.To())
		assert.Equal(t, f.Value, tx.Value())
		assert.Equal(t, f.Data, tx.Data())
		assert.Equal(t, f.Gas, tx.Gas())
		assert.Equal(t, f.MaxFeePerGas, tx.GasFeeCap())
		assert.Equal(t, f.MaxPriorityFeePerGas, tx.GasTipCap())
		assert.Empty(t, tx.AccessList())
		from, err := types.Sender(types.LatestSignerForChainID(f.ChainID), tx)
		require.NoError(t, err)
		assert.Equal(t, fakenode.WalletAddress(), from)

		again, _, err := w.signTransaction(t.Context(), instr(msg))
		require.NoError(t, err)
		assert.Equal(t, raw, again, "signing is deterministic")
	}
}

func TestSignTransactionRefused(t *testing.T) {
	t.Parallel()
	w := &wallets{keys: signPort(t)}
	noChain := transfer()
	noChain.ChainID = new(big.Int)
	good := signPayload(t, walletID, keyID, 0, transfer())
	dirty := append([]byte(nil), good...)
	dirty[32+24-1] = 1 // above the uint64 keyId

	tests := []struct {
		name       string
		msg        []byte
		cosigners  []common.Address
		threshold  uint64
		wantErr    error
		wantPublic string
	}{
		{name: "no chain id", msg: signPayload(t, walletID, keyID, 0, noChain), wantErr: errNoChainID},
		{name: "another wallet", msg: signPayload(t, common.HexToHash("0x01"), keyID, 0, transfer()), wantErr: errNoKey},
		{name: "another key", msg: signPayload(t, walletID, keyID+1, 0, transfer()), wantErr: errNoKey},
		{name: "a cosigner the wallet hasn't", msg: good, cosigners: []common.Address{{1}}, threshold: 1, wantErr: errCosigners},
		{name: "a threshold the wallet hasn't", msg: good, threshold: 1, wantErr: errCosigners},
		{name: "trailing bytes", msg: append(good, 0), wantPublic: "invalid payload"},
		{name: "dirty padding", msg: dirty, wantPublic: "invalid payload"},
		{name: "empty", msg: nil, wantPublic: "invalid payload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			i := instr(tt.msg)
			i.Cosigners, i.CosignersThreshold = tt.cosigners, tt.threshold
			out, _, err := w.signTransaction(t.Context(), i)
			assert.Nil(t, out)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.EqualError(t, err, tt.wantPublic)
			}
		})
	}
}

// keyStub answers KeyInfo with info and signs with key, or fails with err.
type keyStub struct {
	info *wallet.IWalletKeyManagerKeyExistence
	key  *big.Int
	err  error
}

func (k keyStub) KeyInfo(context.Context, common.Hash, uint64) (*wallet.IWalletKeyManagerKeyExistence, error) {
	return k.info, k.err
}

func (k keyStub) SignWallet(_ context.Context, _ common.Hash, _ uint64, msg []byte) ([]byte, error) {
	if k.key == nil {
		return nil, errors.New("can not sign")
	}
	prv, err := crypto.ToECDSA(common.LeftPadBytes(k.key.Bytes(), 32))
	if err != nil {
		return nil, err
	}
	return crypto.Sign(crypto.Keccak256(msg), prv)
}

// evmKey is key's info as tee-node answers it.
func evmKey(key *big.Int) *wallet.IWalletKeyManagerKeyExistence {
	prv, err := crypto.ToECDSA(common.LeftPadBytes(key.Bytes(), 32))
	if err != nil {
		panic(err)
	}
	return &wallet.IWalletKeyManagerKeyExistence{
		KeyType:     keyTypeEVM,
		SigningAlgo: algoECDSA,
		PublicKey:   crypto.FromECDSAPub(&prv.PublicKey)[1:],
	}
}

// The node's answers the fake never gives.
func TestSignTransactionKeys(t *testing.T) {
	t.Parallel()
	one, two := big.NewInt(1), big.NewInt(2)
	vrf := evmKey(one)
	vrf.SigningAlgo = pad32("keccak256-secp256k1-vrf")
	xrp := evmKey(one)
	xrp.KeyType, xrp.SigningAlgo = pad32("XRP"), pad32("sha512half-secp256k1-ecdsa")
	shortKey := evmKey(one)
	shortKey.PublicKey = shortKey.PublicKey[:63]

	tests := []struct {
		name       string
		keys       keyStub
		wantErr    error
		wantPublic string
	}{
		{name: "the node fails", keys: keyStub{err: errors.New("500")}, wantPublic: "can't read the wallet key"},
		{name: "a VRF key", keys: keyStub{info: vrf, key: one}, wantErr: errNotEVM},
		{name: "an XRP key", keys: keyStub{info: xrp, key: one}, wantErr: errNotEVM},
		{name: "an unreadable public key", keys: keyStub{info: shortKey, key: one}, wantErr: errUnreadable},
		{name: "signing fails", keys: keyStub{info: evmKey(one)}, wantPublic: "can't sign"},
		{name: "signed by another key", keys: keyStub{info: evmKey(one), key: two}, wantErr: errOtherKey},
	}
	msg := signPayload(t, walletID, keyID, 0, transfer())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := &wallets{keys: tt.keys}
			out, _, err := w.signTransaction(t.Context(), instr(msg))
			assert.Nil(t, out)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.EqualError(t, err, tt.wantPublic)
			}
		})
	}

	w := &wallets{keys: keyStub{info: evmKey(one), key: one}}
	_, _, err := w.signTransaction(t.Context(), instr(msg))
	require.NoError(t, err, "the stub signs when the key matches")
}

func TestSameCosigners(t *testing.T) {
	t.Parallel()
	a, b := common.Address{1}, common.Address{2}
	wallet := wallet.IWalletKeyManagerKeyConfigConstants{Cosigners: []common.Address{a, b}, CosignersThreshold: 2}
	tests := []struct {
		name      string
		cosigners []common.Address
		threshold uint64
		want      bool
	}{
		{"the same", []common.Address{a, b}, 2, true},
		{"in another order", []common.Address{b, a}, 2, true},
		{"one missing", []common.Address{a}, 2, false},
		{"one repeated", []common.Address{a, a}, 2, false},
		{"one more", []common.Address{a, b, {3}}, 2, false},
		{"a lower threshold", []common.Address{a, b}, 1, false},
	}
	for _, tt := range tests {
		i := &instruction.DataFixed{Cosigners: tt.cosigners, CosignersThreshold: tt.threshold}
		assert.Equal(t, tt.want, sameCosigners(i, wallet), tt.name)
	}
}
