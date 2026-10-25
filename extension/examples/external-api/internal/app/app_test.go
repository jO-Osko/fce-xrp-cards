package app

import (
	"context"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/fce-go-scaffold/internal/fakenode"
	"github.com/flare-foundation/fce-go-scaffold/pkg/node"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Answers as the sources gave them on 2026-10-09, trimmed.
const (
	coinbaseBTC = `{"data":{"amount":"82562.585","base":"BTC","currency":"USD"}}`
	krakenBTC   = `{"error":[],"result":{"XXBTZUSD":{"a":["82563.70000","2","2.000"],"c":["82563.70000","0.00023013"]}}}`
	bitstampBTC = `{"timestamp":"1791539465","last":"82565.23","bid":"82565.23","ask":"82565.24"}`
)

// The prices above, in decimals.
var (
	coinbasePriceBTC = big.NewInt(8256258500000)
	krakenPriceBTC   = big.NewInt(8256370000000)
	bitstampPriceBTC = big.NewInt(8256523000000)
)

func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

func good() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{"coinbase": answer(coinbaseBTC), "kraken": answer(krakenBTC), "bitstamp": answer(bitstampBTC)}
}

// stub serves the sources' answers over TLS, each under /<name>/, and returns
// prices fetching from them; a source missing from answers gets 404.
func stub(t *testing.T, answers map[string]http.HandlerFunc) *prices {
	t.Helper()
	mux := http.NewServeMux()
	for name, h := range answers {
		mux.HandleFunc("/"+name+"/", h)
	}
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	srcs := make([]source, len(sources))
	for i, s := range sources {
		srcs[i] = s
		srcs[i].urls = map[string]string{}
		for _, p := range pairs {
			srcs[i].urls[p] = srv.URL + "/" + s.name + "/" + strings.ReplaceAll(p, "/", "-")
		}
	}
	return newPrices(newClient(srv.Client().Transport), srcs, signPort(t))
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

// the instruction a test answers
var testID = common.HexToHash("0x1d")

// recorder records posted partial results.
type recorder struct {
	mu    sync.Mutex
	posts [][]byte
}

func (r *recorder) post(_ context.Context, b []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.posts = append(r.posts, b)
	return nil
}

func TestQuoteABI(t *testing.T) {
	t.Parallel()
	var r recorder
	out, err := stub(t, good()).quoteABI(context.Background(), testID, "BTC/USD", r.post)
	require.NoError(t, err)

	got := map[string]*big.Int{}
	for _, b := range r.posts {
		var part struct {
			Source string
			Price  *big.Int
		}
		v, err := partialArgs.Unpack(b)
		require.NoError(t, err)
		require.NoError(t, partialArgs.Copy(&part, v))
		got[part.Source] = part.Price
	}
	assert.Equal(t, map[string]*big.Int{"coinbase": coinbasePriceBTC, "kraken": krakenPriceBTC, "bitstamp": bitstampPriceBTC}, got)

	var final struct {
		Pair      string
		Price     *big.Int
		Decimals  uint8
		Sources   uint8
		Signature []byte
	}
	v, err := finalArgs.Unpack(out)
	require.NoError(t, err)
	require.NoError(t, finalArgs.Copy(&final, v))
	assert.Equal(t, "BTC/USD", final.Pair)
	assert.Equal(t, krakenPriceBTC, final.Price)
	assert.Equal(t, uint8(decimals), final.Decimals)
	assert.Equal(t, uint8(3), final.Sources)

	// signed by the TEE, as the contract recovers it: EIP-191, V 27 or 28
	msg, err := priceMessage(testID, final.Pair, final.Price, final.Sources)
	require.NoError(t, err)
	sig := final.Signature
	require.Len(t, sig, crypto.SignatureLength)
	assert.Contains(t, []byte{27, 28}, sig[crypto.RecoveryIDOffset])
	sig[crypto.RecoveryIDOffset] -= 27
	pub, err := crypto.SigToPub(accounts.TextHash(crypto.Keccak256(msg)), sig)
	require.NoError(t, err)
	assert.Equal(t, fakenode.Address(), crypto.PubkeyToAddress(*pub))
}

type failingSigner struct{}

func (failingSigner) SignTEE(context.Context, []byte) ([]byte, error) {
	return nil, errors.New("sign port down")
}

func TestQuoteABISignFails(t *testing.T) {
	t.Parallel()
	p := stub(t, good())
	p.signer = failingSigner{}
	var r recorder
	_, err := p.quoteABI(context.Background(), testID, "BTC/USD", r.post)
	require.EqualError(t, err, "signing the result")
}

// The contract's test pins the same bytes: the tag, and the hash of one message.
func TestPriceSharedVector(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "0x5052494345535f50524943455f56310000000000000000000000000000000000", priceTag.Hex())
	msg, err := priceMessage(testID, "BTC/USD", krakenPriceBTC, 3)
	require.NoError(t, err)
	assert.Equal(t, "0xf37ebd1bf2584234671d901ee7cc833bc71be0e25274c031697e580d58ba51bb", crypto.Keccak256Hash(msg).Hex())
}

