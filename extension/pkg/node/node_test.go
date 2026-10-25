package node

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

var testKey, _ = crypto.HexToECDSA("4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318")

// the one wallet key the fake holds
var (
	walletKey, _ = crypto.HexToECDSA("59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d")
	walletID     = common.HexToHash("0x77")
)

const keyID = 3

// nodeSign signs as tee-node v0.0.29's /sign: utils.Sign over keccak256(msg).
func nodeSign(msg []byte) ([]byte, error) {
	return crypto.Sign(accounts.TextHash(crypto.Keccak256(msg)), testKey)
}

// eciesKey is testKey as tee-node v0.0.29's utils.ECDSAPrivKeyToECIES converts it:
// secp256k1 with ECIES_AES128_SHA256.
func eciesKey() *ecies.PrivateKey {
	return ecies.ImportECDSA(testKey)
}

// fakeNode serves tee-node v0.0.29's POST /sign and POST /decrypt, with the same checks.
func fakeNode() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sign", func(w http.ResponseWriter, r *http.Request) {
		var req types.SignRequest
		if !decodeStrict(r, &req) || len(req.Message) == 0 {
			http.Error(w, "message is required", http.StatusBadRequest)
			return
		}
		sig, err := nodeSign(req.Message)
		if err != nil {
			http.Error(w, "can not sign", http.StatusInternalServerError)
			return
		}
		//nolint:errcheck,gosec // test fake; the client checks the response
		json.NewEncoder(w).Encode(types.SignResponse{Message: req.Message, Signature: sig})
	})
	mux.HandleFunc("POST /decrypt", func(w http.ResponseWriter, r *http.Request) {
		var req types.DecryptRequest
		if !decodeStrict(r, &req) || len(req.EncryptedMessage) == 0 {
			http.Error(w, "message is required", http.StatusBadRequest)
			return
		}
		plain, err := eciesKey().Decrypt(req.EncryptedMessage, nil, nil)
		if err != nil {
			http.Error(w, "can not decrypt", http.StatusBadRequest)
			return
		}
		//nolint:errcheck,gosec // test fake; the client checks the response
		json.NewEncoder(w).Encode(types.DecryptResponse{DecryptedMessage: plain})
	})
	mux.HandleFunc("GET /key-info/{w}/{k}", func(w http.ResponseWriter, r *http.Request) {
		if !walletKeyOf(w, r) {
			return
		}
		//nolint:errcheck,gosec // test fake; the client checks the response
		json.NewEncoder(w).Encode(wallet.IWalletKeyManagerKeyExistence{
			WalletId:  walletID,
			KeyId:     keyID,
			PublicKey: crypto.FromECDSAPub(&walletKey.PublicKey)[1:],
			ConfigConstants: wallet.IWalletKeyManagerKeyConfigConstants{
				Cosigners: []common.Address{{1}}, CosignersThreshold: 1,
			},
		})
	})
	mux.HandleFunc("POST /sign/{w}/{k}", func(w http.ResponseWriter, r *http.Request) {
		var req types.SignRequest
		if !decodeStrict(r, &req) {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if !walletKeyOf(w, r) {
			return
		}
		sig, err := crypto.Sign(crypto.Keccak256(req.Message), walletKey)
		if err != nil {
			http.Error(w, "can not sign", http.StatusInternalServerError)
			return
		}
		//nolint:errcheck,gosec // test fake; the client checks the response
		json.NewEncoder(w).Encode(types.SignResponse{Message: req.Message, Signature: sig})
	})
	mux.HandleFunc("POST /decrypt/{w}/{k}", func(w http.ResponseWriter, r *http.Request) {
		var req types.DecryptRequest
		if !decodeStrict(r, &req) || len(req.EncryptedMessage) == 0 {
			http.Error(w, "message is required", http.StatusBadRequest)
			return
		}
		if !walletKeyOf(w, r) {
			return
		}
		plain, err := ecies.ImportECDSA(walletKey).Decrypt(req.EncryptedMessage, nil, nil)
		if err != nil {
			http.Error(w, "can not decrypt", http.StatusBadRequest)
			return
		}
		//nolint:errcheck,gosec // test fake; the client checks the response
		json.NewEncoder(w).Encode(types.DecryptResponse{DecryptedMessage: plain})
	})
	return mux
}

// walletKeyOf parses the ids as tee-node v0.0.29 does, and answers 400 or 404
// unless they name the fake's wallet key.
func walletKeyOf(w http.ResponseWriter, r *http.Request) bool {
	id, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(r.PathValue("w")), "0x"))
	k, kerr := strconv.ParseUint(r.PathValue("k"), 10, 64)
	switch {
	case err != nil || len(id) != 32 || kerr != nil:
		http.Error(w, "invalid id", http.StatusBadRequest)
		return false
	case common.Hash(id) != walletID || k != keyID:
		http.Error(w, "wallet non-existent", http.StatusNotFound)
		return false
	}
	return true
}

