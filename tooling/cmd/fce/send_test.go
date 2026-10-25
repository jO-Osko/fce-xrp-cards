package main

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeCall(t *testing.T) {
	t.Parallel()
	// cast calldata 'myCommand(bytes)' 0x1234
	data, err := encodeCall("myCommand(bytes)", []string{"0x1234"})
	require.NoError(t, err)
	assert.Equal(t, "0x51c12d11000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000021234000000000000000000000000000000000000000000000000000000000000", hexutil.Encode(data))

	// cast calldata 'f(address,uint8,int256,bool,bytes2,string,uint256)' 0x00000000000000000000000000000000000000aa 255 -3 true 0xbeef hi 0x10
	data, err = encodeCall("f(address, uint8,int256,bool,bytes2,string,uint256)",
		[]string{"0x00000000000000000000000000000000000000aa", "255", "-3", "true", "0xbeef", "hi", "0x10"})
	require.NoError(t, err)
	assert.Equal(t, "0xf2ec3f3b00000000000000000000000000000000000000000000000000000000000000aa00000000000000000000000000000000000000000000000000000000000000fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffd0000000000000000000000000000000000000000000000000000000000000001beef00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000e0000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000000026869000000000000000000000000000000000000000000000000000000000000", hexutil.Encode(data))

	data, err = encodeCall("ping()", nil)
	require.NoError(t, err)
	assert.Equal(t, "0x5c36b186", hexutil.Encode(data))

	data, err = encodeCall("0xabcdef", nil)
	require.NoError(t, err)
	assert.Equal(t, "0xabcdef", hexutil.Encode(data))

	for _, c := range []struct {
		sig  string
		args []string
	}{
		{"myCommand", nil},
		{"myCommand(bytes)", nil},
		{"myCommand(bytes)", []string{"1234"}},
		{"f(uint8)", []string{"256"}},
		{"f(int8)", []string{"-129"}},
		{"f(uint256)", []string{"-1"}},
		{"f(bytes2)", []string{"0xbe"}},
		{"f(address)", []string{"0x12"}},
		{"f(uint256[])", []string{"1"}},
		{"f((uint256,bool))", []string{"1"}},
		{"f(foo)", []string{"1"}},
		{"0x12", []string{"1"}},
	} {
		_, err := encodeCall(c.sig, c.args)
		assert.Error(t, err, c.sig, c.args)
	}
}

type dataError struct{ data string }

func (e dataError) Error() string  { return "execution reverted" }
func (e dataError) ErrorData() any { return e.data }

func TestInsufficientFee(t *testing.T) {
	t.Parallel()
	// cast calldata 'InsufficientFee(uint256,uint256)' 1000 0
	err := dataError{"0xa458261b00000000000000000000000000000000000000000000000000000000000003e80000000000000000000000000000000000000000000000000000000000000000"}
	price, available, ok := insufficientFee(err)
	require.True(t, ok)
	assert.Equal(t, "1000", price.String())
	assert.Equal(t, "0", available.String())

	_, _, ok = insufficientFee(dataError{"0x12345678"})
	assert.False(t, ok)
	_, _, ok = insufficientFee(errors.New("execution reverted"))
	assert.False(t, ok)
}

func TestCheckURL(t *testing.T) {
	t.Parallel()
	for _, u := range []string{"https://a.trycloudflare.com", "http://203.0.113.7:6662", "https://example.com/machine"} {
		assert.NoError(t, checkURL(u), u)
	}
	for _, u := range []string{"", "a.example.com", "ftp://a.example.com", "https://a.example.com/", "https://u:p@a.example.com", "https://a.example.com?x=1", "https://a.example.com#x", "https://"} {
		assert.Error(t, checkURL(u), u)
	}
}
