package card

import (
	"fmt"
	"math/big"
	"strings"
)

// Loan conversion: the TEE only reserves debt for a conversion and credits RLUSD
// repaid to the pool under the user's tag. It never confirms a conversion: if
// the repayment never arrives, the reservation lapses and nothing changed.

// PendingConversion reserves debt a conversion is about to repay, until it expires.
type PendingConversion struct {
	ID      string   `json:"id"`
	Amount  *big.Int `json:"amount"`
	Created int64    `json:"created"`
	Expires int64    `json:"expires"`
}

// Repayment is one RLUSD payment to the pool credited against a user's debt.
type Repayment struct {
	TxHash           string `json:"txHash"`
	RlusdAmount      string `json:"rlusdAmount"`
	RlusdCredited    string `json:"rlusdCredited"`
	RlusdExcess      string `json:"rlusdExcess,omitempty"`
	XrpUnlockedDrops string `json:"xrpUnlockedDrops"`
	At               int64  `json:"at"`
}

// LoanTerms are the XLS-66 terms the mock broker approves.
type LoanTerms struct {
	InterestRate    uint32 `json:"interestRate"`
	PaymentTotal    uint32 `json:"paymentTotal"`
	PaymentInterval uint32 `json:"paymentInterval"`
	GracePeriod     uint32 `json:"gracePeriod"`
}

// ConvertResponse tells the Credit Server exactly which loan to take: principal
// RlusdAmount from LoanBrokerID, repaid to PoolAccount with DestinationTag.
type ConvertResponse struct {
	ConversionID        string    `json:"conversionId"`
	Tag                 uint32    `json:"tag"`
	XRPLAddress         string    `json:"xrplAddress"`
	RlusdAmount         string    `json:"rlusdAmount"`
	ConvertibleRlusd    string    `json:"convertibleRlusd"`
	TTLSeconds          int64     `json:"ttlSeconds"`
	PoolAccount         string    `json:"poolAccount"`
	DestinationTag      uint32    `json:"destinationTag"`
	CreditServerAccount string    `json:"creditServerAccount"`
	LoanBrokerID        string    `json:"loanBrokerId"`
	RLUSDCurrency       string    `json:"rlusdCurrency"`
	RLUSDIssuer         string    `json:"rlusdIssuer"`
	Terms               LoanTerms `json:"terms"`
}

// RepaymentResponse is the result of crediting RLUSD repaid to the pool.
type RepaymentResponse struct {
	Tag               uint32 `json:"tag"`
	TxHash            string `json:"txHash"`
	RlusdCredited     string `json:"rlusdCredited"`
	RlusdExcess       string `json:"rlusdExcess,omitempty"`
	XrpUnlockedDrops  string `json:"xrpUnlockedDrops"`
	RlusdDebt         string `json:"rlusdDebt"`
	XrpAvailableDrops string `json:"xrpAvailableDrops"`
	XrpLockedDrops    string `json:"xrpLockedDrops"`
	Duplicate         bool   `json:"duplicate,omitempty"`
}

// PendingConversionRecord is a pending conversion as GET_POSITION shows it.
type PendingConversionRecord struct {
	ID          string `json:"id"`
	RlusdAmount string `json:"rlusdAmount"`
	Created     int64  `json:"created"`
	Expires     int64  `json:"expires"`
}

// pruneConversions drops conversions that expired by now (or are fully repaid)
// and returns the debt the rest still reserve.
func (a *Account) pruneConversions(now int64) *big.Int {
	total := big.NewInt(0)
	kept := a.Conversions[:0]
	for _, c := range a.Conversions {
		if c.Expires > now && c.Amount.Sign() > 0 {
			kept = append(kept, c)
			total.Add(total, c.Amount)
		}
	}
	a.Conversions = kept
	return total
}

// activeConversions lists conversions still open at now without changing the account.
func (a *Account) activeConversions(now int64) ([]PendingConversionRecord, *big.Int) {
	recs := make([]PendingConversionRecord, 0, len(a.Conversions))
	total := big.NewInt(0)
	for _, c := range a.Conversions {
		if c.Expires > now && c.Amount.Sign() > 0 {
			recs = append(recs, PendingConversionRecord{
				ID: c.ID, RlusdAmount: formatRLUSD(c.Amount), Created: c.Created, Expires: c.Expires,
			})
			total.Add(total, c.Amount)
		}
	}
	return recs, total
}

