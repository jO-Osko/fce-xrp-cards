package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/flare-foundation/fce-go-scaffold/internal/fakenode"
	"github.com/flare-foundation/fce-go-scaffold/pkg/node"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixed, public test keys: the admin, and someone else.
var (
	adminKey = mustKey("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
	otherKey = mustKey("8a1f9a8f95be41cd7ccb6168179afb4504aefe388d1e14474d32c45c72ce7b7a")
	admin    = crypto.PubkeyToAddress(adminKey.PublicKey)
	other    = crypto.PubkeyToAddress(otherKey.PublicKey)
	// the fake sign port's, which conformance runs with
	tee = fakenode.Address()
	// bidders; they sign nothing, so any address does
	alice = common.HexToAddress("0x00000000000000000000000000000000000a11ce")
	bob   = common.HexToAddress("0x0000000000000000000000000000000000000b0b")
)

func mustKey(hex string) *ecdsa.PrivateKey {
	k, err := crypto.HexToECDSA(hex)
	if err != nil {
		panic(err)
	}
	return k
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

// adminState is a state whose admin is set, decrypting through a fake sign port.
func adminState(t *testing.T) *state {
	t.Helper()
	st := newState(tee, signPort(t))
	setAdmin(t, st, admin)
	return st
}

// encrypt encrypts value to the fake sign port's key, as fce encrypt does.
func encrypt(t *testing.T, value string) []byte {
	t.Helper()
	ct, err := ecies.Encrypt(rand.Reader, fakenode.PublicKey(), []byte(value), nil, nil)
	require.NoError(t, err)
	return ct
}

// sign signs digest as cast wallet sign does: EIP-191, V 27 or 28.
func sign(t *testing.T, key *ecdsa.PrivateKey, digest common.Hash) []byte {
	t.Helper()
	sig, err := crypto.Sign(accounts.TextHash(digest[:]), key)
	require.NoError(t, err)
	sig[crypto.RecoveryIDOffset] += 27
	return sig
}

func setAdminPayload(t *testing.T, nonce uint64, a common.Address) []byte {
	t.Helper()
	b, err := setAdminArgs.Pack(nonce, a)
	require.NoError(t, err)
	return b
}

func provisionPayload(t *testing.T, key *ecdsa.PrivateKey, to common.Address, name string, ciphertext []byte) []byte {
	t.Helper()
	digest, err := provisionDigest(to, name, ciphertext)
	require.NoError(t, err)
	b, err := json.Marshal(provisionMessage{TEE: to, Name: name, Ciphertext: ciphertext, Signature: sign(t, key, digest)})
	require.NoError(t, err)
	return b
}

func rotatePayload(t *testing.T, key *ecdsa.PrivateKey, to common.Address, counter uint64, name string, ciphertext []byte) []byte {
	t.Helper()
	digest, err := rotateDigest(to, counter, name, ciphertext)
	require.NoError(t, err)
	b, err := json.Marshal(rotateMessage{TEE: to, Counter: counter, Name: name, Ciphertext: ciphertext, Signature: sign(t, key, digest)})
	require.NoError(t, err)
	return b
}

// setAdmin sets a as the contract's next setAdmin would.
func setAdmin(t *testing.T, st *state, a common.Address) {
	t.Helper()
	_, _, err := st.setAdmin(context.Background(), &instruction.DataFixed{OriginalMessage: setAdminPayload(t, st.nonce+1, a)})
	require.NoError(t, err)
}

func provision(st *state, msg []byte) ([]byte, error) {
	out, _, err := st.provision(context.Background(), &types.DirectInstruction{Message: msg})
	return out, err
}

func rotate(st *state, msg []byte) ([]byte, error) {
	out, _, err := st.rotate(context.Background(), &types.DirectInstruction{Message: msg})
	return out, err
}

func TestSetAdmin(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t))
	call := func(msg []byte) ([]byte, error) {
		out, run, err := st.setAdmin(context.Background(), &instruction.DataFixed{OriginalMessage: msg})
		assert.Nil(t, run)
		return out, err
	}
	payload := setAdminPayload(t, 1, admin)
	out, err := call(payload)
	require.NoError(t, err)
	assert.Equal(t, payload, out)
	assert.Equal(t, admin, st.admin)

	_, err = call(append(payload, 0))
	require.EqualError(t, err, "invalid payload")

	// nonces may skip; an old one, delivered again or late, is refused
	_, err = call(setAdminPayload(t, 3, other))
	require.NoError(t, err)
	for _, msg := range [][]byte{payload, setAdminPayload(t, 2, admin), setAdminPayload(t, 3, admin), setAdminPayload(t, 0, admin)} {
		_, err = call(msg)
		require.ErrorIs(t, err, errOldNonce)
	}
	assert.Equal(t, uint64(3), st.nonce)
	assert.Equal(t, other, st.admin)

	setAdmin(t, st, common.Address{})
	_, err = provision(st, provisionPayload(t, adminKey, tee, "api-key", encrypt(t, "a")))
	require.ErrorIs(t, err, errNoAdmin, "the zero address removes the admin")
}

