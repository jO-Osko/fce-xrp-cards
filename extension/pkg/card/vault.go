package card

import (
	"fmt"
	"math/big"
	"strings"
)

// The lending pool ("vault") inside the TEE. One pool of RLUSD funds every card
// payment; the operator's own liquidity and third-party LPs' liquidity are
// commingled in it, and both hold the same LP shares (an XRPL trust-line token
// issued by the LP deposit account).
//
// What the shares are worth:
//
//	totalAssets = cash + card debt (with interest) − owed to merchants − owed to the treasury
//
// Card debt accrues a fixed BorrowAPRBps; ReserveFactorBps of that interest and
// the CardFeeBps merchant fee go to the TEE treasury, the rest of the interest
// raises totalAssets and so the share price. Deposits and redemptions use the
// ERC-4626 share formulas with a virtual share and asset of 1 (no donation
// attack is possible here: cash only changes through ledger commands).
//
// Liquidity is never short in the demo (the operator mints more), so a card
// payment or redemption that would need more cash than is free simply reverts.

var indexOne = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)

const secondsPerYear = 365 * 24 * 3600

// LP is a liquidity provider: an XRPL address with a reserved deposit tag.
type LP struct {
	Tag            uint32         `json:"tag"`
	XRPLAddress    string         `json:"xrplAddress"`
	SharesIssued   *big.Int       `json:"sharesIssued"`
	SharesRedeemed *big.Int       `json:"sharesRedeemed"`
	Deposits       []LPDeposit    `json:"deposits,omitempty"`
	Redemptions    []LPRedemption `json:"redemptions,omitempty"`
}

// LPDeposit is RLUSD credited to an LP for newly issued shares.
type LPDeposit struct {
	TxHash      string `json:"txHash"`
	RlusdAmount string `json:"rlusdAmount"`
	Shares      string `json:"shares"`
	At          int64  `json:"at"`
}

// LPRedemption is shares burned for RLUSD paid out of the pool.
type LPRedemption struct {
	TxHash      string `json:"txHash"`
	Shares      string `json:"shares"`
	RlusdAmount string `json:"rlusdAmount"`
	At          int64  `json:"at"`
}

// Pool is the vault state. Amounts are RLUSD micro-units, shares are share units
// (1e6 per displayed share, so one displayed share starts at one RLUSD); the
// watcher converts them to and from the token's decimal amounts on XRPL.
type Pool struct {
	BorrowIndex     *big.Int `json:"borrowIndex"`
	LastAccrual     int64    `json:"lastAccrual"`
	TotalDebtScaled *big.Int `json:"totalDebtScaled"`
	Cash            *big.Int `json:"cash"`
	TreasuryOwed    *big.Int `json:"treasuryOwed"`
	TotalShares     *big.Int `json:"totalShares"`

	// Running totals for the stats.
	FeesEarned      *big.Int `json:"feesEarned"`
	InterestEarned  *big.Int `json:"interestEarned"`
	InterestReserve *big.Int `json:"interestReserve"`
	TreasuryPaid    *big.Int `json:"treasuryPaid"`

	LPs         map[uint32]*LP    `json:"lps"`
	LPAddrToTag map[string]uint32 `json:"lpAddrToTag"`
	NextLPTag   uint32            `json:"nextLPTag"`
}

func newPool() *Pool {
	return &Pool{
		BorrowIndex:     new(big.Int).Set(indexOne),
		TotalDebtScaled: big.NewInt(0),
		Cash:            big.NewInt(0),
		TreasuryOwed:    big.NewInt(0),
		TotalShares:     big.NewInt(0),
		FeesEarned:      big.NewInt(0),
		InterestEarned:  big.NewInt(0),
		InterestReserve: big.NewInt(0),
		TreasuryPaid:    big.NewInt(0),
		LPs:             map[uint32]*LP{},
		LPAddrToTag:     map[string]uint32{},
		NextLPTag:       LPTagBase,
	}
}

