package card

import (
	"fmt"
	"math/big"
	"strings"
)

var (
	oneE8 = big.NewInt(100_000_000)
	tenK  = big.NewInt(10_000)
)

func parseDecimalToFixed(s string, decimals int) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty amount")
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid decimal: %s", s)
	}
	whole := parts[0]
	if whole == "" {
		whole = "0"
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > decimals {
		frac = frac[:decimals]
	}
	for len(frac) < decimals {
		frac += "0"
	}
	combined := whole + frac
	n := new(big.Int)
	if _, ok := n.SetString(combined, 10); !ok {
		return nil, fmt.Errorf("invalid number: %s", s)
	}
	if neg {
		n.Neg(n)
	}
	return n, nil
}

func formatFixed(n *big.Int, decimals int) string {
	if n == nil {
		n = big.NewInt(0)
	}
	neg := n.Sign() < 0
	abs := new(big.Int).Abs(n)
	s := abs.String()
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	whole := s[:len(s)-decimals]
	frac := s[len(s)-decimals:]
	frac = strings.TrimRight(frac, "0")
	out := whole
	if frac != "" {
		out = whole + "." + frac
	}
	if neg {
		return "-" + out
	}
	return out
}

func parseDrops(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	n := new(big.Int)
	if _, ok := n.SetString(s, 10); !ok {
		return nil, fmt.Errorf("invalid drops: %s", s)
	}
	if n.Sign() < 0 {
		return nil, fmt.Errorf("drops must be non-negative")
	}
	return n, nil
}

func xrpDropsUSD(drops, priceE8 *big.Int) *big.Int {
	num := new(big.Int).Mul(drops, priceE8)
	return num.Div(num, oneE8)
}

func collateralDropsForDebt(debtMicros, priceE8 *big.Int, ltvBps int64) *big.Int {
	if debtMicros.Sign() <= 0 {
		return big.NewInt(0)
	}
	num := new(big.Int).Mul(debtMicros, oneE8)
	num.Mul(num, tenK)
	den := new(big.Int).Mul(priceE8, big.NewInt(ltvBps))
	out := new(big.Int).Div(num, den)
	if new(big.Int).Mod(num, den).Sign() != 0 {
		out.Add(out, big.NewInt(1))
	}
	return out
}

func ltvBreached(debtMicros, collateralUSDMicros *big.Int, capBps int64) bool {
	lhs := new(big.Int).Mul(debtMicros, tenK)
	rhs := new(big.Int).Mul(collateralUSDMicros, big.NewInt(capBps))
	return lhs.Cmp(rhs) > 0
}

func parsePriceE8(s string) (*big.Int, error) {
	return parseDecimalToFixed(s, 8)
}

func formatRLUSD(n *big.Int) string {
	return formatFixed(n, RLUSDDecimals)
}

func parseRLUSD(s string) (*big.Int, error) {
	n, err := parseDecimalToFixed(s, RLUSDDecimals)
	if err != nil {
		return nil, err
	}
	if n.Sign() <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	return n, nil
}
