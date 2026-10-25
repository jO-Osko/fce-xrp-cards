// Package app is the wallets example: the TEE signs EVM transactions with a
// wallet key it holds, for the signer the wallet's project owner names in the
// contract, which numbers each wallet's transactions per chain.
package app

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/go-flare-common/pkg/tee/structs/wallet"
)

// The extension's op pair.
const (
	// OPTypeWallets names the extension.
	OPTypeWallets op.Type = "WALLETS"
	// OPCommandSignTransaction arrives as an instruction, sent through the
	// contract; ABI in, a signed transaction out.
	OPCommandSignTransaction op.Command = "SIGN_TRANSACTION"
)

// Register registers the extension's handler on s, signing with keys.
func Register(s *extension.Server, keys Keys) {
	w := &wallets{keys: keys}
	s.RegisterInstructionHandler(OPTypeWallets, OPCommandSignTransaction, w.signTransaction)
}

// Keys reads and signs with the wallet keys the TEE holds, as *node.Client
// does through the sign port.
type Keys interface {
	KeyInfo(ctx context.Context, walletID common.Hash, keyID uint64) (*wallet.IWalletKeyManagerKeyExistence, error)
	SignWallet(ctx context.Context, walletID common.Hash, keyID uint64, msg []byte) ([]byte, error)
}

// wallets keeps no state: the chain numbers the transactions, and signing is
// deterministic, so an instruction arriving again signs the same bytes.
type wallets struct {
	keys Keys
}