// fillDefaults repairs a pool loaded from an older or partial snapshot.
func (p *Pool) fillDefaults() {
	zero := func(n **big.Int) {
		if *n == nil {
			*n = big.NewInt(0)
		}
	}
	if p.BorrowIndex == nil || p.BorrowIndex.Sign() == 0 {
		p.BorrowIndex = new(big.Int).Set(indexOne)
	}
	for _, n := range []**big.Int{
		&p.TotalDebtScaled, &p.Cash, &p.TreasuryOwed, &p.TotalShares,
		&p.FeesEarned, &p.InterestEarned, &p.InterestReserve, &p.TreasuryPaid,
	} {
		zero(n)
	}
	if p.LPs == nil {
		p.LPs = map[uint32]*LP{}
	}
	if p.LPAddrToTag == nil {
		p.LPAddrToTag = map[string]uint32{}
	}
	if p.NextLPTag < LPTagBase {
		p.NextLPTag = LPTagBase
	}
	for _, lp := range p.LPs {
		zero(&lp.SharesIssued)
		zero(&lp.SharesRedeemed)
	}
}

// accrualPeriods returns the whole accrual periods between the last accrual and now.
// Interest moves in whole InterestAccrualPeriod steps so results inside one
// period are exact, which keeps the conformance fixtures deterministic.
func (p *Pool) accrualPeriods(now int64) int64 {
	if p.LastAccrual == 0 || now <= p.LastAccrual {
		return 0
	}
	return (now - p.LastAccrual) / int64(InterestAccrualPeriod.Seconds())
}

// indexAt is the borrow index at now, without changing the pool.
func (p *Pool) indexAt(now int64) *big.Int {
	idx := new(big.Int).Set(p.BorrowIndex)
	n := p.accrualPeriods(now)
	if n == 0 {
		return idx
	}
	dt := n * int64(InterestAccrualPeriod.Seconds())
	// idx += idx × APR × dt / year: simple interest over the whole gap since the
	// last accrual, so it compounds only as often as the pool is written.
	inc := new(big.Int).Mul(idx, big.NewInt(BorrowAPRBps*dt))
	inc.Div(inc, big.NewInt(10000*secondsPerYear))
	return idx.Add(idx, inc)
}

// treasuryAt is the treasury owed at now, including the reserve cut of interest not yet accrued.
func (p *Pool) treasuryAt(now int64) *big.Int {
	interest := new(big.Int).Sub(debtOf(p.TotalDebtScaled, p.indexAt(now)), debtOf(p.TotalDebtScaled, p.BorrowIndex))
	reserve := interest.Mul(interest, big.NewInt(ReserveFactorBps))
	reserve.Div(reserve, big.NewInt(10000))
	return reserve.Add(reserve, p.TreasuryOwed)
}

// accrue moves the pool's interest up to now: the debt grows and the treasury
// takes its reserve cut. Callers hold the ledger write lock.
func (p *Pool) accrue(now int64) {
	if p.LastAccrual == 0 {
		p.LastAccrual = now
		return
	}
	n := p.accrualPeriods(now)
	if n == 0 {
		return
	}
	newIdx := p.indexAt(now)
	interest := new(big.Int).Sub(debtOf(p.TotalDebtScaled, newIdx), debtOf(p.TotalDebtScaled, p.BorrowIndex))
	reserve := new(big.Int).Mul(interest, big.NewInt(ReserveFactorBps))
	reserve.Div(reserve, big.NewInt(10000))
	p.TreasuryOwed.Add(p.TreasuryOwed, reserve)
	p.InterestReserve.Add(p.InterestReserve, reserve)
	p.InterestEarned.Add(p.InterestEarned, interest)
	p.BorrowIndex = newIdx
	p.LastAccrual += n * int64(InterestAccrualPeriod.Seconds())
}

// debtOf converts a scaled debt to RLUSD at index idx (rounded down).
func debtOf(scaled, idx *big.Int) *big.Int {
	d := new(big.Int).Mul(scaled, idx)
	return d.Div(d, indexOne)
}

// scaledUp converts an RLUSD amount to scaled debt, rounded up, so borrowing never rounds in the borrower's favour.
func scaledUp(amount, idx *big.Int) *big.Int {
	s := new(big.Int).Mul(amount, indexOne)
	s.Add(s, new(big.Int).Sub(idx, big.NewInt(1)))
	return s.Div(s, idx)
}

