package card

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Account is the per-user virtual loan ledger entry, keyed by destination tag.
type Account struct {
	Tag         uint32 `json:"tag"`
	XRPLAddress string `json:"xrplAddress"`
	// Collateral is XRP in this demo. Any XRPL token (trust line or MPT, e.g.
	// RWA) works the same way with balances per asset and per-asset risk
	// parameters (README, "Collateral beyond XRP").
	XrpAvailable *big.Int `json:"xrpAvailable"`
	XrpLocked    *big.Int `json:"xrpLocked"`
	// DebtScaled is the RLUSD debt divided by the pool's borrow index, so it
	// grows with interest. (Stored as "rlusdDebt": with the index at 1 it equals
	// the debt, which keeps older snapshots readable.)
	DebtScaled *big.Int      `json:"rlusdDebt"`
	Deposits   []Deposit     `json:"deposits,omitempty"`
	Payments   []CardPayment `json:"payments,omitempty"`

	Conversions []PendingConversion `json:"conversions,omitempty"`
	Repayments  []Repayment         `json:"repayments,omitempty"`
}

// Deposit is one credited XRPL payment.
type Deposit struct {
	TxHash string `json:"txHash"`
	Drops  string `json:"drops"`
	At     int64  `json:"at"`
}

// CardPayment is one approved mock-card spend.
type CardPayment struct {
	Label          string `json:"label,omitempty"`
	RlusdAmount    string `json:"rlusdAmount"`
	XrpLockedDrops string `json:"xrpLockedDrops"`
	At             int64  `json:"at"`
}

// PendingSettle holds a prepared daily settlement until XRPL confirm.
type PendingSettle struct {
	Nonce string `json:"nonce"`
	// Amount goes to the merchants' settlement account, TreasuryAmount to the TEE treasury.
	Amount         *big.Int `json:"amount"`
	TreasuryAmount *big.Int `json:"treasuryAmount,omitempty"`
	Created        int64    `json:"created"`
}

func newAccount(tag uint32, addr string) *Account {
	return &Account{
		Tag:          tag,
		XRPLAddress:  addr,
		XrpAvailable: big.NewInt(0),
		XrpLocked:    big.NewInt(0),
		DebtScaled:   big.NewInt(0),
	}
}

// Ledger is the in-memory CARD state machine.
type Ledger struct {
	cfg Config

	mu     sync.RWMutex
	prices *PriceCache

	accounts    map[uint32]*Account
	addrToTag   map[string]uint32
	nextTag     uint32
	processedTx map[string]bool

	// virtualSettlement is card spend owed to merchants (net of the card fee)
	// since the last SETTLE_DAILY.
	virtualSettlement *big.Int
	pendingSettle     *PendingSettle
	lastSettleTx      string

	pool *Pool

	settleSeq     atomic.Uint64
	conversionSeq atomic.Uint64

	// now is the clock; tests replace it.
	now func() time.Time

	// Persist is an optional hook after mutating state (mock TEE snapshot).
	Persist func()
}

// NewLedger builds an empty ledger with cfg.
func NewLedger(cfg Config) *Ledger {
	if cfg.DefaultXRPUSD == "" {
		cfg.DefaultXRPUSD = "2.50"
	}
	if cfg.CardProcessorCredential == "" {
		cfg.CardProcessorCredential = "swell-demo-card-credential"
	}
	return &Ledger{
		cfg:               cfg,
		prices:            newPriceCache(cfg.DefaultXRPUSD),
		accounts:          map[uint32]*Account{},
		addrToTag:         map[string]uint32{},
		nextTag:           1000,
		processedTx:       map[string]bool{},
		virtualSettlement: big.NewInt(0),
		pool:              newPool(),
		now:               time.Now,
	}
}

// Config returns a copy of the ledger config.
func (l *Ledger) Config() Config { return l.cfg }

