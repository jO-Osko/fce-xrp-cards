package app

import (
	"context"
	"math/big"
	"net/http"
	"slices"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
)

// minSources is how many sources must answer: of three, one may be down or wrong.
const minSources = 2

var (
	errUnknownPair = extension.PublicError("unknown pair", nil)
	errTooFew      = extension.PublicError("too few sources answered", nil)
	errAnswered    = extension.PublicError("instruction already answered", nil)
)

// prices fetches from its sources, and signs the instructions' answers.
type prices struct {
	client  *http.Client
	sources []source
	signer  Signer

	mu sync.Mutex
	// the instructions answered since boot: tee-node doesn't remember them, so the
	// operator could deliver one again, fetch anew, and submit the price it likes.
	// Each holds 32 bytes until a reboot, which gives the TEE a new id.
	answered map[common.Hash]bool
}

// answer records id, and reports false if it was already answered.
func (p *prices) answer(id common.Hash) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.answered[id] {
		return false
	}
	p.answered[id] = true
	return true
}

// median fetches pair from every source at once, passes each answer to partial
// as it arrives, and returns the median of the answers and how many there were.
// A source that fails is left out, and its error stays inside: an API's answer
// may carry anything.
func (p *prices) median(ctx context.Context, pair string, partial func(source string, price *big.Int)) (*big.Int, int, error) {
	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		got []*big.Int
	)
	for _, src := range p.sources {
		wg.Go(func() {
			price, err := fetch(ctx, p.client, src, pair)
			if err != nil {
				return
			}
			partial(src.name, price)
			mu.Lock()
			got = append(got, price)
			mu.Unlock()
		})
	}
	wg.Wait()

	if len(got) < minSources {
		return nil, len(got), errTooFew
	}
	slices.SortFunc(got, (*big.Int).Cmp)
	m := len(got) / 2
	if len(got)%2 == 1 {
		return got[m], len(got), nil
	}
	sum := new(big.Int).Add(got[m-1], got[m])
	return sum.Rsh(sum, 1), len(got), nil
}
