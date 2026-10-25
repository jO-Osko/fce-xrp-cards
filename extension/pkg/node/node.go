package node

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/fce-go-scaffold/pkg/internal/signport"
	"github.com/flare-foundation/go-flare-common/pkg/tee/structs/wallet"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// ErrBadRequest means the node refused the input, e.g. an empty message or a
// ciphertext the TEE key can't decrypt.
// Errors never include the node's response body, which may echo inputs.
var ErrBadRequest = signport.ErrBadRequest

// ErrNotFound means the TEE doesn't hold the wallet key asked for.
var ErrNotFound = signport.ErrNotFound

// teeAddressProbe is fixed and never a 96-byte node preimage, so its signature is valid for nothing else.
const teeAddressProbe = "FCC_EXTENSION_TEE_ADDRESS_PROBE_V1"

// Client calls the sign port. It is safe for concurrent use.
// Every call retries while the node isn't listening yet, until ctx ends.
type Client struct {
	sp *signport.Client
}

// New returns a Client for the sign port on 127.0.0.1:port.
func New(port int) *Client {
	return &Client{sp: signport.New(port)}
}

// SignTEE signs msg with the TEE key: EIP-191 over keccak256(msg), V 0 or 1.
// The node signs its own data the same way, so never let a user choose msg.
func (c *Client) SignTEE(ctx context.Context, msg []byte) ([]byte, error) {
	var res types.SignResponse
	if err := c.sp.Call(ctx, "/sign", types.SignRequest{Message: msg}, &res); err != nil {
		return nil, fmt.Errorf("signing with the TEE key: %w", err)
	}
	if len(res.Signature) != crypto.SignatureLength {
		return nil, fmt.Errorf("signing with the TEE key: %w", signport.ErrInvalidResponse)
	}
	return res.Signature, nil
}

// Decrypt decrypts an ECIES ciphertext encrypted to the TEE's public key.
func (c *Client) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	var res types.DecryptResponse
	if err := c.sp.Call(ctx, "/decrypt", types.DecryptRequest{EncryptedMessage: ciphertext}, &res); err != nil {
		return nil, fmt.Errorf("decrypting with the TEE key: %w", err)
	}
	return res.DecryptedMessage, nil
}

// TEEAddress returns the TEE key's address, the teeId, recovered from a signed
// fixed probe; the node has no endpoint for it.
// Call it once at startup and reuse the result.
func (c *Client) TEEAddress(ctx context.Context) (common.Address, error) {
	probe := []byte(teeAddressProbe)
	sig, err := c.SignTEE(ctx, probe)
	if err != nil {
		return common.Address{}, err
	}
	a, err := recoverSigner(probe, sig)
	if err != nil {
		return common.Address{}, fmt.Errorf("recovering the TEE address: %w", err)
	}
	return a, nil
}

// KeyInfo returns what the TEE knows of a wallet key it holds: its type,
// signing algorithm and public key (64 bytes, X‖Y), and the wallet's admins
// and cosigners. The node keeps no record of which extension or user may use it.
func (c *Client) KeyInfo(ctx context.Context, walletID common.Hash, keyID uint64) (*wallet.IWalletKeyManagerKeyExistence, error) {
	var res wallet.IWalletKeyManagerKeyExistence
	if err := c.sp.Get(ctx, keyPath("/key-info", walletID, keyID), &res); err != nil {
		return nil, fmt.Errorf("fetching the wallet key's info: %w", err)
	}
	return &res, nil
}

// SignWallet signs msg with a wallet key the TEE holds, by the key's signing
// algorithm and with no prefix: an EVM key signs keccak256(msg), V 0 or 1, an
// XRP key SHA512Half(msg), and a VRF key answers a proof as JSON. Pass the
// preimage, never a digest.
// The node signs with any key the TEE holds: deciding who may use it is the extension's job.
func (c *Client) SignWallet(ctx context.Context, walletID common.Hash, keyID uint64, msg []byte) ([]byte, error) {
	var res types.SignResponse
	if err := c.sp.Call(ctx, keyPath("/sign", walletID, keyID), types.SignRequest{Message: msg}, &res); err != nil {
		return nil, fmt.Errorf("signing with the wallet key: %w", err)
	}
	return res.Signature, nil
}

// DecryptWallet decrypts an ECIES ciphertext encrypted to a wallet key's
// public key; EVM and XRP keys only.
func (c *Client) DecryptWallet(ctx context.Context, walletID common.Hash, keyID uint64, ciphertext []byte) ([]byte, error) {
	var res types.DecryptResponse
	req := types.DecryptRequest{EncryptedMessage: ciphertext}
	if err := c.sp.Call(ctx, keyPath("/decrypt", walletID, keyID), req, &res); err != nil {
		return nil, fmt.Errorf("decrypting with the wallet key: %w", err)
	}
	return res.DecryptedMessage, nil
}

func keyPath(route string, walletID common.Hash, keyID uint64) string {
	return route + "/" + walletID.Hex() + "/" + strconv.FormatUint(keyID, 10)
}

func recoverSigner(msg, sig []byte) (common.Address, error) {
	if len(sig) != crypto.SignatureLength {
		return common.Address{}, fmt.Errorf("signature of %d bytes", len(sig))
	}
	s := bytes.Clone(sig)
	if s[crypto.RecoveryIDOffset] >= 27 {
		s[crypto.RecoveryIDOffset] -= 27
	}
	pub, err := crypto.SigToPub(accounts.TextHash(crypto.Keccak256(msg)), s)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}
