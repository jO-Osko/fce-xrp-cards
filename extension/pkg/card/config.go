// Package card is the XRPL credit-card virtual ledger shared by the FCC
// template (direct actions) and the mock TEE (POST /card).
package card

import (
	"os"
	"time"
)

const (
	Version = "0.2.0"

	OPType = "CARD"

	CmdRegister      = "REGISTER"
	CmdCreditDeposit = "CREDIT_DEPOSIT"
	CmdReserve       = "RESERVE"
	CmdGetPosition   = "GET_POSITION"
	CmdStats         = "GET_STATS"
	CmdSettleDaily   = "SETTLE_DAILY"
	CmdSettleConfirm = "SETTLE_CONFIRM"

	CmdLPRegister = "LP_REGISTER"
	CmdLPDeposit  = "LP_DEPOSIT"
	CmdLPRedeem   = "LP_REDEEM"
	CmdGetVault   = "GET_VAULT"

	CmdConvertRequest       = "CONVERT_REQUEST"
	CmdCreditRLUSDRepayment = "CREDIT_RLUSD_REPAYMENT"

	// LTVCapBps is max loan-to-value in basis points (7500 = 75%).
	// One cap because the only collateral is XRP. Collateral can be any XRPL
	// token (trust line or MPT); with RWA collateral (tokenized treasuries, money
	// funds) this becomes a per-asset risk model: LTV, haircut and limits per
	// asset (README, "Collateral beyond XRP").
	LTVCapBps = 7500

	PriceTTL = 120 * time.Second

	// Lending pool. Fixed rates keep the demo simple.
	// BorrowAPRBps is the interest card debt accrues (12% a year).
	BorrowAPRBps = 1200
	// ReserveFactorBps is the TEE treasury's cut of that interest; LPs get the rest.
	ReserveFactorBps = 1000
	// CardFeeBps is the merchant fee on each card payment, paid to the TEE treasury.
	CardFeeBps = 100
	// InterestAccrualPeriod is the step interest accrues in.
	InterestAccrualPeriod = time.Minute
	// LPTagBase: LP deposit tags start above it, apart from card tags (1001+).
	LPTagBase = 500000

	// ConversionTTL is how long a CONVERT_REQUEST reserves debt before it lapses.
	ConversionTTL     = 15 * time.Minute
	PricePollInterval = 45 * time.Second
	RLUSDDecimals     = 6

	// RLUSDCurrencyHex is "RLUSD" as an XRPL currency code; the issuer is generated (defaults_gen.go).
	RLUSDCurrencyHex = "524C555344000000000000000000000000000000"
)

// Config holds runtime settings read from the environment.
type Config struct {
	CardProcessorCredential string
	DepositAccount          string
	SettlementAddress       string
	DefaultXRPUSD           string

	// Loan conversion: RLUSD repayments arrive at PoolAccount with the user's tag.
	PoolAccount         string
	CreditServerAccount string
	LoanBrokerID        string

	// Lending pool: LPs deposit RLUSD at LPDepositAccount, which also issues the
	// shares as a trust-line token with currency code ShareCurrency; TEE profit goes to TreasuryAccount; OperatorAccount is
	// the operator's own LP position (the "internal" liquidity).
	LPDepositAccount string
	TreasuryAccount  string
	OperatorAccount  string
	ShareCurrency    string
}

//go:generate node ../../../scripts/sync-config.mjs

// DefaultConfig returns config with demo defaults, overridden by env.
func DefaultConfig() Config {
	c := Config{
		CardProcessorCredential: "swell-demo-card-credential",
		// XRPL accounts generated from watcher/wallets.json (defaults_gen.go).
		DepositAccount:    DefaultDepositAccount,
		SettlementAddress: DefaultSettlementAddress,
		DefaultXRPUSD:     "2.50",

		PoolAccount:         DefaultPoolAccount,
		CreditServerAccount: DefaultCreditServerAccount,
		LoanBrokerID:        DefaultLoanBrokerID,

		LPDepositAccount: DefaultLPDepositAccount,
		TreasuryAccount:  DefaultTreasuryAccount,
		OperatorAccount:  DefaultOperatorAccount,
		ShareCurrency:    DefaultShareCurrency,
	}
	if v := os.Getenv("CARD_PROCESSOR_CREDENTIAL"); v != "" {
		c.CardProcessorCredential = v
	}
	if v := os.Getenv("XRPL_DEPOSIT_ACCOUNT"); v != "" {
		c.DepositAccount = v
	}
	if v := os.Getenv("XRPL_SETTLEMENT_ADDRESS"); v != "" {
		c.SettlementAddress = v
	}
	if v := os.Getenv("DEFAULT_XRP_USD"); v != "" {
		c.DefaultXRPUSD = v
	}
	return c
}