func (l *Ledger) addDebt(acc *Account, amount *big.Int) {
	s := scaledUp(amount, l.pool.BorrowIndex)
	acc.DebtScaled.Add(acc.DebtScaled, s)
	l.pool.TotalDebtScaled.Add(l.pool.TotalDebtScaled, s)
}

// repayDebt lowers acc's debt by amount (at most all of it); debtNow is its current debt.
func (l *Ledger) repayDebt(acc *Account, amount, debtNow *big.Int) {
	s := new(big.Int).Set(acc.DebtScaled)
	if amount.Cmp(debtNow) < 0 {
		s.Mul(amount, indexOne)
		s.Div(s, l.pool.BorrowIndex)
		if s.Cmp(acc.DebtScaled) > 0 {
			s.Set(acc.DebtScaled)
		}
	}
	acc.DebtScaled.Sub(acc.DebtScaled, s)
	l.pool.TotalDebtScaled.Sub(l.pool.TotalDebtScaled, s)
}

// debtNow is acc's current debt, accrued to the pool's index.
func (l *Ledger) debtNow(acc *Account) *big.Int {
	return debtOf(acc.DebtScaled, l.pool.BorrowIndex)
}

// owedToMerchants is card spend not yet paid out by a settlement.
func (l *Ledger) owedToMerchants() *big.Int {
	owed := new(big.Int).Set(l.virtualSettlement)
	if l.pendingSettle != nil {
		owed.Add(owed, l.pendingSettle.Amount)
	}
	return owed
}

// owedToTreasury is TEE profit not yet paid to the treasury account.
func (l *Ledger) owedToTreasury(treasuryOwed *big.Int) *big.Int {
	owed := new(big.Int).Set(treasuryOwed)
	if l.pendingSettle != nil && l.pendingSettle.TreasuryAmount != nil {
		owed.Add(owed, l.pendingSettle.TreasuryAmount)
	}
	return owed
}

// freeLiquidity is cash not already promised to merchants or the treasury.
func (l *Ledger) freeLiquidity(treasuryOwed *big.Int) *big.Int {
	free := new(big.Int).Sub(l.pool.Cash, l.owedToMerchants())
	free.Sub(free, l.owedToTreasury(treasuryOwed))
	if free.Sign() < 0 {
		free.SetInt64(0)
	}
	return free
}

// totalAssets is what all LP shares together are worth, at index idx.
func (l *Ledger) totalAssets(idx, treasuryOwed *big.Int) *big.Int {
	a := new(big.Int).Add(l.pool.Cash, debtOf(l.pool.TotalDebtScaled, idx))
	a.Sub(a, l.owedToMerchants())
	a.Sub(a, l.owedToTreasury(treasuryOwed))
	if a.Sign() < 0 {
		a.SetInt64(0)
	}
	return a
}

// sharePriceMicro is the RLUSD (micro-units) one displayed share (1e6 units) is worth.
func sharePriceMicro(totalAssets, totalShares *big.Int) *big.Int {
	p := new(big.Int).Add(totalAssets, big.NewInt(1))
	p.Mul(p, big.NewInt(1_000_000))
	return p.Div(p, new(big.Int).Add(totalShares, big.NewInt(1)))
}

func parseShares(s string) (*big.Int, error) {
	n, ok := new(big.Int).SetString(strings.TrimSpace(s), 10)
	if !ok || n.Sign() <= 0 {
		return nil, fmt.Errorf("shares must be a positive integer (share units)")
	}
	return n, nil
}

// LPRegister reserves an LP deposit tag for an XRPL address (idempotent).
func (l *Ledger) LPRegister(req Request) (any, error) {
	addr := strings.TrimSpace(req.XRPLAddress)
	if addr == "" || !strings.HasPrefix(addr, "r") {
		return nil, fmt.Errorf("xrplAddress required (classic address starting with r)")
	}
	l.mu.Lock()
	lp, created := l.lpFor(addr)
	resp := LPRegisterResponse{
		Tag:            lp.Tag,
		XRPLAddress:    lp.XRPLAddress,
		DepositAccount: l.cfg.LPDepositAccount,
		ShareCurrency:  l.cfg.ShareCurrency,
	}
	l.mu.Unlock()
	if created {
		l.persist()
	}
	return resp, nil
}