// ConvertRequest reserves part of a user's debt for conversion to an XLS-66 loan.
// An empty rlusdAmount converts all debt not already reserved.
func (l *Ledger) ConvertRequest(req Request) (any, error) {
	if req.Tag == 0 {
		return nil, fmt.Errorf("tag required")
	}
	if l.cfg.PoolAccount == "" || l.cfg.LoanBrokerID == "" || l.cfg.CreditServerAccount == "" {
		return nil, fmt.Errorf("loan conversion not configured")
	}

	l.mu.Lock()
	acc, ok := l.accounts[req.Tag]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("unknown tag %d", req.Tag)
	}
	now := l.now().Unix()
	l.pool.accrue(now)
	convertible := new(big.Int).Sub(l.debtNow(acc), acc.pruneConversions(now))
	if convertible.Sign() < 0 {
		convertible.SetInt64(0)
	}

	amount := new(big.Int).Set(convertible)
	if strings.TrimSpace(req.RlusdAmount) != "" {
		var err error
		if amount, err = parseRLUSD(req.RlusdAmount); err != nil {
			l.mu.Unlock()
			return nil, err
		}
	}
	if amount.Sign() <= 0 {
		l.mu.Unlock()
		return nil, fmt.Errorf("nothing to convert")
	}
	if amount.Cmp(convertible) > 0 {
		l.mu.Unlock()
		return nil, fmt.Errorf("amount exceeds convertible debt %s", formatRLUSD(convertible))
	}

	id := fmt.Sprintf("%016x", l.conversionSeq.Add(1))
	acc.Conversions = append(acc.Conversions, PendingConversion{
		ID:      id,
		Amount:  amount,
		Created: now,
		Expires: now + int64(ConversionTTL.Seconds()),
	})
	resp := ConvertResponse{
		ConversionID:        id,
		Tag:                 acc.Tag,
		XRPLAddress:         acc.XRPLAddress,
		RlusdAmount:         formatRLUSD(amount),
		ConvertibleRlusd:    formatRLUSD(new(big.Int).Sub(convertible, amount)),
		TTLSeconds:          int64(ConversionTTL.Seconds()),
		PoolAccount:         l.cfg.PoolAccount,
		DestinationTag:      acc.Tag,
		CreditServerAccount: l.cfg.CreditServerAccount,
		LoanBrokerID:        l.cfg.LoanBrokerID,
		RLUSDCurrency:       RLUSDCurrencyHex,
		RLUSDIssuer:         RLUSDIssuer,
		Terms:               DefaultLoanTerms,
	}
	l.mu.Unlock()
	l.persist()
	return resp, nil
}

// CreditRLUSDRepayment credits RLUSD paid to the pool with the user's tag
// (idempotent on txHash). It lowers the debt, unlocks collateral in proportion,
// and releases pending conversions oldest first. Any RLUSD beyond the debt is
// recorded as excess, not credited.
func (l *Ledger) CreditRLUSDRepayment(req Request) (any, error) {
	if req.Tag == 0 {
		return nil, fmt.Errorf("tag required")
	}
	amount, err := parseRLUSD(req.RlusdAmount)
	if err != nil {
		return nil, err
	}
	txHash := strings.TrimSpace(req.TxHash)
	if txHash == "" {
		return nil, fmt.Errorf("txHash required")
	}

	l.mu.Lock()
	acc, ok := l.accounts[req.Tag]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("unknown tag %d", req.Tag)
	}
	if l.processedTx[txHash] {
		resp := RepaymentResponse{
			Tag:               acc.Tag,
			TxHash:            txHash,
			RlusdCredited:     "0",
			XrpUnlockedDrops:  "0",
			RlusdDebt:         formatRLUSD(debtOf(acc.DebtScaled, l.pool.indexAt(l.now().Unix()))),
			XrpAvailableDrops: acc.XrpAvailable.String(),
			XrpLockedDrops:    acc.XrpLocked.String(),
			Duplicate:         true,
		}
		l.mu.Unlock()
		return resp, nil
	}

	now := l.now().Unix()
	l.pool.accrue(now)
	debt := l.debtNow(acc)
	credited := new(big.Int).Set(amount)
	if credited.Cmp(debt) > 0 {
		credited.Set(debt)
	}
	// Excess RLUSD stays in the pool account but outside the ledger's cash: it is
	// nobody's liquidity until an operator decides what to do with it.
	excess := new(big.Int).Sub(amount, credited)

	// Unlock locked XRP in the same proportion as the debt repaid; all of it once the debt is gone.
	unlock := big.NewInt(0)
	if credited.Sign() > 0 {
		if credited.Cmp(debt) == 0 {
			unlock.Set(acc.XrpLocked)
		} else {
			unlock.Mul(acc.XrpLocked, credited)
			unlock.Div(unlock, debt)
		}
	}
	l.repayDebt(acc, credited, debt)
	l.pool.Cash.Add(l.pool.Cash, credited)
	acc.XrpLocked.Sub(acc.XrpLocked, unlock)
	acc.XrpAvailable.Add(acc.XrpAvailable, unlock)

	rest := new(big.Int).Set(credited)
	for i := range acc.Conversions {
		if rest.Sign() == 0 {
			break
		}
		c := &acc.Conversions[i]
		take := new(big.Int).Set(rest)
		if take.Cmp(c.Amount) > 0 {
			take.Set(c.Amount)
		}
		c.Amount = new(big.Int).Sub(c.Amount, take)
		rest.Sub(rest, take)
	}
	acc.pruneConversions(now)

	l.processedTx[txHash] = true
	rec := Repayment{
		TxHash:           txHash,
		RlusdAmount:      formatRLUSD(amount),
		RlusdCredited:    formatRLUSD(credited),
		XrpUnlockedDrops: unlock.String(),
		At:               now,
	}
	if excess.Sign() > 0 {
		rec.RlusdExcess = formatRLUSD(excess)
	}
	acc.Repayments = append(acc.Repayments, rec)
	resp := RepaymentResponse{
		Tag:               acc.Tag,
		TxHash:            txHash,
		RlusdCredited:     rec.RlusdCredited,
		RlusdExcess:       rec.RlusdExcess,
		XrpUnlockedDrops:  rec.XrpUnlockedDrops,
		RlusdDebt:         formatRLUSD(l.debtNow(acc)),
		XrpAvailableDrops: acc.XrpAvailable.String(),
		XrpLockedDrops:    acc.XrpLocked.String(),
	}
	l.mu.Unlock()
	l.persist()
	return resp, nil
}
