// Package types contains types useful when interacting with this extension.
package types

import (
	"encoding/json"

	"github.com/ethereum/go-ethereum/common"
)

// CardRequest is the JSON body for POST /card (and OriginalMessage for /action).
type CardRequest struct {
	Command string `json:"command"`

	// Credential is required for RESERVE / SETTLE_DAILY / SETTLE_CONFIRM.
	Credential string `json:"credential,omitempty"`

	XRPLAddress string `json:"xrplAddress,omitempty"`
	Tag         uint32 `json:"tag,omitempty"`

	// CREDIT_DEPOSIT
	Drops  string `json:"drops,omitempty"`
	TxHash string `json:"txHash,omitempty"`

	// RESERVE — RLUSD amount as decimal string, e.g. "25.00"
	RlusdAmount string `json:"rlusdAmount,omitempty"`
	Label       string `json:"label,omitempty"`

	// SETTLE_CONFIRM
	SettleNonce string `json:"settleNonce,omitempty"`
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
	XrpUsdPrice            string              `json:"xrpUsdPrice"`
	LtvCapBps              int64               `json:"ltvCapBps"`
	DepositAccount         string              `json:"depositAccount"`
	SettlementAddress      string              `json:"settlementAddress"`
	Deposits               []DepositRecord     `json:"deposits"`
	Payments               []CardPaymentRecord `json:"payments"`
}

// CardPaymentRecord is one approved mock-card spend.
type CardPaymentRecord struct {
	Label          string `json:"label,omitempty"`
	RlusdAmount    string `json:"rlusdAmount"`
	XrpLockedDrops string `json:"xrpLockedDrops"`
	At             int64  `json:"at"`
}

// DepositRecord is one XRPL payment credited to the user's collateral.
type DepositRecord struct {
	TxHash string `json:"txHash"`
	Drops  string `json:"drops"`
	At     int64  `json:"at"`
}

type ReserveResponse struct {
	Approved          bool   `json:"approved"`
	RlusdAmount       string `json:"rlusdAmount"`
	XrpLockedDrops    string `json:"xrpLockedDrops"`
	XrpAvailableLeft  string `json:"xrpAvailableLeft"`
	RlusdDebt         string `json:"rlusdDebt"`
	VirtualSettlement string `json:"virtualSettlementRlusd"`
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
}

type SettleConfirmResponse struct {
	RlusdAmount string `json:"rlusdAmount"`
	TxHash      string `json:"txHash"`
	WireQueued  bool   `json:"wireQueued"`
}

// State holds the extension's observable state, returned by GET /state.
type State struct {
	UserCount              int    `json:"userCount"`
	VirtualSettlementRlusd string `json:"virtualSettlementRlusd"`
	ProcessedTxCount       int    `json:"processedTxCount"`
	PendingSettleNonce     string `json:"pendingSettleNonce,omitempty"`
	PendingSettleRlusd     string `json:"pendingSettleRlusd,omitempty"`
}

// StateResponse is the envelope returned by GET /state.
type StateResponse struct {
	StateVersion common.Hash `json:"stateVersion"`
	State        State       `json:"state"`
}

// CardResponse is the JSON envelope for POST /card.
type CardResponse struct {
	OK      bool            `json:"ok"`
	Command string          `json:"command"`
	Error   string          `json:"error,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}