// lpFor returns the LP for addr, registering it when new. Callers hold the write lock.
func (l *Ledger) lpFor(addr string) (*LP, bool) {
	if tag, ok := l.pool.LPAddrToTag[addr]; ok {
		return l.pool.LPs[tag], false
	}
	l.pool.NextLPTag++
	lp := &LP{Tag: l.pool.NextLPTag, XRPLAddress: addr, SharesIssued: big.NewInt(0), SharesRedeemed: big.NewInt(0)}
	l.pool.LPs[lp.Tag] = lp
	l.pool.LPAddrToTag[addr] = lp.Tag
	return lp, true
}

// LPDeposit credits RLUSD that reached the LP deposit account with an LP tag
// (idempotent on txHash) and mints shares for it. The watcher then sends the
// share tokens to the LP and sweeps the RLUSD into the pool.
func (l *Ledger) LPDeposit(req Request) (any, error) {
	amount, err := parseRLUSD(req.RlusdAmount)
	if err != nil {
		return nil, err
	}
	txHash := strings.TrimSpace(req.TxHash)
	if txHash == "" {
		return nil, fmt.Errorf("txHash required")
	}

	l.mu.Lock()
	lp, ok := l.pool.LPs[req.Tag]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("unknown LP tag %d; reserve a tag first", req.Tag)
	}
	if l.processedTx[txHash] {
		// Report what the original deposit minted, so a caller that lost the first
		// answer (timeout, crash) can still issue the shares.
		resp := LPDepositResponse{Tag: lp.Tag, XRPLAddress: lp.XRPLAddress, TxHash: txHash, Shares: "0", ShareCurrency: l.cfg.ShareCurrency, Duplicate: true}
		for _, d := range lp.Deposits {
			if d.TxHash == txHash {
				resp.RlusdAmount, resp.Shares = d.RlusdAmount, d.Shares
			}
		}
		l.mu.Unlock()
		return resp, nil
	}

	now := l.now().Unix()
	l.pool.accrue(now)
	assets := l.totalAssets(l.pool.BorrowIndex, l.pool.TreasuryOwed)
	shares := new(big.Int).Mul(amount, new(big.Int).Add(l.pool.TotalShares, big.NewInt(1)))
	shares.Div(shares, new(big.Int).Add(assets, big.NewInt(1)))
	if shares.Sign() <= 0 {
		l.mu.Unlock()
		return nil, fmt.Errorf("deposit too small for one share unit")
	}
	l.pool.TotalShares.Add(l.pool.TotalShares, shares)
	l.pool.Cash.Add(l.pool.Cash, amount)
	lp.SharesIssued.Add(lp.SharesIssued, shares)
	lp.Deposits = append(lp.Deposits, LPDeposit{TxHash: txHash, RlusdAmount: formatRLUSD(amount), Shares: shares.String(), At: now})
	l.processedTx[txHash] = true

	resp := LPDepositResponse{
		Tag:           lp.Tag,
		XRPLAddress:   lp.XRPLAddress,
		TxHash:        txHash,
		RlusdAmount:   formatRLUSD(amount),
		Shares:        shares.String(),
		SharePrice:    formatRLUSD(sharePriceMicro(l.totalAssets(l.pool.BorrowIndex, l.pool.TreasuryOwed), l.pool.TotalShares)),
		ShareCurrency: l.cfg.ShareCurrency,
	}
	l.mu.Unlock()
	l.persist()
	return resp, nil
}