// SetPrice sets the XRP/USD price (8-decimal fixed). Useful in tests.
func (l *Ledger) SetPrice(priceE8 *big.Int) { l.prices.Set(priceE8) }

func (l *Ledger) persist() {
	if l.Persist != nil {
		l.Persist()
	}
}

func (l *Ledger) requireCredential(cred string) error {
	if cred == "" || cred != l.cfg.CardProcessorCredential {
		return fmt.Errorf("invalid card processor credential")
	}
	return nil
}

// Dispatch runs a CARD command from req.Command (or fails if empty).
func (l *Ledger) Dispatch(req Request) (any, error) {
	cmd := strings.ToUpper(strings.TrimSpace(req.Command))
	switch cmd {
	case CmdRegister:
		return l.Register(req)
	case CmdCreditDeposit:
		return l.CreditDeposit(req)
	case CmdReserve:
		return l.Reserve(req)
	case CmdGetPosition:
		return l.GetPosition(req)
	case CmdStats:
		return l.OperatorStats(), nil
	case CmdSettleDaily:
		return l.SettleDaily(req)
	case CmdSettleConfirm:
		return l.SettleConfirm(req)
	case CmdConvertRequest:
		return l.ConvertRequest(req)
	case CmdCreditRLUSDRepayment:
		return l.CreditRLUSDRepayment(req)
	case CmdLPRegister:
		return l.LPRegister(req)
	case CmdLPDeposit:
		return l.LPDeposit(req)
	case CmdLPRedeem:
		return l.LPRedeem(req)
	case CmdGetVault:
		return l.GetVault(req)
	default:
		return nil, fmt.Errorf("unknown command: %s", req.Command)
	}
}

// Register allocates a destination tag for an XRPL classic address.
func (l *Ledger) Register(req Request) (any, error) {
	addr := strings.TrimSpace(req.XRPLAddress)
	if addr == "" || !strings.HasPrefix(addr, "r") {
		return nil, fmt.Errorf("xrplAddress required (classic address starting with r)")
	}

	l.mu.Lock()
	if tag, ok := l.addrToTag[addr]; ok {
		resp := RegisterResponse{
			Tag:            tag,
			XRPLAddress:    addr,
			DepositAccount: l.cfg.DepositAccount,
		}
		l.mu.Unlock()
		return resp, nil
	}

	l.nextTag++
	tag := l.nextTag
	l.accounts[tag] = newAccount(tag, addr)
	l.addrToTag[addr] = tag
	resp := RegisterResponse{
		Tag:            tag,
		XRPLAddress:    addr,
		DepositAccount: l.cfg.DepositAccount,
	}
	l.mu.Unlock()
	// Persist after unlock — Persist/MarshalSnapshot takes RLock.
	l.persist()
	return resp, nil
}

// CreditDeposit credits XRP drops for a tagged account (idempotent on txHash).
func (l *Ledger) CreditDeposit(req Request) (any, error) {
	if req.Tag == 0 {
		return nil, fmt.Errorf("tag required")
	}
	drops, err := parseDrops(req.Drops)
	if err != nil {
		return nil, err
	}
	if drops.Sign() <= 0 {
		return nil, fmt.Errorf("drops must be positive")
	}
	txHash := strings.TrimSpace(req.TxHash)
	if txHash == "" {
		return nil, fmt.Errorf("txHash required")
	}

	l.mu.Lock()

	if _, seen := l.processedTx[txHash]; seen {
		acc := l.accounts[req.Tag]
		avail := "0"
		if acc != nil {
			avail = acc.XrpAvailable.String()
		}
		l.mu.Unlock()
		return CreditDepositResponse{
			Tag:               req.Tag,
			CreditedDrops:     "0",
			XrpAvailableDrops: avail,
			TxHash:            txHash,
			Duplicate:         true,
		}, nil
	}

	acc, ok := l.accounts[req.Tag]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("unknown tag %d; register first", req.Tag)
	}

	acc.XrpAvailable.Add(acc.XrpAvailable, drops)
	acc.Deposits = append(acc.Deposits, Deposit{
		TxHash: txHash,
		Drops:  drops.String(),
		At:     l.now().Unix(),
	})
	l.processedTx[txHash] = true
	resp := CreditDepositResponse{
		Tag:               req.Tag,
		CreditedDrops:     drops.String(),
		XrpAvailableDrops: acc.XrpAvailable.String(),
		TxHash:            txHash,
	}
	l.mu.Unlock()
	l.persist()
	return resp, nil
}