func TestQuoteJSON(t *testing.T) {
	t.Parallel()
	var r recorder
	out, err := stub(t, good()).quoteJSON(context.Background(), "BTC/USD", r.post)
	require.NoError(t, err)

	got := make([]string, 0, len(r.posts))
	for _, b := range r.posts {
		got = append(got, string(b))
	}
	assert.ElementsMatch(t, []string{
		`{"source":"coinbase","price":"8256258500000"}`,
		`{"source":"kraken","price":"8256370000000"}`,
		`{"source":"bitstamp","price":"8256523000000"}`,
	}, got)
	assert.JSONEq(t, `{"pair":"BTC/USD","price":"8256370000000","decimals":8,"sources":3}`, string(out))
}

// A failing source is left out: its partial isn't posted, and the median is of the others.
func TestSourceFails(t *testing.T) {
	t.Parallel()
	var followed atomic.Bool
	tests := map[string]http.HandlerFunc{
		"error status": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"a redirect":   func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/followed/", http.StatusFound) },
		"over the size limit": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":{"amount":"1","pad":"`+strings.Repeat("x", maxBody)+`"}}`)
		},
		"not JSON":        answer(`<html>`),
		"not a decimal":   answer(`{"data":{"amount":"1e5"}}`),
		"a zero price":    answer(`{"data":{"amount":"0.000000001"}}`),
		"a missing price": answer(`{"data":{}}`),
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			answers := good()
			answers["coinbase"] = h
			answers["followed"] = func(http.ResponseWriter, *http.Request) { followed.Store(true) }
			var r recorder
			out, err := stub(t, answers).quoteJSON(context.Background(), "BTC/USD", r.post)
			require.NoError(t, err)
			assert.Len(t, r.posts, 2)
			// the mean of the two left: kraken's and bitstamp's
			assert.JSONEq(t, `{"pair":"BTC/USD","price":"8256446500000","decimals":8,"sources":2}`, string(out))
		})
	}
	t.Cleanup(func() { assert.False(t, followed.Load(), "a redirect was followed") })
}

func TestKrakenTickers(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"error":["EQuery:Unknown asset pair"],"result":{}}`,
		`{"error":[],"result":{"A":{"c":["1","1"]},"B":{"c":["2","1"]}}}`,
		`{"error":[],"result":{"A":{"c":[]}}}`,
	} {
		_, err := krakenPrice([]byte(body))
		require.Error(t, err, body)
	}
}

func TestTooFewSources(t *testing.T) {
	t.Parallel()
	var r recorder
	_, err := stub(t, map[string]http.HandlerFunc{"kraken": answer(krakenBTC)}).quoteABI(context.Background(), testID, "BTC/USD", r.post)
	require.ErrorIs(t, err, errTooFew)
	assert.Len(t, r.posts, 1, "the one answer is still posted")
}

func TestParsePrice(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int64{
		"82562.585":   8256258500000,
		"0.006870000": 687000,
		"0.123456789": 12345678,
		"7":           700000000,
	} {
		got, err := parsePrice(in)
		require.NoError(t, err, in)
		assert.Equal(t, big.NewInt(want), got, in)
	}
	for _, in := range []string{"", "0", "0.000000001", "-1", "1e3", "1/3", " 1", "1.", ".5", "1,5"} {
		_, err := parsePrice(in)
		require.Error(t, err, in)
	}
}

// Every source has an https URL for every pair: plain HTTP would let the network, the operator's, change the price.
func TestSourcesHTTPS(t *testing.T) {
	t.Parallel()
	for _, s := range sources {
		require.Len(t, s.urls, len(pairs), s.name)
		for _, p := range pairs {
			assert.True(t, strings.HasPrefix(s.urls[p], "https://"), "%s %s: %s", s.name, p, s.urls[p])
		}
	}
}

func TestGetPrice(t *testing.T) {
	t.Parallel()
	p := stub(t, good())
	call := func(id common.Hash, msg []byte) ([]byte, bool, error) {
		out, run, err := p.getPrice(context.Background(), &instruction.DataFixed{InstructionID: id, OriginalMessage: msg})
		return out, run != nil, err
	}
	msg, err := getPriceArgs.Pack("BTC/USD")
	require.NoError(t, err)
	unknown, err := getPriceArgs.Pack("DOGE/USD")
	require.NoError(t, err)

	// refusals don't use up the id
	_, _, err = call(testID, unknown)
	require.ErrorIs(t, err, errUnknownPair)
	_, _, err = call(testID, append(msg, 0))
	require.EqualError(t, err, "invalid payload")

	out, long, err := call(testID, msg)
	require.NoError(t, err)
	assert.Nil(t, out)
	assert.True(t, long, "it goes long-running")

	// delivered again, it would fetch anew for the operator to pick from
	_, _, err = call(testID, msg)
	require.ErrorIs(t, err, errAnswered)
	_, long, err = call(common.HexToHash("0x1e"), msg)
	require.NoError(t, err)
	assert.True(t, long)
}

func TestGetPriceDirect(t *testing.T) {
	t.Parallel()
	p := stub(t, good())
	call := func(msg string) (bool, error) {
		_, run, err := p.getPriceDirect(context.Background(), &types.DirectInstruction{Message: []byte(msg)})
		return run != nil, err
	}
	long, err := call(`{"pair":"FLR/USD"}`)
	require.NoError(t, err)
	assert.True(t, long)

	_, err = call(`{"pair":"DOGE/USD"}`)
	require.ErrorIs(t, err, errUnknownPair)
	for _, msg := range []string{`{"pair":"BTC/USD","extra":1}`, `{"pair":"BTC/USD"} {}`, `[]`} {
		_, err = call(msg)
		require.EqualError(t, err, "invalid payload", msg)
	}
}