// LPRedeem burns shares an address sent back to the LP deposit account and
// returns the RLUSD to pay it (idempotent on txHash). Shares are transferable,
// so whoever sends them is paid. It reverts when free cash cannot cover the
// payout; the watcher then returns the shares (never expected in the demo).
func (l *Ledger) LPRedeem(req Request) (any, error) {
	addr := strings.TrimSpace(req.XRPLAddress)
	if addr == "" || !strings.HasPrefix(addr, "r") {
		return nil, fmt.Errorf("xrplAddress required (classic address starting with r)")
	}
	shares, err := parseShares(req.Shares)
	if err != nil {
		return nil, err
	}
	txHash := strings.TrimSpace(req.TxHash)
	if txHash == "" {
		return nil, fmt.Errorf("txHash required")
	}

	l.mu.Lock()
	if l.processedTx[txHash] {
		// Report the original payout, so a caller that lost the first answer can still pay it.
		resp := LPRedeemResponse{XRPLAddress: addr, TxHash: txHash, Shares: "0", RlusdAmount: "0", Duplicate: true}
		if tag, ok := l.pool.LPAddrToTag[addr]; ok {
			for _, r := range l.pool.LPs[tag].Redemptions {
				if r.TxHash == txHash {
					resp.Shares, resp.RlusdAmount = r.Shares, r.RlusdAmount
				}
			}
		}
		l.mu.Unlock()
		return resp, nil
	}
	if shares.Cmp(l.pool.TotalShares) > 0 {
		l.mu.Unlock()
		return nil, fmt.Errorf("more shares than exist")
	}
	now := l.now().Unix()
	l.pool.accrue(now)
	assets := l.totalAssets(l.pool.BorrowIndex, l.pool.TreasuryOwed)
	payout := new(big.Int).Mul(shares, new(big.Int).Add(assets, big.NewInt(1)))
	payout.Div(payout, new(big.Int).Add(l.pool.TotalShares, big.NewInt(1)))
	if payout.Cmp(l.freeLiquidity(l.pool.TreasuryOwed)) > 0 {
		l.mu.Unlock()
		// Demo: the operator always keeps enough liquidity, so this is just a revert.
		return nil, fmt.Errorf("insufficient pool liquidity")
	}
	l.pool.TotalShares.Sub(l.pool.TotalShares, shares)
	l.pool.Cash.Sub(l.pool.Cash, payout)
	lp, _ := l.lpFor(addr)
	lp.SharesRedeemed.Add(lp.SharesRedeemed, shares)
	lp.Redemptions = append(lp.Redemptions, LPRedemption{TxHash: txHash, Shares: shares.String(), RlusdAmount: formatRLUSD(payout), At: now})
	l.processedTx[txHash] = true

	resp := LPRedeemResponse{
		XRPLAddress: addr,
		TxHash:      txHash,
		Shares:      shares.String(),
		RlusdAmount: formatRLUSD(payout),
		SharePrice:  formatRLUSD(sharePriceMicro(l.totalAssets(l.pool.BorrowIndex, l.pool.TreasuryOwed), l.pool.TotalShares)),
	}
	l.mu.Unlock()
	l.persist()
	return resp, nil
}

// GetVault returns the pool's state, and the LP's own records when xrplAddress is given.
func (l *Ledger) GetVault(req Request) (any, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	v := l.vaultView(l.now().Unix())
	if addr := strings.TrimSpace(req.XRPLAddress); addr != "" {
		if tag, ok := l.pool.LPAddrToTag[addr]; ok {
			lp := l.pool.LPs[tag]
			deps := make([]LPDeposit, 0, len(lp.Deposits))
			for i := len(lp.Deposits) - 1; i >= 0; i-- {
				deps = append(deps, lp.Deposits[i])
			}
			reds := make([]LPRedemption, 0, len(lp.Redemptions))
			for i := len(lp.Redemptions) - 1; i >= 0; i-- {
				reds = append(reds, lp.Redemptions[i])
			}
			v.Position = &LPPosition{
				Tag:            lp.Tag,
				XRPLAddress:    lp.XRPLAddress,
				SharesIssued:   lp.SharesIssued.String(),
				SharesRedeemed: lp.SharesRedeemed.String(),
				Deposits:       deps,
				Redemptions:    reds,
			}
		}
	}
	return v, nil
}