// Reserve is a card payment: it locks XRP collateral and borrows RLUSD from
// the pool. The merchant is owed the amount less CardFeeBps, which goes to the
// TEE treasury; the cardholder owes the full amount, plus interest from now.
func (l *Ledger) Reserve(req Request) (any, error) {
	if err := l.requireCredential(req.Credential); err != nil {
		return nil, err
	}
	if req.Tag == 0 {
		return nil, fmt.Errorf("tag required")
	}
	debtAdd, err := parseRLUSD(req.RlusdAmount)
	if err != nil {
		return nil, err
	}

	priceE8, ok := l.prices.Get()
	if !ok {
		return nil, fmt.Errorf("no XRP/USD price")
	}

	l.mu.Lock()

	acc, ok := l.accounts[req.Tag]
	if !ok {
		l.mu.Unlock()
		return nil, fmt.Errorf("unknown tag %d", req.Tag)
	}

	l.pool.accrue(l.now().Unix())
	// Demo: the operator keeps the pool funded (it can always mint more), so
	// running out of liquidity is not a business decline but a revert.
	if debtAdd.Cmp(l.freeLiquidity(l.pool.TreasuryOwed)) > 0 {
		l.mu.Unlock()
		return nil, fmt.Errorf("insufficient pool liquidity")
	}

	lockDrops := collateralDropsForDebt(debtAdd, priceE8, LTVCapBps)
	if acc.XrpAvailable.Cmp(lockDrops) < 0 {
		resp := ReserveResponse{
			Approved: false,
			Reason: fmt.Sprintf(
				"insufficient available XRP: need %s drops, have %s",
				lockDrops.String(), acc.XrpAvailable.String(),
			),
			RlusdAmount: formatRLUSD(debtAdd),
		}
		l.mu.Unlock()
		return resp, nil
	}

	// Locking moves XRP from available to locked, so total collateral is unchanged.
	newDebt := new(big.Int).Add(l.debtNow(acc), debtAdd)
	collatUSD := xrpDropsUSD(new(big.Int).Add(acc.XrpAvailable, acc.XrpLocked), priceE8)
	if ltvBreached(newDebt, collatUSD, LTVCapBps) {
		resp := ReserveResponse{
			Approved:    false,
			Reason:      "would breach LTV cap",
			RlusdAmount: formatRLUSD(debtAdd),
		}
		l.mu.Unlock()
		return resp, nil
	}

	acc.XrpAvailable.Sub(acc.XrpAvailable, lockDrops)
	acc.XrpLocked.Add(acc.XrpLocked, lockDrops)
	l.addDebt(acc, debtAdd)
	fee := new(big.Int).Mul(debtAdd, big.NewInt(CardFeeBps))
	fee.Div(fee, big.NewInt(10000))
	l.virtualSettlement.Add(l.virtualSettlement, new(big.Int).Sub(debtAdd, fee))
	l.pool.TreasuryOwed.Add(l.pool.TreasuryOwed, fee)
	l.pool.FeesEarned.Add(l.pool.FeesEarned, fee)

	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "Card payment"
	}
	acc.Payments = append(acc.Payments, CardPayment{
		Label:          label,
		RlusdAmount:    formatRLUSD(debtAdd),
		XrpLockedDrops: lockDrops.String(),
		At:             l.now().Unix(),
	})
	resp := ReserveResponse{
		Approved:          true,
		RlusdAmount:       formatRLUSD(debtAdd),
		XrpLockedDrops:    lockDrops.String(),
		XrpAvailableLeft:  acc.XrpAvailable.String(),
		RlusdDebt:         formatRLUSD(l.debtNow(acc)),
		VirtualSettlement: formatRLUSD(l.virtualSettlement),
	}
	l.mu.Unlock()
	l.persist()
	return resp, nil
}

