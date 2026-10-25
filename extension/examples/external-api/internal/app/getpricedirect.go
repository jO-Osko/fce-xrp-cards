package app

import (
	"context"
	"encoding/json"
	"math/big"
	"slices"

	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
	"github.com/flare-foundation/fce-go-scaffold/pkg/payload"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// getPriceDirectMessage is the JSON a client posts to the proxy's /direct.
type getPriceDirectMessage struct {
	Pair string `json:"pair"`
}

// pricePartial is a partial result: one source's price, in decimals.
type pricePartial struct {
	Source string `json:"source"`
	Price  string `json:"price"`
}

// priceFinal is the final result: the median, in decimals, of the sources that answered.
type priceFinal struct {
	Pair     string `json:"pair"`
	Price    string `json:"price"`
	Decimals int    `json:"decimals"`
	Sources  int    `json:"sources"`
}

// getPriceDirect is getPrice for direct actions, in JSON, and unsigned: the
// proxy picks a direct action's id, so a signed answer could pass as an
// instruction's. Anyone the proxy lets in can send it; a replay fetches again
// and changes nothing, so it is idempotent, but each one costs a request to
// every source, and their rate limits are shared.
func (p *prices) getPriceDirect(_ context.Context, d *types.DirectInstruction) ([]byte, extension.Run, error) {
	var m getPriceDirectMessage
	if err := payload.DecodeJSON(d.Message, &m); err != nil {
		return nil, nil, extension.PublicError("invalid payload", err)
	}
	if !slices.Contains(pairs, m.Pair) {
		return nil, nil, errUnknownPair
	}

	return nil, func(ctx context.Context, prog *extension.Progress) ([]byte, error) {
		return p.quoteJSON(ctx, m.Pair, prog.Post)
	}, nil
}

// quoteJSON posts each source's price through post, and returns the median, as JSON.
func (p *prices) quoteJSON(ctx context.Context, pair string, post func(context.Context, []byte) error) ([]byte, error) {
	price, n, err := p.median(ctx, pair, func(src string, price *big.Int) {
		if b, err := json.Marshal(pricePartial{Source: src, Price: price.String()}); err == nil {
			_ = post(ctx, b) // the library logs a dropped partial; the final still follows
		}
	})
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(priceFinal{Pair: pair, Price: price.String(), Decimals: decimals, Sources: n})
	if err != nil {
		return nil, extension.PublicError("encoding the result", err)
	}
	return out, nil
}