// vaultView summarises the pool at now without changing it. Callers hold a lock.
func (l *Ledger) vaultView(now int64) VaultResponse {
	p := l.pool
	idx := p.indexAt(now)
	treasury := p.treasuryAt(now)
	debt := debtOf(p.TotalDebtScaled, idx)
	assets := l.totalAssets(idx, treasury)
	price := sharePriceMicro(assets, p.TotalShares)

	var utilBps int64
	if assets.Sign() > 0 {
		u := new(big.Int).Mul(debt, big.NewInt(10000))
		utilBps = u.Div(u, assets).Int64()
		if utilBps > 10000 {
			utilBps = 10000
		}
	}
	// Supply rate = borrow rate × utilization × (1 − reserve factor).
	supplyBps := BorrowAPRBps * utilBps * (10000 - ReserveFactorBps) / 100_000_000

	// Internal (operator) vs LP liquidity, by the shares the operator was issued
	// and redeemed. Shares are transferable, so once the operator moves shares on
	// XRPL this split is approximate (clamped to the total).
	lpCount := 0
	for _, lp := range p.LPs {
		if lp.SharesIssued.Sign() > 0 {
			lpCount++ // a redeemer who only received shares by transfer is not counted
		}
	}
	operatorShares := big.NewInt(0)
	if tag, ok := p.LPAddrToTag[l.cfg.OperatorAccount]; ok {
		op := p.LPs[tag]
		operatorShares.Sub(op.SharesIssued, op.SharesRedeemed)
		if operatorShares.Sign() < 0 {
			operatorShares.SetInt64(0)
		}
		if operatorShares.Cmp(p.TotalShares) > 0 {
			operatorShares.Set(p.TotalShares)
		}
	}
	lpShares := new(big.Int).Sub(p.TotalShares, operatorShares)
	// Same conversion as a redemption, so the value is what the shares would pay out.
	value := func(shares *big.Int) string {
		v := new(big.Int).Mul(shares, new(big.Int).Add(assets, big.NewInt(1)))
		return formatRLUSD(v.Div(v, new(big.Int).Add(p.TotalShares, big.NewInt(1))))
	}
	interestNow := new(big.Int).Add(p.InterestEarned, new(big.Int).Sub(debt, debtOf(p.TotalDebtScaled, p.BorrowIndex)))
	reserveNow := new(big.Int).Sub(treasury, p.TreasuryOwed)
	reserveNow.Add(reserveNow, p.InterestReserve)

	return VaultResponse{
		TotalAssetsRlusd:       formatRLUSD(assets),
		CashRlusd:              formatRLUSD(p.Cash),
		FreeLiquidityRlusd:     formatRLUSD(l.freeLiquidity(treasury)),
		CardDebtRlusd:          formatRLUSD(debt),
		OwedToMerchantsRlusd:   formatRLUSD(l.owedToMerchants()),
		UtilizationBps:         utilBps,
		BorrowAPRBps:           BorrowAPRBps,
		SupplyAPRBps:           supplyBps,
		ReserveFactorBps:       ReserveFactorBps,
		CardFeeBps:             CardFeeBps,
		TotalShares:            p.TotalShares.String(),
		SharePrice:             formatRLUSD(price),
		InternalShares:         operatorShares.String(),
		InternalLiquidityRlusd: value(operatorShares),
		LPShares:               lpShares.String(),
		LPLiquidityRlusd:       value(lpShares),
		LPCount:                lpCount,
		InterestEarnedRlusd:    formatRLUSD(interestNow),
		InterestToLPsRlusd:     formatRLUSD(new(big.Int).Sub(interestNow, reserveNow)),
		TreasuryOwedRlusd:      formatRLUSD(l.owedToTreasury(treasury)),
		TreasuryPaidRlusd:      formatRLUSD(p.TreasuryPaid),
		FeesEarnedRlusd:        formatRLUSD(p.FeesEarned),
		InterestReserveRlusd:   formatRLUSD(reserveNow),
		LPDepositAccount:       l.cfg.LPDepositAccount,
		PoolAccount:            l.cfg.PoolAccount,
		TreasuryAccount:        l.cfg.TreasuryAccount,
		OperatorAccount:        l.cfg.OperatorAccount,
		ShareCurrency:          l.cfg.ShareCurrency,
	}
}
