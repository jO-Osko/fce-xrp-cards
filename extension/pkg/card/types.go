package card

import "encoding/json"

// Request is the JSON body for CARD ops (direct message or POST /card).
type Request struct {
	Command string `json:"command,omitempty"`

	Credential string `json:"credential,omitempty"`

	XRPLAddress string `json:"xrplAddress,omitempty"`
	Tag         uint32 `json:"tag,omitempty"`

	Drops  string `json:"drops,omitempty"`
	TxHash string `json:"txHash,omitempty"`

	RlusdAmount string `json:"rlusdAmount,omitempty"`
	Label       string `json:"label,omitempty"`

	SettleNonce string `json:"settleNonce,omitempty"`

	// Shares is an LP share amount in share units, 1e6 per displayed share (LP_REDEEM).
	Shares string `json:"shares,omitempty"`
}

type RegisterResponse struct {
	Tag            uint32 `json:"tag"`
	XRPLAddress    string `json:"xrplAddress"`
	DepositAccount string `json:"depositAccount"`
}

type PositionResponse struct {
	Tag                    uint32              `json:"tag"`
	XRPLAddress            string              `json:"xrplAddress"`
	XrpAvailableDrops      string              `json:"xrpAvailableDrops"`
	XrpLockedDrops         string              `json:"xrpLockedDrops"`
	RlusdDebt              string              `json:"rlusdDebt"`
	VirtualSettlementRlusd string              `json:"virtualSettlementRlusd"`
	PendingSettleRlusd     string              `json:"pendingSettleRlusd,omitempty"`
	PendingSettleNonce     string              `json:"pendingSettleNonce,omitempty"`
	LastSettleTx           string              `json:"lastSettleTx,omitempty"`
	XrpUsdPrice            string              `json:"xrpUsdPrice"`
	LtvCapBps              int64               `json:"ltvCapBps"`
	DepositAccount         string              `json:"depositAccount"`
	SettlementAddress      string              `json:"settlementAddress"`
	Deposits               []DepositRecord     `json:"deposits"`
	Payments               []CardPaymentRecord `json:"payments"`

	ConvertibleRlusd   string                    `json:"convertibleRlusd"`
	PendingConversions []PendingConversionRecord `json:"pendingConversions"`
	Repayments         []Repayment               `json:"repayments"`
	PoolAccount        string                    `json:"poolAccount,omitempty"`
	BorrowAPRBps       int64                     `json:"borrowAprBps"`
}

type CardPaymentRecord struct {
	Label          string `json:"label,omitempty"`
	RlusdAmount    string `json:"rlusdAmount"`
	XrpLockedDrops string `json:"xrpLockedDrops"`
	At             int64  `json:"at"`
}

type DepositRecord struct {
	TxHash string `json:"txHash"`
	Drops  string `json:"drops"`
	At     int64  `json:"at"`
}

