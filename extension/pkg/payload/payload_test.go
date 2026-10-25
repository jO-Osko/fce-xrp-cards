package payload

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func abiType(t string) abi.Type {
	typ, err := abi.NewType(t, "", nil)
	if err != nil {
		panic(err)
	}
	return typ
}

var pairArgs = abi.Arguments{
	{Name: "sender", Type: abiType("address")},
	{Name: "message", Type: abiType("bytes")},
}

type pair struct {
	Sender  common.Address
	Message []byte
}

var testSender = common.HexToAddress("0x00000000000000000000000000000000000000c0")

func encodePair(t *testing.T, message []byte) []byte {
	t.Helper()
	b, err := pairArgs.Pack(testSender, message)
	require.NoError(t, err)
	return b
}

func TestDecodeABI(t *testing.T) {
	t.Parallel()
	for _, message := range [][]byte{[]byte("hello"), {}, bytes.Repeat([]byte{0xff}, 33)} {
		var p pair
		require.NoError(t, DecodeABI(pairArgs, encodePair(t, message), &p))
		assert.Equal(t, testSender, p.Sender)
		assert.Equal(t, message, p.Message)
	}
}

func TestDecodeABISingle(t *testing.T) {
	t.Parallel()
	args := abi.Arguments{{Name: "pair", Type: abiType("string")}}
	b, err := args.Pack("BTC/USD")
	require.NoError(t, err)

	var s string
	require.NoError(t, DecodeABI(args, b, &s))
	assert.Equal(t, "BTC/USD", s)
}

func TestDecodeABIRefuses(t *testing.T) {
	t.Parallel()
	good := encodePair(t, []byte("hello"))
	dirtyAddress := bytes.Clone(good)
	dirtyAddress[0] = 0xff
	dirtyTail := bytes.Clone(good)
	dirtyTail[len(dirtyTail)-1] = 0xff // after "hello", inside its word
	farOffset := bytes.Clone(good)
	farOffset[63] = 0x60 // message one word later, after a zero word
	farOffset = append(farOffset, make([]byte, 32)...)
	copy(farOffset[96:], good[64:])

	tests := []struct {
		name string
		in   []byte
		want error
	}{
		{"empty", nil, ErrInvalid},
		{"truncated", good[:len(good)-32], ErrInvalid},
		{"short last word", good[:len(good)-1], ErrInvalid},
		{"trailing byte", append(bytes.Clone(good), 0), ErrNotCanonical},
		{"trailing word", append(bytes.Clone(good), make([]byte, 32)...), ErrNotCanonical},
		{"dirty address padding", dirtyAddress, ErrNotCanonical},
		{"dirty bytes padding", dirtyTail, ErrNotCanonical},
		{"another offset", farOffset, ErrNotCanonical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var p pair
			err := DecodeABI(pairArgs, tt.in, &p)
			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, tt.want.Error(), err.Error())
		})
	}
}

func TestDecodeABIWrongTarget(t *testing.T) {
	t.Parallel()
	var v struct{ Other []byte }
	err := DecodeABI(pairArgs, encodePair(t, []byte("hello")), &v)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalid)
	assert.NotErrorIs(t, err, ErrNotCanonical)
}

type message struct {
	Message string `json:"message"`
}

func TestDecodeJSON(t *testing.T) {
	t.Parallel()
	for _, in := range []string{`{"message":"hello"}`, " {\"message\":\"hello\"}\n"} {
		var m message
		require.NoError(t, DecodeJSON([]byte(in), &m), in)
		assert.Equal(t, "hello", m.Message)
	}
}

func TestDecodeJSONRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ``},
		{"not JSON", `secret`},
		{"unknown field", `{"message":"hello","secret":1}`},
		{"wrong type", `{"message":7}`},
		{"second value", `{"message":"hello"}{}`},
		{"trailing data", `{"message":"hello"} secret`},
		{"truncated", `{"message":"hello"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var m message
			err := DecodeJSON([]byte(tt.in), &m)
			require.ErrorIs(t, err, ErrInvalid)
			assert.Equal(t, ErrInvalid.Error(), err.Error())
			assert.False(t, strings.Contains(err.Error(), "secret"))
		})
	}
}

func TestDecodeJSONKeepsTheCause(t *testing.T) {
	t.Parallel()
	var m message
	err := DecodeJSON([]byte(`{"message":x}`), &m)
	var syntax *json.SyntaxError
	require.ErrorAs(t, err, &syntax)
	assert.Equal(t, ErrInvalid.Error(), err.Error())
}
