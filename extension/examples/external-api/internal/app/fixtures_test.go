package app

import (
	"flag"
	"testing"

	"github.com/flare-foundation/fce-go-scaffold/internal/golden"

	"github.com/flare-foundation/fce-go-scaffold/conformance"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite the conformance fixtures")

// fixturesDir is the extension's conformance directory, relative to this package.
const fixturesDir = "../../conformance"

// fixtures are the golden cases `fce conformance` checks the binary against.
// Only network-free ones: the binary fetches from the real sources, so a known
// pair's answer isn't fixed; the unit tests cover it against stub sources.
func fixtures(t *testing.T) []conformance.Fixture {
	t.Helper()
	unknown, err := getPriceArgs.Pack("DOGE/USD")
	require.NoError(t, err)
	return []conformance.Fixture{
		{
			Name: "get-price-unknown-pair", Description: "refuses a pair the extension doesn't price, at once",
			Channel: types.Instruction, OPType: OPTypePrices, OPCommand: OPCommandGetPrice,
			Message: unknown, Result: conformance.Expected{Status: 0, Log: "error: unknown pair"},
		},
		{
			Name: "get-price-not-canonical", Description: "refuses a payload with a trailing byte",
			Channel: types.Instruction, OPType: OPTypePrices, OPCommand: OPCommandGetPrice,
			Message: append(unknown, 0), Result: conformance.Expected{Status: 0, Log: "error: invalid payload"},
		},
		{
			Name: "get-price-direct-unknown-pair", Description: "refuses a pair the extension doesn't price, at once",
			Channel: types.Direct, OPType: OPTypePrices, OPCommand: OPCommandGetPriceDirect,
			Message: []byte(`{"pair":"DOGE/USD"}`), Result: conformance.Expected{Status: 0, Log: "error: unknown pair"},
		},
		{
			Name: "get-price-direct-unknown-field", Description: "refuses a field the command doesn't define",
			Channel: types.Direct, OPType: OPTypePrices, OPCommand: OPCommandGetPriceDirect,
			Message: []byte(`{"pair":"BTC/USD","extra":1}`), Result: conformance.Expected{Status: 0, Log: "error: invalid payload"},
		},
	}
}

// TestFixtures keeps the committed fixtures in step with fixtures:
// `go test ./examples/external-api/internal/app -update` rewrites them.
func TestFixtures(t *testing.T) {
	t.Parallel()
	require.NoError(t, golden.Check(fixturesDir, fixtures(t), *update))
}
