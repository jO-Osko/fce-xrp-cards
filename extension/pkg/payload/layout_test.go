package payload

import (
	"bytes"
	"encoding/binary"
	"math/big"
	"runtime"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tupleType(t *testing.T, c []abi.ArgumentMarshaling) abi.Type {
	t.Helper()
	return componentType(t, "tuple", c)
}

func componentType(tb testing.TB, name string, c []abi.ArgumentMarshaling) abi.Type {
	tb.Helper()
	typ, err := abi.NewType(name, "", c)
	require.NoError(tb, err)
	return typ
}

const word = 32

func wordOf(v uint64) []byte {
	b := make([]byte, word)
	binary.BigEndian.PutUint64(b[word-8:], v)
	return b
}

type inner struct {
	D string
	E []byte
}

type outer struct {
	A *big.Int
	B string
	C inner
}

type static struct {
	A *big.Int
	B bool
}

// TestDecodeABICanonical decodes go-ethereum's encodings of every shape back to their values.
func TestDecodeABICanonical(t *testing.T) {
	t.Parallel()
	innerC := []abi.ArgumentMarshaling{{Name: "D", Type: "string"}, {Name: "E", Type: "bytes"}}
	outerC := []abi.ArgumentMarshaling{{Name: "A", Type: "uint256"}, {Name: "B", Type: "string"}, {Name: "C", Type: "tuple", Components: innerC}}
	staticC := []abi.ArgumentMarshaling{{Name: "A", Type: "uint256"}, {Name: "B", Type: "bool"}}
	outerV := outer{A: big.NewInt(7), B: "outer", C: inner{D: "inner", E: []byte{9, 9}}}

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{"uint8", canonical(abiType("uint8"), uint8(7))},
		{"int8", canonical(abiType("int8"), int8(-3))},
		{"int128", canonical(abiType("int128"), big.NewInt(-5))},
		{"uint128 max", canonical(abiType("uint128"), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1)))},
		{"bool", canonical(abiType("bool"), true)},
		{"address", canonical(abiType("address"), testSender)},
		{"bytes4", canonical(abiType("bytes4"), [4]byte{1, 2, 3, 4})},
		{"bytes", canonical(abiType("bytes"), bytes.Repeat([]byte{0xff}, 33))},
		{"empty string", canonical(abiType("string"), "")},
		{"uint256[]", canonical(abiType("uint256[]"), []*big.Int{big.NewInt(1), big.NewInt(2)})},
		{"empty uint256[]", canonical(abiType("uint256[]"), []*big.Int{})},
		{"uint256[3]", canonical(abiType("uint256[3]"), [3]*big.Int{big.NewInt(1), big.NewInt(2), big.NewInt(3)})},
		{"string[]", canonical(abiType("string[]"), []string{"a", "", "ccc"})},
		{"string[2]", canonical(abiType("string[2]"), [2]string{"xx", "yyyy"})},
		{"bytes[][]", canonical(abiType("bytes[][]"), [][][]byte{{{1}, {}}, {}, {{2, 3}}})},
		{"uint8[2][]", canonical(abiType("uint8[2][]"), [][2]uint8{{1, 2}, {3, 4}})},
		{"string[][2]", canonical(abiType("string[][2]"), [2][]string{{"a"}, {"b", "c"}})},
		{"dynamic tuple", canonical(tupleType(t, outerC), outerV)},
		{"dynamic tuple[]", canonical(tupleType(t, []abi.ArgumentMarshaling{{Name: "X", Type: "tuple[]", Components: outerC}}), struct{ X []outer }{[]outer{outerV, outerV}})},
		// a dynamic value whose offset exceeds what follows its head slot
		{"string after three words", canonical(tupleType(t, []abi.ArgumentMarshaling{{Name: "A", Type: "uint256"}, {Name: "B", Type: "uint256"}, {Name: "C", Type: "uint256"}, {Name: "D", Type: "string"}}), struct {
			A, B, C *big.Int
			D       string
		}{big.NewInt(1), big.NewInt(2), big.NewInt(3), "x"})},
		{"static tuple[2]", canonical(componentType(t, "tuple[2]", staticC), [2]static{{big.NewInt(1), true}, {big.NewInt(2), false}})},
		{"static tuple in a dynamic one", canonical(tupleType(t, []abi.ArgumentMarshaling{{Name: "S", Type: "tuple", Components: staticC}, {Name: "T", Type: "string"}}), struct {
			S static
			T string
		}{static{big.NewInt(1), true}, "t"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// canonical checks that go-ethereum's encoding of v, as one argument of typ, decodes back to v.
func canonical[T any](typ abi.Type, v T) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		t.Parallel()
		args := abi.Arguments{{Name: "v", Type: typ}}
		b, err := args.Pack(v)
		require.NoError(t, err)

		// a struct, as abi.Arguments.Copy takes a tuple: its first field holds the value
		var got struct{ V T }
		require.NoError(t, DecodeABI(args, b, &got))
		assert.Equal(t, v, got.V)
	}
}

// TestDecodeABICanonicalArguments decodes several top-level arguments, which
// encode unlike one tuple of them: without a leading offset.
func TestDecodeABICanonicalArguments(t *testing.T) {
	t.Parallel()
	args := abi.Arguments{
		{Name: "sender", Type: abiType("address")},
		{Name: "message", Type: abiType("bytes")},
		{Name: "names", Type: abiType("string[]")},
		{Name: "amount", Type: abiType("uint256")},
	}
	type all struct {
		Sender  common.Address
		Message []byte
		Names   []string
		Amount  *big.Int
	}
	want := all{testSender, []byte("hello"), []string{"a", "b"}, big.NewInt(42)}
	b, err := args.Pack(want.Sender, want.Message, want.Names, want.Amount)
	require.NoError(t, err)

	var got all
	require.NoError(t, DecodeABI(args, b, &got))
	assert.Equal(t, want, got)
}

// aliased encodes a single dynamic array of n elements whose offsets all point at elem.
func aliased(n int, elem []byte) []byte {
	b := wordOf(word)
	b = append(b, wordOf(uint64(n))...)
	for range n {
		b = append(b, wordOf(uint64(word*n))...)
	}
	return append(b, elem...)
}

// TestDecodeABIRefusesAliasingCheaply refuses offsets aliasing one region,
// which go-ethereum would decode, and re-encode, once per offset.
func TestDecodeABIRefusesAliasingCheaply(t *testing.T) {
	blob := append(wordOf(100_000), make([]byte, 100_000)...)
	words := wordOf(1000)
	for range 1000 {
		words = append(words, wordOf(1)...)
	}
	twoStrings := tupleType(t, []abi.ArgumentMarshaling{{Name: "A", Type: "string"}, {Name: "B", Type: "string"}})
	sameBlob := append(wordOf(word), wordOf(2*word)...)
	sameBlob = append(append(sameBlob, wordOf(2*word)...), blob...)

	tests := []struct {
		name string
		typ  abi.Type
		in   []byte
	}{
		// the arrays would each allocate about 100 MB
		{"string[]", abiType("string[]"), aliased(1000, blob)},
		{"bytes[]", abiType("bytes[]"), aliased(1000, blob)},
		{"uint256[][]", abiType("uint256[][]"), aliased(1000, words)},
		{"tuple of strings", twoStrings, sameBlob},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			var v any
			err := DecodeABI(abi.Arguments{{Type: tt.typ}}, tt.in, &v)
			runtime.ReadMemStats(&after)

			require.ErrorIs(t, err, ErrNotCanonical)
			assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20))
		})
	}
}