// GetPosition returns balances for a tag or XRPL address.
func (l *Ledger) GetPosition(req Request) (any, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var acc *Account
	switch {
	case req.Tag != 0:
		acc = l.accounts[req.Tag]
	case req.XRPLAddress != "":
		if tag, ok := l.addrToTag[req.XRPLAddress]; ok {
			acc = l.accounts[tag]
		}
	default:
		return nil, fmt.Errorf("tag or xrplAddress required")
	}
	if acc == nil {
		return nil, fmt.Errorf("account not found")
	}

	deposits := make([]DepositRecord, 0, len(acc.Deposits))
	for i := len(acc.Deposits) - 1; i >= 0; i-- {
		d := acc.Deposits[i]
		deposits = append(deposits, DepositRecord{TxHash: d.TxHash, Drops: d.Drops, At: d.At})
	}
	payments := make([]CardPaymentRecord, 0, len(acc.Payments))
	for i := len(acc.Payments) - 1; i >= 0; i-- {
		p := acc.Payments[i]
		payments = append(payments, CardPaymentRecord{
			Label: p.Label, RlusdAmount: p.RlusdAmount, XrpLockedDrops: p.XrpLockedDrops, At: p.At,
		})
	}

	pendingAmt := ""
	pendingNonce := ""
	if l.pendingSettle != nil {
		pendingAmt = formatRLUSD(l.pendingSettle.Amount)
		pendingNonce = l.pendingSettle.Nonce
	}

	now := l.now().Unix()
	debt := debtOf(acc.DebtScaled, l.pool.indexAt(now))
	conversions, reserved := acc.activeConversions(now)
	convertible := new(big.Int).Sub(debt, reserved)
	if convertible.Sign() < 0 {
		convertible.SetInt64(0)
	}
	repayments := make([]Repayment, 0, len(acc.Repayments))
	for i := len(acc.Repayments) - 1; i >= 0; i-- {
		repayments = append(repayments, acc.Repayments[i])
	}

	return PositionResponse{
		Tag:                    acc.Tag,
		XRPLAddress:            acc.XRPLAddress,
		XrpAvailableDrops:      acc.XrpAvailable.String(),
		XrpLockedDrops:         acc.XrpLocked.String(),
		RlusdDebt:              formatRLUSD(debt),
		VirtualSettlementRlusd: formatRLUSD(l.virtualSettlement),
		PendingSettleRlusd:     pendingAmt,
		PendingSettleNonce:     pendingNonce,
		LastSettleTx:           l.lastSettleTx,
		XrpUsdPrice:            l.prices.Format(),
		LtvCapBps:              LTVCapBps,
		DepositAccount:         l.cfg.DepositAccount,
		SettlementAddress:      l.cfg.SettlementAddress,
		Deposits:               deposits,
		Payments:               payments,
		ConvertibleRlusd:       formatRLUSD(convertible),
		PendingConversions:     conversions,
		Repayments:             repayments,
		PoolAccount:            l.cfg.PoolAccount,
		BorrowAPRBps:           BorrowAPRBps,
	}, nil
}

func (l *Ledger) settleDailyResponse(p *PendingSettle) SettleDailyResponse {
	resp := SettleDailyResponse{
		SettleNonce:       p.Nonce,
		RlusdAmount:       formatRLUSD(p.Amount),
		SettlementAddress: l.cfg.SettlementAddress,
		RLUSDCurrency:     RLUSDCurrencyHex,
		RLUSDIssuer:       RLUSDIssuer,
	}
	if p.TreasuryAmount != nil && p.TreasuryAmount.Sign() > 0 {
		resp.TreasuryRlusd = formatRLUSD(p.TreasuryAmount)
		resp.TreasuryAddress = l.cfg.TreasuryAccount
	}
	return resp
}

