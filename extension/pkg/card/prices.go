package card

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// PriceCache holds a single XRP/USD price (8 decimal fixed-point).
type PriceCache struct {
	mu        sync.RWMutex
	priceE8   *big.Int
	updatedAt time.Time
	fallback  string
}

func newPriceCache(defaultUSD string) *PriceCache {
	p, _ := parsePriceE8(defaultUSD)
	return &PriceCache{priceE8: p, updatedAt: time.Now(), fallback: defaultUSD}
}

// Get returns the cached price.
func (c *PriceCache) Get() (*big.Int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.priceE8 == nil {
		return nil, false
	}
	return new(big.Int).Set(c.priceE8), true
}

// Set updates the cached price.
func (c *PriceCache) Set(priceE8 *big.Int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.priceE8 = new(big.Int).Set(priceE8)
	c.updatedAt = time.Now()
}

// Format returns a decimal string for the price.
func (c *PriceCache) Format() string {
	p, ok := c.Get()
	if !ok {
		return c.fallback
	}
	return formatFixed(p, 8)
}

// StartPricePoller refreshes from CoinGecko until done is closed.
// With token or RWA collateral this becomes one price per asset, e.g. issuer
// NAV, XRPL Price Oracles (XLS-47) or Flare FTSO feeds, each with its own
// staleness limit.
func (l *Ledger) StartPricePoller(done <-chan struct{}) {
	ticker := time.NewTicker(PricePollInterval)
	defer ticker.Stop()
	l.refreshPrice()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			l.refreshPrice()
		}
	}
}

func (l *Ledger) refreshPrice() {
	price, err := fetchCoinGeckoXRPUSD()
	if err != nil {
		return
	}
	l.prices.Set(price)
}

func fetchCoinGeckoXRPUSD() (*big.Int, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Get("https://api.coingecko.com/api/v3/simple/price?ids=ripple&vs_currencies=usd")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coingecko status %d", resp.StatusCode)
	}
	var body map[string]map[string]float64
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	usd, ok := body["ripple"]["usd"]
	if !ok || usd <= 0 {
		return nil, fmt.Errorf("no ripple usd price")
	}
	return parsePriceE8(fmt.Sprintf("%.8f", usd))
}