type ReserveResponse struct {
	Approved          bool   `json:"approved"`
	RlusdAmount       string `json:"rlusdAmount"`
	XrpLockedDrops    string `json:"xrpLockedDrops,omitempty"`
	XrpAvailableLeft  string `json:"xrpAvailableLeft,omitempty"`
	RlusdDebt         string `json:"rlusdDebt,omitempty"`
	VirtualSettlement string `json:"virtualSettlementRlusd,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

type CreditDepositResponse struct {
	Tag               uint32 `json:"tag"`
	CreditedDrops     string `json:"creditedDrops"`
	XrpAvailableDrops string `json:"xrpAvailableDrops"`
	TxHash            string `json:"txHash"`
	Duplicate         bool   `json:"duplicate,omitempty"`
}

type SettleDailyResponse struct {
	SettleNonce       string `json:"settleNonce"`
	RlusdAmount       string `json:"rlusdAmount"`
	SettlementAddress string `json:"settlementAddress"`
	RLUSDCurrency     string `json:"rlusdCurrency"`
	RLUSDIssuer       string `json:"rlusdIssuer"`
	// TreasuryRlusd is TEE profit (card fees, interest reserve) to pay the treasury with this settlement.
	TreasuryRlusd   string `json:"treasuryRlusd,omitempty"`
	TreasuryAddress string `json:"treasuryAddress,omitempty"`
}

type SettleConfirmResponse struct {
	RlusdAmount   string `json:"rlusdAmount"`
	TreasuryRlusd string `json:"treasuryRlusd,omitempty"`
	TxHash        string `json:"txHash"`
	WireQueued    bool   `json:"wireQueued"`
}

// Envelope is the JSON wrapper for mock POST /card.
type Envelope struct {
	OK      bool            `json:"ok"`
	Command string          `json:"command"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// UserStat is one cardholder's line in the operator overview.
type UserStat struct {
	Tag               uint32 `json:"tag"`
	XRPLAddress       string `json:"xrplAddress"`
	XrpAvailableDrops string `json:"xrpAvailableDrops"`
	XrpLockedDrops    string `json:"xrpLockedDrops"`
	RlusdDebt         string `json:"rlusdDebt"`
	DepositsCount     int    `json:"depositsCount"`
	PaymentsCount     int    `json:"paymentsCount"`
	UtilizationBps    int64  `json:"utilizationBps"`
	LastActivityAt    int64  `json:"lastActivityAt"`
}

// StatsResponse is the card-operator overview: totals plus a per-user breakdown.
type StatsResponse struct {
	UserCount              int        `json:"userCount"`
	TotalXrpAvailableDrops string     `json:"totalXrpAvailableDrops"`
	TotalXrpLockedDrops    string     `json:"totalXrpLockedDrops"`
	TotalCollateralDrops   string     `json:"totalCollateralDrops"`
	TotalRlusdDebt         string     `json:"totalRlusdDebt"`
	TotalCardVolumeRlusd   string     `json:"totalCardVolumeRlusd"`
	VirtualSettlementRlusd string     `json:"virtualSettlementRlusd"`
	ProcessedTxCount       int        `json:"processedTxCount"`
	TotalCardPayments      int        `json:"totalCardPayments"`
	TotalDeposits          int        `json:"totalDeposits"`
	XrpUsdPrice            string     `json:"xrpUsdPrice"`
	LtvCapBps              int64      `json:"ltvCapBps"`
	DepositAccount         string     `json:"depositAccount"`
	SettlementAddress      string     `json:"settlementAddress"`
	Users                  []UserStat `json:"users"`

	// Vault is the lending pool: liquidity by source, rates, TEE profit.
	Vault VaultResponse `json:"vault"`
}

// LPRegisterResponse tells an LP where to send RLUSD: DepositAccount with Tag.
type LPRegisterResponse struct {
	Tag            uint32 `json:"tag"`
	XRPLAddress    string `json:"xrplAddress"`
	DepositAccount string `json:"depositAccount"`
	ShareCurrency  string `json:"shareCurrency"`
}

// LPDepositResponse is the shares to issue XRPLAddress for a deposit.
type LPDepositResponse struct {
	Tag           uint32 `json:"tag"`
	XRPLAddress   string `json:"xrplAddress"`
	TxHash        string `json:"txHash"`
	RlusdAmount   string `json:"rlusdAmount,omitempty"`
	Shares        string `json:"shares"`
	SharePrice    string `json:"sharePrice,omitempty"`
	ShareCurrency string `json:"shareCurrency"`
	Duplicate     bool   `json:"duplicate,omitempty"`
}

// LPRedeemResponse is the RLUSD to pay XRPLAddress for burned shares.
type LPRedeemResponse struct {
	XRPLAddress string `json:"xrplAddress"`
	TxHash      string `json:"txHash"`
	Shares      string `json:"shares"`
	RlusdAmount string `json:"rlusdAmount"`
	SharePrice  string `json:"sharePrice,omitempty"`
	Duplicate   bool   `json:"duplicate,omitempty"`
}

// LPPosition is one LP's records inside the TEE (its share balance lives on XRPL).
type LPPosition struct {
	Tag            uint32         `json:"tag"`
	XRPLAddress    string         `json:"xrplAddress"`
	SharesIssued   string         `json:"sharesIssued"`
	SharesRedeemed string         `json:"sharesRedeemed"`
	Deposits       []LPDeposit    `json:"deposits"`
	Redemptions    []LPRedemption `json:"redemptions"`
}

// VaultResponse is the lending pool's state (GET_VAULT).
type VaultResponse struct {
	TotalAssetsRlusd       string `json:"totalAssetsRlusd"`
	CashRlusd              string `json:"cashRlusd"`
	FreeLiquidityRlusd     string `json:"freeLiquidityRlusd"`
	CardDebtRlusd          string `json:"cardDebtRlusd"`
	OwedToMerchantsRlusd   string `json:"owedToMerchantsRlusd"`
	UtilizationBps         int64  `json:"utilizationBps"`
	BorrowAPRBps           int64  `json:"borrowAprBps"`
	SupplyAPRBps           int64  `json:"supplyAprBps"`
	ReserveFactorBps       int64  `json:"reserveFactorBps"`
	CardFeeBps             int64  `json:"cardFeeBps"`
	TotalShares            string `json:"totalShares"`
	SharePrice             string `json:"sharePrice"`
	InternalShares         string `json:"internalShares"`
	InternalLiquidityRlusd string `json:"internalLiquidityRlusd"`
	LPShares               string `json:"lpShares"`
	LPLiquidityRlusd       string `json:"lpLiquidityRlusd"`
	LPCount                int    `json:"lpCount"`
	InterestEarnedRlusd    string `json:"interestEarnedRlusd"`
	InterestToLPsRlusd     string `json:"interestToLpsRlusd"`
	TreasuryOwedRlusd      string `json:"treasuryOwedRlusd"`
	TreasuryPaidRlusd      string `json:"treasuryPaidRlusd"`
	FeesEarnedRlusd        string `json:"feesEarnedRlusd"`
	InterestReserveRlusd   string `json:"interestReserveRlusd"`
	LPDepositAccount       string `json:"lpDepositAccount"`
	PoolAccount            string `json:"poolAccount"`
	TreasuryAccount        string `json:"treasuryAccount"`
	OperatorAccount        string `json:"operatorAccount"`
	ShareCurrency          string `json:"shareCurrency"`

	Position *LPPosition `json:"position,omitempty"`
}

// StateSummary is returned by GET /state.
type StateSummary struct {
	UserCount              int    `json:"userCount"`
	VirtualSettlementRlusd string `json:"virtualSettlementRlusd"`
	ProcessedTxCount       int    `json:"processedTxCount"`
	PendingSettleNonce     string `json:"pendingSettleNonce,omitempty"`
	PendingSettleRlusd     string `json:"pendingSettleRlusd,omitempty"`
}