// SettleDaily freezes what is owed to merchants and to the TEE treasury into
// one pending settlement.
func (l *Ledger) SettleDaily(req Request) (any, error) {
	if err := l.requireCredential(req.Credential); err != nil {
		return nil, err
	}
	if l.cfg.SettlementAddress == "" {
		return nil, fmt.Errorf("XRPL_SETTLEMENT_ADDRESS not configured")
	}

	l.mu.Lock()

	if l.pendingSettle != nil {
		resp := l.settleDailyResponse(l.pendingSettle)
		l.mu.Unlock()
		return resp, nil
	}

	l.pool.accrue(l.now().Unix())
	treasury := new(big.Int).Set(l.pool.TreasuryOwed)
	if l.cfg.TreasuryAccount == "" {
		// Without a treasury account the profit stays owed until one is configured.
		treasury.SetInt64(0)
	}
	if l.virtualSettlement.Sign() <= 0 && treasury.Sign() <= 0 {
		l.mu.Unlock()
		return nil, fmt.Errorf("nothing to settle")
	}

	// Deterministic sequence so conformance fixtures can pin the nonce.
	nonce := fmt.Sprintf("%016x", l.settleSeq.Add(1))
	l.pendingSettle = &PendingSettle{
		Nonce:          nonce,
		Amount:         new(big.Int).Set(l.virtualSettlement),
		TreasuryAmount: treasury,
		Created:        l.now().Unix(),
	}
	l.virtualSettlement = big.NewInt(0)
	l.pool.TreasuryOwed.Sub(l.pool.TreasuryOwed, treasury)
	resp := l.settleDailyResponse(l.pendingSettle)
	l.mu.Unlock()
	l.persist()
	return resp, nil
}

// SettleConfirm clears a pending settlement after the on-chain RLUSD payments:
// the pool's cash goes down by the merchants' and the treasury's amounts.
func (l *Ledger) SettleConfirm(req Request) (any, error) {
	if err := l.requireCredential(req.Credential); err != nil {
		return nil, err
	}
	txHash := strings.TrimSpace(req.TxHash)
	if txHash == "" {
		return nil, fmt.Errorf("txHash required")
	}
	if strings.TrimSpace(req.SettleNonce) == "" {
		return nil, fmt.Errorf("settleNonce required")
	}

	l.mu.Lock()

	if l.pendingSettle == nil {
		l.mu.Unlock()
		return nil, fmt.Errorf("no pending settlement")
	}
	if l.pendingSettle.Nonce != req.SettleNonce {
		l.mu.Unlock()
		return nil, fmt.Errorf("settleNonce mismatch")
	}

	p := l.pendingSettle
	l.pool.Cash.Sub(l.pool.Cash, p.Amount)
	resp := SettleConfirmResponse{
		RlusdAmount: formatRLUSD(p.Amount),
		TxHash:      txHash,
		WireQueued:  true,
	}
	if p.TreasuryAmount != nil && p.TreasuryAmount.Sign() > 0 {
		l.pool.Cash.Sub(l.pool.Cash, p.TreasuryAmount)
		l.pool.TreasuryPaid.Add(l.pool.TreasuryPaid, p.TreasuryAmount)
		resp.TreasuryRlusd = formatRLUSD(p.TreasuryAmount)
	}
	l.lastSettleTx = txHash
	l.pendingSettle = nil
	l.mu.Unlock()
	l.persist()
	return resp, nil
}