func TestDecodeABIRefusesValues(t *testing.T) {
	t.Parallel()
	high := wordOf(0)
	high[15] = 1 // 2^128
	unsigned := bytes.Repeat([]byte{0xff}, word)
	unsigned[16] = 0x7f // int128's leading bytes say negative, its value positive
	two := wordOf(2)
	dirty := wordOf(0)
	dirty[31] = 1

	tests := []struct {
		name string
		typ  string
		in   []byte
	}{
		{"uint128 above its range", "uint128", high},
		{"int128 not sign-extended", "int128", unsigned},
		{"uint8 above its range", "uint8", wordOf(256)},
		{"bool 2", "bool", two},
		{"bytes4 dirty", "bytes4", dirty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var v any
			err := DecodeABI(abi.Arguments{{Type: abiType(tt.typ)}}, tt.in, &v)
			require.ErrorIs(t, err, ErrNotCanonical)
		})
	}
}

func TestDecodeABIRefusesOutOfRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		typ  string
		in   []byte
	}{
		{"offset past the end", "bytes", wordOf(1 << 40)},
		{"length past the end", "bytes", append(wordOf(word), wordOf(1<<40)...)},
		{"huge length word", "uint256[]", append(wordOf(word), bytes.Repeat([]byte{0xff}, word)...)},
		{"more elements than words", "uint256[]", append(append(wordOf(word), wordOf(3)...), make([]byte, 2*word)...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var v any
			err := DecodeABI(abi.Arguments{{Type: abiType(tt.typ)}}, tt.in, &v)
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
}

func TestDecodeABIUnsupportedType(t *testing.T) {
	t.Parallel()
	var v any
	err := DecodeABI(abi.Arguments{{Name: "zero"}}, wordOf(1), &v)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalid)
	assert.NotErrorIs(t, err, ErrNotCanonical)
}
