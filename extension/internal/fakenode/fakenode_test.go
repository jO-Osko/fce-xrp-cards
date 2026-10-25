package fakenode

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/flare-foundation/go-flare-common/pkg/tee/structs/wallet"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func post(t *testing.T, n *Node, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	n.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(b)))
	return rec
}

func TestSign(t *testing.T) {
	t.Parallel()
	msg := []byte("hello")
	rec := post(t, New(), "/sign", types.SignRequest{Message: msg})
	require.Equal(t, http.StatusOK, rec.Code)

	var res types.SignResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	pub, err := crypto.SigToPub(accounts.TextHash(crypto.Keccak256(msg)), res.Signature)
	require.NoError(t, err)
	assert.Equal(t, Address(), crypto.PubkeyToAddress(*pub))
}

func TestDecrypt(t *testing.T) {
	t.Parallel()
	ct, err := ecies.Encrypt(rand.Reader, PublicKey(), []byte("secret"), nil, nil)
	require.NoError(t, err)
	rec := post(t, New(), "/decrypt", types.DecryptRequest{EncryptedMessage: ct})
	require.Equal(t, http.StatusOK, rec.Code)

	var res types.DecryptResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, "secret", string(res.DecryptedMessage))
}

func TestRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path string
		body       any
	}{
		{"empty sign", "/sign", types.SignRequest{}},
		{"unknown sign field", "/sign", map[string]string{"message": "aGk=", "extra": "x"}},
		{"empty decrypt", "/decrypt", types.DecryptRequest{}},
		{"bad ciphertext", "/decrypt", types.DecryptRequest{EncryptedMessage: []byte("not a ciphertext")}},
		{"unknown result field", "/result", map[string]string{"extra": "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, http.StatusBadRequest, post(t, New(), tt.path, tt.body).Code)
		})
	}
}

// walletPath is path for the fake's wallet key.
func walletPath(route string) string {
	return route + "/" + WalletID().Hex() + "/0"
}

func TestKeyInfo(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	New().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, walletPath("/key-info"), nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var info wallet.IWalletKeyManagerKeyExistence
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	pub, err := crypto.UnmarshalPubkey(append([]byte{4}, info.PublicKey...))
	require.NoError(t, err)
	assert.Equal(t, WalletAddress(), crypto.PubkeyToAddress(*pub))
	assert.NotEqual(t, Address(), WalletAddress())
	assert.Equal(t, "EVM", string(bytes.TrimRight(info.KeyType[:], "\x00")))
	assert.Equal(t, "keccak256-secp256k1-ecdsa", string(bytes.TrimRight(info.SigningAlgo[:], "\x00")))
	assert.Empty(t, info.ConfigConstants.Cosigners)
	assert.Zero(t, info.ConfigConstants.CosignersThreshold)
}

func TestSignWallet(t *testing.T) {
	t.Parallel()
	msg := []byte("hello")
	rec := post(t, New(), walletPath("/sign"), types.SignRequest{Message: msg})
	require.Equal(t, http.StatusOK, rec.Code)

	var res types.SignResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	pub, err := crypto.SigToPub(crypto.Keccak256(msg), res.Signature)
	require.NoError(t, err)
	assert.Equal(t, WalletAddress(), crypto.PubkeyToAddress(*pub))
}

func TestDecryptWallet(t *testing.T) {
	t.Parallel()
	ct, err := ecies.Encrypt(rand.Reader, &ecies.ImportECDSA(walletKey).PublicKey, []byte("secret"), nil, nil)
	require.NoError(t, err)
	rec := post(t, New(), walletPath("/decrypt"), types.DecryptRequest{EncryptedMessage: ct})
	require.Equal(t, http.StatusOK, rec.Code)

	var res types.DecryptResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, "secret", string(res.DecryptedMessage))
}

func TestWalletRefusals(t *testing.T) {
	t.Parallel()
	other := "/" + common.HexToHash("0x01").Hex() + "/0"
	sign := types.SignRequest{Message: []byte("hi")}
	tests := []struct {
		name, path string
		body       any
		code       int
	}{
		{"unknown wallet", "/sign" + other, sign, http.StatusNotFound},
		{"unknown key", "/sign/" + WalletID().Hex() + "/1", sign, http.StatusNotFound},
		{"short wallet id", "/sign/0x01/0", sign, http.StatusBadRequest},
		{"bad key id", "/sign/" + WalletID().Hex() + "/x", sign, http.StatusBadRequest},
		{"unknown sign field", walletPath("/sign"), map[string]string{"message": "aGk=", "extra": "x"}, http.StatusBadRequest},
		{"unknown wallet decrypt", "/decrypt" + other, types.DecryptRequest{EncryptedMessage: []byte("x")}, http.StatusNotFound},
		{"empty decrypt", walletPath("/decrypt"), types.DecryptRequest{}, http.StatusBadRequest},
		{"bad ciphertext", walletPath("/decrypt"), types.DecryptRequest{EncryptedMessage: []byte("x")}, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.code, post(t, New(), tt.path, tt.body).Code)
		})
	}
}

func TestResults(t *testing.T) {
	t.Parallel()
	n := New()
	for _, st := range []uint8{4, 1} {
		r := types.ActionResult{ID: common.HexToHash("0x01"), Status: st}
		require.Equal(t, http.StatusOK, post(t, n, "/result", r).Code)
	}
	got := n.Results()
	require.Len(t, got, 2)
	assert.Equal(t, uint8(4), got[0].Status)
	assert.Equal(t, uint8(1), got[1].Status)
}