// Summary returns observable state for GET /state.
func (l *Ledger) Summary() StateSummary {
	l.mu.RLock()
	defer l.mu.RUnlock()
	nonce := ""
	pendingAmt := ""
	if l.pendingSettle != nil {
		nonce = l.pendingSettle.Nonce
		pendingAmt = formatRLUSD(l.pendingSettle.Amount)
	}
	return StateSummary{
		UserCount:              len(l.accounts),
		VirtualSettlementRlusd: formatRLUSD(l.virtualSettlement),
		ProcessedTxCount:       len(l.processedTx),
		PendingSettleNonce:     nonce,
		PendingSettleRlusd:     pendingAmt,
	}
}

// OperatorStats returns the card-operator overview: aggregate totals across all
// cardholders, a per-user breakdown sorted by tag, and the lending pool.
func (l *Ledger) OperatorStats() StatsResponse {
	l.mu.RLock()
	defer l.mu.RUnlock()

	now := l.now().Unix()
	idx := l.pool.indexAt(now)
	priceE8, _ := l.prices.Get()
	totalAvail := big.NewInt(0)
	totalLocked := big.NewInt(0)
	totalDebt := big.NewInt(0)
	totalVolume := big.NewInt(0)
	totalPayments := 0
	totalDeposits := 0

	users := make([]UserStat, 0, len(l.accounts))
	for _, acc := range l.accounts {
		debt := debtOf(acc.DebtScaled, idx)
		totalAvail.Add(totalAvail, acc.XrpAvailable)
		totalLocked.Add(totalLocked, acc.XrpLocked)
		totalDebt.Add(totalDebt, debt)
		totalPayments += len(acc.Payments)
		totalDeposits += len(acc.Deposits)

		var last int64
		for _, d := range acc.Deposits {
			if d.At > last {
				last = d.At
			}
		}
		for _, p := range acc.Payments {
			if p.At > last {
				last = p.At
			}
			if amt, err := parseRLUSD(p.RlusdAmount); err == nil {
				totalVolume.Add(totalVolume, amt)
			}
		}

		// Utilization = debt / (collateral value in USD * LTV cap).
		collateral := new(big.Int).Add(acc.XrpAvailable, acc.XrpLocked)
		var utilBps int64
		if priceE8 != nil {
			maxDebt := xrpDropsUSD(collateral, priceE8)
			maxDebt.Mul(maxDebt, big.NewInt(LTVCapBps))
			maxDebt.Div(maxDebt, big.NewInt(10000))
			if maxDebt.Sign() > 0 {
				u := new(big.Int).Mul(debt, big.NewInt(10000))
				u.Div(u, maxDebt)
				utilBps = u.Int64()
			}
		}

		users = append(users, UserStat{
			Tag:               acc.Tag,
			XRPLAddress:       acc.XRPLAddress,
			XrpAvailableDrops: acc.XrpAvailable.String(),
			XrpLockedDrops:    acc.XrpLocked.String(),
			RlusdDebt:         formatRLUSD(debt),
			DepositsCount:     len(acc.Deposits),
			PaymentsCount:     len(acc.Payments),
			UtilizationBps:    utilBps,
			LastActivityAt:    last,
		})
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Tag < users[j].Tag })

	return StatsResponse{
		UserCount:              len(l.accounts),
		TotalXrpAvailableDrops: totalAvail.String(),
		TotalXrpLockedDrops:    totalLocked.String(),
		TotalCollateralDrops:   new(big.Int).Add(totalAvail, totalLocked).String(),
		TotalRlusdDebt:         formatRLUSD(totalDebt),
		TotalCardVolumeRlusd:   formatRLUSD(totalVolume),
		VirtualSettlementRlusd: formatRLUSD(l.virtualSettlement),
		ProcessedTxCount:       len(l.processedTx),
		TotalCardPayments:      totalPayments,
		TotalDeposits:          totalDeposits,
		XrpUsdPrice:            l.prices.Format(),
		LtvCapBps:              LTVCapBps,
		DepositAccount:         l.cfg.DepositAccount,
		SettlementAddress:      l.cfg.SettlementAddress,
		Users:                  users,
		Vault:                  l.vaultView(now),
	}
}