func TestProvision(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t))
	msg := provisionPayload(t, adminKey, tee, "api-key", encrypt(t, "a"))
	_, err := provision(st, msg)
	require.ErrorIs(t, err, errNoAdmin)

	setAdmin(t, st, admin)
	out, err := provision(st, msg)
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"api-key"}`, string(out), "the result never holds the secret")
	assert.Equal(t, "a", st.secrets["api-key"])

	// idempotent: the replay succeeds and changes nothing
	again, err := provision(st, msg)
	require.NoError(t, err)
	assert.Equal(t, out, again)

	// encryption is randomized: the same secret, encrypted anew, is the same secret
	_, err = provision(st, provisionPayload(t, adminKey, tee, "api-key", encrypt(t, "a")))
	require.NoError(t, err)

	_, err = provision(st, provisionPayload(t, adminKey, tee, "api-key", encrypt(t, "b")))
	require.ErrorIs(t, err, errAnotherValue)
	assert.Equal(t, "a", st.secrets["api-key"])
}

// Idempotent means a replay changes nothing even after the state moved on.
func TestProvisionReplayedAfterRotate(t *testing.T) {
	t.Parallel()
	st := adminState(t)
	msg := provisionPayload(t, adminKey, tee, "api-key", encrypt(t, "a"))
	_, err := provision(st, msg)
	require.NoError(t, err)
	_, err = rotate(st, rotatePayload(t, adminKey, tee, 1, "api-key", encrypt(t, "b")))
	require.NoError(t, err)

	_, err = provision(st, msg)
	require.ErrorIs(t, err, errAnotherValue)
	assert.Equal(t, "b", st.secrets["api-key"])
}

func TestRotate(t *testing.T) {
	t.Parallel()
	st := adminState(t)
	_, err := rotate(st, rotatePayload(t, adminKey, tee, 1, "api-key", encrypt(t, "b")))
	require.ErrorIs(t, err, errNotProvisioned)

	_, err = provision(st, provisionPayload(t, adminKey, tee, "api-key", encrypt(t, "a")))
	require.NoError(t, err)
	toB := rotatePayload(t, adminKey, tee, 1, "api-key", encrypt(t, "b"))
	out, err := rotate(st, toB)
	require.NoError(t, err, "a refused rotation doesn't use up its counter")
	assert.JSONEq(t, `{"name":"api-key","counter":1}`, string(out))

	toC := rotatePayload(t, adminKey, tee, 5, "api-key", encrypt(t, "c"))
	_, err = rotate(st, toC)
	require.NoError(t, err, "counters may skip")

	for _, msg := range [][]byte{toB, toC, rotatePayload(t, adminKey, tee, 4, "api-key", encrypt(t, "b"))} {
		_, err = rotate(st, msg)
		require.ErrorIs(t, err, errOldCounter)
	}
	assert.Equal(t, "c", st.secrets["api-key"])

	// a new admin counts from its own last
	setAdmin(t, st, other)
	_, err = rotate(st, rotatePayload(t, otherKey, tee, 1, "api-key", encrypt(t, "d")))
	require.NoError(t, err)
	setAdmin(t, st, admin)
	_, err = rotate(st, rotatePayload(t, adminKey, tee, 5, "api-key", encrypt(t, "e")))
	require.ErrorIs(t, err, errOldCounter)
}

func TestRefused(t *testing.T) {
	t.Parallel()
	elsewhere := common.HexToAddress("0x00000000000000000000000000000000000000e1")
	ct := encrypt(t, "v")

	// a ROTATE carrying the admin's signature of a PROVISION
	var m rotateMessage
	require.NoError(t, json.Unmarshal(rotatePayload(t, adminKey, tee, 9, "n", ct), &m))
	digest, err := provisionDigest(tee, "n", ct)
	require.NoError(t, err)
	m.Signature = sign(t, adminKey, digest)
	otherOp, err := json.Marshal(m)
	require.NoError(t, err)

	// encrypted to another key
	otherCT, err := ecies.Encrypt(rand.Reader, ecies.ImportECDSAPublic(&otherKey.PublicKey), []byte("v"), nil, nil)
	require.NoError(t, err)

	tests := []struct {
		name string
		msg  []byte
		call func(*state, []byte) ([]byte, error)
		want string
	}{
		{"provision for another TEE", provisionPayload(t, adminKey, elsewhere, "n", ct), provision, "not for this TEE"},
		{"provision not by the admin", provisionPayload(t, otherKey, tee, "n", ct), provision, "not signed by the admin"},
		{"provision encrypted to another key", provisionPayload(t, adminKey, tee, "m", otherCT), provision, "can't decrypt"},
		{"rotate for another TEE", rotatePayload(t, adminKey, elsewhere, 9, "n", ct), rotate, "not for this TEE"},
		{"rotate not by the admin", rotatePayload(t, otherKey, tee, 9, "n", ct), rotate, "not signed by the admin"},
		{"rotate encrypted to another key", rotatePayload(t, adminKey, tee, 9, "n", otherCT), rotate, "can't decrypt"},
		{"signed for another op", otherOp, rotate, "not signed by the admin"},
		{"short signature", []byte(`{"teeId":"` + tee.Hex() + `","name":"n","ciphertext":"0x01","signature":"0x01"}`), provision, "not signed by the admin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			st := adminState(t)
			st.secrets["n"] = "v"
			_, err := tt.call(st, tt.msg)
			require.EqualError(t, err, tt.want)
			assert.Equal(t, map[string]string{"n": "v"}, st.secrets)
		})
	}
}

func TestInvalidPayload(t *testing.T) {
	t.Parallel()
	for _, msg := range [][]byte{
		[]byte(`{"teeId":"` + tee.Hex() + `","name":"n","ciphertext":"0x","signature":"0x","extra":1}`),
		[]byte(`{"teeId":"` + tee.Hex() + `","name":"n","ciphertext":"0x","signature":"0x"} {}`),
		[]byte(`{"teeId":"0x01","name":"n","ciphertext":"0x","signature":"0x"}`),
		provisionPayload(t, adminKey, tee, "", encrypt(t, "v")),
		provisionPayload(t, adminKey, tee, "n", make([]byte, maxCiphertext+1)),
		provisionPayload(t, adminKey, tee, "n", encrypt(t, string(make([]byte, maxValue+1)))),
	} {
		_, err := provision(adminState(t), msg)
		require.EqualError(t, err, "invalid payload", string(msg))
	}
}

// V 0 or 1, as go-ethereum's crypto.Sign gives it, is accepted too.
func TestRecoverSignerV(t *testing.T) {
	t.Parallel()
	digest := crypto.Keccak256Hash([]byte("x"))
	sig := sign(t, adminKey, digest)
	for _, v := range []byte{sig[64], sig[64] - 27} {
		sig[64] = v
		got, err := recoverSigner(digest, sig)
		require.NoError(t, err)
		assert.Equal(t, admin, got)
	}
}

func TestReport(t *testing.T) {
	t.Parallel()
	st := newState(tee, signPort(t))
	version, b, err := st.report(context.Background())
	require.NoError(t, err)
	assert.Equal(t, stateVersion, version)
	assert.Equal(t, setAdminPayload(t, 0, common.Address{}), b, "(0, 0) before any setAdmin")

	setAdmin(t, st, admin)
	_, b, err = st.report(context.Background())
	require.NoError(t, err)
	assert.Equal(t, setAdminPayload(t, 1, admin), b)
}

// The contract's test pins the same bytes: the version's name and one encoded state.
func TestStateSharedVector(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0x534543524554535f53544154455f563100000000000000000000000000000000", stateVersion.Hex())
	b := setAdminPayload(t, 7, common.HexToAddress("0xa1"))
	assert.Equal(t, "0x000000000000000000000000000000000000000000000000000000000000000700000000000000000000000000000000000000000000000000000000000000a1", hexutil.Encode(b))
}