func decodeStrict(r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

// startFake serves h on loopback and returns its port.
func startFake(t *testing.T, h http.Handler) int {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	tcp, ok := srv.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	return tcp.Port
}

func TestSignTEE(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, fakeNode()))

	msg := []byte("hello")
	sig, err := c.SignTEE(t.Context(), msg)
	require.NoError(t, err)
	signer, err := recoverSigner(msg, sig)
	require.NoError(t, err)
	assert.Equal(t, crypto.PubkeyToAddress(testKey.PublicKey), signer)
}

func TestSignTEEBadRequest(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, fakeNode()))

	_, err := c.SignTEE(t.Context(), nil)
	require.ErrorIs(t, err, ErrBadRequest)
}

func TestDecrypt(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, fakeNode()))

	ct, err := ecies.Encrypt(rand.Reader, &eciesKey().PublicKey, []byte("secret"), nil, nil)
	require.NoError(t, err)
	plain, err := c.Decrypt(t.Context(), ct)
	require.NoError(t, err)
	assert.Equal(t, "secret", string(plain))
}

func TestDecryptBadRequest(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, fakeNode()))
	other, err := ecies.GenerateKey(rand.Reader, ecies.DefaultCurve, ecies.ECIES_AES128_SHA256)
	require.NoError(t, err)
	wrongKey, err := ecies.Encrypt(rand.Reader, &other.PublicKey, []byte("secret"), nil, nil)
	require.NoError(t, err)

	for _, ct := range [][]byte{nil, []byte("not a ciphertext"), wrongKey} {
		_, err := c.Decrypt(t.Context(), ct)
		require.ErrorIs(t, err, ErrBadRequest)
	}
}

func TestTEEAddress(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, fakeNode()))

	a, err := c.TEEAddress(t.Context())
	require.NoError(t, err)
	assert.Equal(t, crypto.PubkeyToAddress(testKey.PublicKey), a)
}

func TestRecoverSignerAcceptsBothV(t *testing.T) {
	t.Parallel()
	msg := []byte("hello")
	sig, err := nodeSign(msg)
	require.NoError(t, err)
	shifted := append([]byte(nil), sig...)
	shifted[crypto.RecoveryIDOffset] += 27

	for _, s := range [][]byte{sig, shifted} {
		a, err := recoverSigner(msg, s)
		require.NoError(t, err)
		assert.Equal(t, crypto.PubkeyToAddress(testKey.PublicKey), a)
	}
	_, err = recoverSigner(msg, sig[:64])
	require.Error(t, err)
}

// The node's error bodies may echo inputs; errors never carry them.
func TestErrorsHideBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, "secret detail"},
		{"not json", http.StatusOK, "secret detail"},
		{"wrong shape", http.StatusOK, `{"signature":"secret detail"}`},
		{"no signature", http.StatusOK, `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := New(startFake(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				//nolint:errcheck,gosec // test fake
				w.Write([]byte(tt.body))
			})))

			_, err := c.SignTEE(t.Context(), []byte("x"))
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestKeyInfo(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, fakeNode()))

	info, err := c.KeyInfo(t.Context(), walletID, keyID)
	require.NoError(t, err)
	assert.Equal(t, walletID, common.Hash(info.WalletId))
	assert.Equal(t, uint64(keyID), info.KeyId)
	assert.Equal(t, crypto.FromECDSAPub(&walletKey.PublicKey)[1:], info.PublicKey)
	assert.Equal(t, []common.Address{{1}}, info.ConfigConstants.Cosigners)
	assert.Equal(t, uint64(1), info.ConfigConstants.CosignersThreshold)

	_, err = c.KeyInfo(t.Context(), walletID, keyID+1)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSignWallet(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, fakeNode()))

	msg := []byte("hello")
	sig, err := c.SignWallet(t.Context(), walletID, keyID, msg)
	require.NoError(t, err)
	pub, err := crypto.SigToPub(crypto.Keccak256(msg), sig)
	require.NoError(t, err, "V is 0 or 1")
	assert.Equal(t, walletKey.PublicKey, *pub, "keccak256, no prefix")

	_, err = c.SignWallet(t.Context(), common.HexToHash("0x78"), keyID, msg)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestDecryptWallet(t *testing.T) {
	t.Parallel()
	c := New(startFake(t, fakeNode()))

	ct, err := ecies.Encrypt(rand.Reader, &ecies.ImportECDSA(walletKey).PublicKey, []byte("secret"), nil, nil)
	require.NoError(t, err)
	plain, err := c.DecryptWallet(t.Context(), walletID, keyID, ct)
	require.NoError(t, err)
	assert.Equal(t, "secret", string(plain))

	toTEE, err := ecies.Encrypt(rand.Reader, &eciesKey().PublicKey, []byte("secret"), nil, nil)
	require.NoError(t, err)
	_, err = c.DecryptWallet(t.Context(), walletID, keyID, toTEE)
	require.ErrorIs(t, err, ErrBadRequest)
	_, err = c.DecryptWallet(t.Context(), walletID, keyID+1, ct)
	require.ErrorIs(t, err, ErrNotFound)
}