// Snapshot is the ledger state the mock TEE persists between restarts.
type Snapshot struct {
	NextTag           uint32              `json:"nextTag"`
	Accounts          map[uint32]*Account `json:"accounts"`
	AddrToTag         map[string]uint32   `json:"addrToTag"`
	ProcessedTx       map[string]bool     `json:"processedTx"`
	VirtualSettlement string              `json:"virtualSettlement"`
	PendingSettle     *PendingSettle      `json:"pendingSettle,omitempty"`
	LastSettleTx      string              `json:"lastSettleTx,omitempty"`
	SettleSeq         uint64              `json:"settleSeq,omitempty"`
	ConversionSeq     uint64              `json:"conversionSeq,omitempty"`
	Pool              *Pool               `json:"pool,omitempty"`
}

func (l *Ledger) snapshot() Snapshot {
	return Snapshot{
		NextTag:           l.nextTag,
		Accounts:          l.accounts,
		AddrToTag:         l.addrToTag,
		ProcessedTx:       l.processedTx,
		VirtualSettlement: l.virtualSettlement.String(),
		PendingSettle:     l.pendingSettle,
		LastSettleTx:      l.lastSettleTx,
		SettleSeq:         l.settleSeq.Load(),
		ConversionSeq:     l.conversionSeq.Load(),
		Pool:              l.pool,
	}
}

// MarshalSnapshot serializes the ledger state under the read lock, so no
// handler can change it mid-encoding.
func (l *Ledger) MarshalSnapshot() ([]byte, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return json.MarshalIndent(l.snapshot(), "", "  ")
}

// ImportSnapshot restores ledger state (caller holds no lock). It validates
// before changing anything, so a bad snapshot leaves the ledger as it was.
func (l *Ledger) ImportSnapshot(s Snapshot) error {
	vs := new(big.Int)
	if s.VirtualSettlement != "" {
		if _, ok := vs.SetString(s.VirtualSettlement, 10); !ok {
			return fmt.Errorf("bad virtualSettlement")
		}
	}
	settleSeq := s.SettleSeq
	if p := s.PendingSettle; p != nil {
		if p.Amount == nil {
			return fmt.Errorf("pending settlement without an amount")
		}
		// Older snapshots have no counter: never reissue the pending nonce.
		if n, err := strconv.ParseUint(p.Nonce, 16, 64); err == nil && n > settleSeq {
			settleSeq = n
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextTag = s.NextTag
	l.accounts = s.Accounts
	if l.accounts == nil {
		l.accounts = map[uint32]*Account{}
	}
	l.pool = s.Pool
	if l.pool == nil {
		l.pool = newPool()
	}
	l.pool.fillDefaults()
	recount := s.Pool == nil
	for _, acc := range l.accounts {
		if acc.XrpAvailable == nil {
			acc.XrpAvailable = big.NewInt(0)
		}
		if acc.XrpLocked == nil {
			acc.XrpLocked = big.NewInt(0)
		}
		if acc.DebtScaled == nil {
			acc.DebtScaled = big.NewInt(0)
		}
		if recount {
			// A snapshot from before the pool: debt was plain RLUSD, i.e. scaled at
			// index 1. Its pool cash is unknown and starts at 0: make an operator
			// deposit before card payments, or start from a fresh snapshot.
			l.pool.TotalDebtScaled.Add(l.pool.TotalDebtScaled, acc.DebtScaled)
		}
	}
	l.addrToTag = s.AddrToTag
	if l.addrToTag == nil {
		l.addrToTag = map[string]uint32{}
	}
	l.processedTx = s.ProcessedTx
	if l.processedTx == nil {
		l.processedTx = map[string]bool{}
	}
	l.virtualSettlement = vs
	l.pendingSettle = s.PendingSettle
	l.lastSettleTx = s.LastSettleTx
	l.settleSeq.Store(settleSeq)
	l.conversionSeq.Store(s.ConversionSeq)
	return nil
}
