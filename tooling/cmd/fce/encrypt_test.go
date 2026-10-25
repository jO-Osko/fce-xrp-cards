package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/fce-tooling/internal/fakenode"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fake sign port's public key, in the forms -pubkey takes.
func pubkeyForms() []string {
	pub := fakenode.PublicKey()
	x, y := common.BigToHash(pub.X), common.BigToHash(pub.Y)
	xy := hexutil.Encode(append(x.Bytes(), y.Bytes()...))
	return []string{
		`{"x":"` + x.Hex() + `","y":"` + y.Hex() + `"}`,
		xy,
		"0x04" + xy[2:],
	}
}

// decrypt decrypts ct as tee-node's sign port does.
func decrypt(t *testing.T, ct []byte) []byte {
	t.Helper()
	srv := httptest.NewServer(fakenode.New())
	defer srv.Close()
	body, err := json.Marshal(types.DecryptRequest{EncryptedMessage: ct})
	require.NoError(t, err)
	res, err := http.Post(srv.URL+"/decrypt", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { require.NoError(t, res.Body.Close()) }()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var out types.DecryptResponse
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	return out.DecryptedMessage
}

func TestEncrypt(t *testing.T) {
	t.Parallel()
	for _, key := range pubkeyForms() {
		var out bytes.Buffer
		code := encryptCmd([]string{"-pubkey", key, "-tee", fakenode.Address().Hex()}, strings.NewReader("s3cret"), &out)
		require.Equal(t, 0, code, key)
		ct, err := hexutil.Decode(strings.TrimSpace(out.String()))
		require.NoError(t, err)
		assert.Equal(t, "s3cret", string(decrypt(t, ct)), key)
	}
}

func TestEncryptRefuses(t *testing.T) {
	t.Parallel()
	key := pubkeyForms()[1]
	tests := []struct {
		name, pubkey, tee, want string
	}{
		{"another TEE's key", key, "0x00000000000000000000000000000000000000e1", "belongs to TEE"},
		{"not an address", key, "0x01", "not an address"},
		{"not a key", "0x" + strings.Repeat("00", 64), "", "not a secp256k1 public key"},
		{"not hex", "x", "", "decoding"},
		{"unknown field", `{"x":"0x01","y":"0x02","z":1}`, "", "decoding"},
	}
	for _, tt := range tests {
		_, err := encrypt(tt.pubkey, tt.tee, []byte("s"))
		require.ErrorContains(t, err, tt.want, tt.name)
	}
	assert.Equal(t, 2, encryptCmd(nil, strings.NewReader(""), &bytes.Buffer{}), "no -pubkey")
}
