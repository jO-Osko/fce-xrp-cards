package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/machinemanager"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/verification"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Coston2 vectors, read 2026-10-12: extension 66988's test machine (its /info,
// its register calldata of 2026-09-18, its attestation result) and an
// availability round of extension 0's keeper (the attester's result, the
// confirmAvailability calldata that used it).
var (
	tee66988   = common.HexToAddress("0xd33948e8401cf5F49C12529e9b9C6E62557A11b8")
	proxy66988 = common.HexToAddress("0x2a3091e070E5DFa41c4f1a99BfcF77adC6b04B9D")
	attester   = common.HexToAddress("0x6f489B4954c65d9EA657ef373C0f4f87652BDA11")
	attesterPx = common.HexToAddress("0xF4E021377420Afe90c1A7D2b8968904946633a64")
)

func readJSON[T any](t *testing.T, name string) *T {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err)
	v := new(T)
	require.NoError(t, json.Unmarshal(b, v))
	return v
}

type metaData interface{ GetAbi() (*abi.ABI, error) }

// calldata unpacks the arguments of a call to the method name of contract md.
func calldata(t *testing.T, md metaData, name, file string) []any {
	t.Helper()
	data := readHex(t, file)
	a, err := md.GetAbi()
	require.NoError(t, err)
	m := a.Methods[name]
	require.Equal(t, m.ID, data[:4])
	args, err := m.Inputs.Unpack(data[4:])
	require.NoError(t, err)
	return args
}

func readHex(t *testing.T, file string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + file)
	require.NoError(t, err)
	data, err := hexutil.Decode(strings.TrimSpace(string(b)))
	require.NoError(t, err)
	return data
}

// packs reports whether calling name of md with args gives the calldata in file.
func packs(t *testing.T, md metaData, name, file string, args ...any) bool {
	t.Helper()
	a, err := md.GetAbi()
	require.NoError(t, err)
	got, err := a.Pack(name, args...)
	require.NoError(t, err)
	return bytes.Equal(readHex(t, file), got)
}

func TestRegistration(t *testing.T) {
	t.Parallel()
	info := readJSON[types.SignedTeeInfoResponse](t, "info-66988.json")
	id, err := TeeID(info.MachineData.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, tee66988, id)
	px, err := ProxyID(info)
	require.NoError(t, err)
	assert.Equal(t, proxy66988, px)

	data, sig, err := Registration(info)
	require.NoError(t, err)
	args := calldata(t, machinemanager.MachineManagerMetaData, "register", "register-66988.hex")
	assert.True(t, packs(t, machinemanager.MachineManagerMetaData, "register", "register-66988.hex", data, sig, px, args[3], args[4]))

	info.DataSignature = info.DataSignature[:64]
	_, _, err = Registration(info)
	require.Error(t, err)
}

func TestVerify(t *testing.T) {
	t.Parallel()
	r := readJSON[types.ActionResponse](t, "attest-66988.json")
	require.NoError(t, Verify(r, 114, tee66988, proxy66988))
	require.ErrorContains(t, Verify(r, 114, attester, proxy66988), "not signed by TEE")
	require.ErrorContains(t, Verify(r, 114, tee66988, attesterPx), "not signed by proxy")
	require.Error(t, Verify(r, 14, tee66988, proxy66988), "bound to the chain")
	r.Result.Status = 0
	require.Error(t, Verify(r, 114, tee66988, proxy66988))
}

func TestProof(t *testing.T) {
	t.Parallel()
	r := readJSON[types.ActionResponse](t, "avail-result.json")
	require.NoError(t, Verify(r, 114, attester, attesterPx))
	p, err := Proof(r)
	require.NoError(t, err)
	assert.True(t, packs(t, verification.VerificationMetaData, "confirmAvailability", "confirm-availability.hex", p))
	assert.Equal(t, tee66988, p.RequestBody.TeeId)

	r.Result.Data = []byte(`{"responseHeader":"0x01"}`)
	_, err = Proof(r)
	require.Error(t, err)
}

func TestProxy(t *testing.T) {
	t.Parallel()
	attest, err := os.ReadFile("testdata/attest-66988.json")
	require.NoError(t, err)
	id := common.HexToHash("0xcbb5dd5030f06faaa70becfd1a382cad09d094a74bf8fb9f1ca14d89e36bbd46")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/action/result/"+id.Hex() && r.URL.Query().Get("submissionTag") == "threshold":
			_, _ = w.Write(attest)
		case r.URL.Path == "/direct" && r.Header.Get("X-API-Key") == "k":
			var d types.DirectInstruction
			if json.NewDecoder(r.Body).Decode(&d) != nil || d.OPType != common.BytesToHash([]byte("T")) || string(d.Message) != "m" {
				http.Error(w, "bad", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(types.Action{Data: types.ActionData{ID: id, Type: types.Direct, SubmissionTag: types.Submit}})
		case r.URL.Path == "/direct":
			http.Error(w, "unauthorized\nmore", http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := New(srv.URL+"/", 5*time.Second)
	ctx := context.Background()

	r, err := p.Result(ctx, id, types.Threshold)
	require.NoError(t, err)
	assert.Equal(t, uint8(1), r.Result.Status)
	_, err = p.Result(ctx, id, types.End)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = p.Result(ctx, common.Hash{1}, types.Threshold)
	require.ErrorIs(t, err, ErrNotFound)

	got, err := p.Direct(ctx, "k", common.BytesToHash([]byte("T")), common.Hash{}, []byte("m"))
	require.NoError(t, err)
	assert.Equal(t, id, got)
	_, err = p.Direct(ctx, "wrong", common.Hash{}, common.Hash{}, nil)
	require.ErrorContains(t, err, "401 Unauthorized: unauthorized")
	require.NotContains(t, err.Error(), "more")
}
