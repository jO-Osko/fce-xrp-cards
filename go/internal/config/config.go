// Package config contains configuration values and defaults used by the extension.
package config

import (
	"os"
	"strconv"
	"time"
)

const (
	Version = "0.2.0"

	OPTypeCard = "CARD"

	OPCommandRegister      = "REGISTER"
	OPCommandCreditDeposit = "CREDIT_DEPOSIT"
	OPCommandReserve       = "RESERVE"
	OPCommandGetPosition   = "GET_POSITION"
	OPCommandSettleDaily   = "SETTLE_DAILY"
	OPCommandSettleConfirm = "SETTLE_CONFIRM"

	TimeoutShutdown = 5 * time.Second

	// LTVCapBps is max loan-to-value in basis points (7500 = 75%).
	LTVCapBps = 7500

	// PriceTTL is how long a cached XRP/USD price is considered fresh.
	PriceTTL = 120 * time.Second

	PricePollInterval = 45 * time.Second

	// DropsPerXRP is XRPL's native subdivision.
	DropsPerXRP = 1_000_000

	// RLUSDDecimals is the fixed-point scale used in the virtual ledger.
	RLUSDDecimals = 6
)

const DefaultStateSnapshotPath = "/tmp/card-state.json"

func StateSnapshotPath() string {
	if v := os.Getenv("STATE_SNAPSHOT_PATH"); v != "" {
		return v
	}
	return DefaultStateSnapshotPath
}

var (
	ExtensionPort = 8080
	SignPort      = 9090
	// Ledger settings (credential, XRPL accounts, fallback price) come from
	// card.DefaultConfig, which reads the same env vars as the FCC extension.
)

func init() {
	if ep := os.Getenv("EXTENSION_PORT"); ep != "" {
		if v, err := strconv.Atoi(ep); err == nil {
			ExtensionPort = v
		}
	}
	if sp := os.Getenv("SIGN_PORT"); sp != "" {
		if v, err := strconv.Atoi(sp); err == nil {
			SignPort = v
		}
	}
}
