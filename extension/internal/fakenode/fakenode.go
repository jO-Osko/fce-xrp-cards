// Package fakenode fakes tee-node's sign port for tests: a fixed TEE key and
// one fixed wallet key sign and decrypt, and posted results are recorded.
package fakenode

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
	"github.com/flare-foundation/go-flare-common/pkg/tee/structs/wallet"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// key is a fixed, public test key, so signatures and fixtures are deterministic.
var key = mustKey("4c0883a69102937d6231471b5dbb6204fe5129617082792ae468d01a3f362318")

// walletKey is anvil's second test account, so a transaction it signs for
// chain 31337 can be sent to a local anvil.
var walletKey = mustKey("59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d")

var walletID = crypto.Keccak256Hash([]byte("fakenode wallet"))

// WalletKeyID is the id of the one wallet key the fake holds.
const WalletKeyID uint64 = 0

// Node serves the sign port as tee-node v0.0.29 does, with the same strict
// decoding: POST /sign, /decrypt and /result with the TEE key, and
// GET /key-info, POST /sign and /decrypt with the wallet key. It is safe for
// concurrent use.
type Node struct {
	mux *http.ServeMux

	mu      sync.Mutex
	results []types.ActionResult
}

// New returns a Node with no results recorded.
func New() *Node {
	n := &Node{mux: http.NewServeMux()}
	n.mux.HandleFunc("POST /sign", n.sign)
	n.mux.HandleFunc("POST /decrypt", n.decrypt)
	n.mux.HandleFunc("POST /result", n.result)
	n.mux.HandleFunc("GET /key-info/{walletId}/{keyId}", n.keyInfo)
	n.mux.HandleFunc("POST /sign/{walletId}/{keyId}", n.signWallet)
	n.mux.HandleFunc("POST /decrypt/{walletId}/{keyId}", n.decryptWallet)
	return n
}

// Address is the TEE key's address, the teeId.
func Address() common.Address { return crypto.PubkeyToAddress(key.PublicKey) }

// PublicKey is the TEE key's ECIES public key, which users encrypt to.
func PublicKey() *ecies.PublicKey { return &ecies.ImportECDSA(key).PublicKey }

// WalletID is the wallet the fake holds key WalletKeyID of: an EVM key, with
// no admins or cosigners.
func WalletID() common.Hash { return walletID }

// WalletAddress is the wallet key's address.
func WalletAddress() common.Address { return crypto.PubkeyToAddress(walletKey.PublicKey) }

// ServeHTTP serves the sign port; other paths get 404, other methods 405.
func (n *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) { n.mux.ServeHTTP(w, r) }

// Results returns the posted results, in arrival order.
func (n *Node) Results() []types.ActionResult {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]types.ActionResult(nil), n.results...)
}

// sign signs as tee-node's /sign: EIP-191 over keccak256(message), V 0 or 1.
func (n *Node) sign(w http.ResponseWriter, r *http.Request) {
	var req types.SignRequest
	if !decodeStrict(r, &req) || len(req.Message) == 0 {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	sig, err := crypto.Sign(accounts.TextHash(crypto.Keccak256(req.Message)), key)
	if err != nil {
		http.Error(w, "can not sign", http.StatusInternalServerError)
		return
	}
	writeJSON(w, types.SignResponse{Message: req.Message, Signature: sig})
}

// decrypt decrypts as tee-node's /decrypt: ECIES with the TEE key.
func (n *Node) decrypt(w http.ResponseWriter, r *http.Request) {
	var req types.DecryptRequest
	if !decodeStrict(r, &req) || len(req.EncryptedMessage) == 0 {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	plain, err := ecies.ImportECDSA(key).Decrypt(req.EncryptedMessage, nil, nil)
	if err != nil {
		http.Error(w, "can not decrypt", http.StatusBadRequest)
		return
	}
	writeJSON(w, types.DecryptResponse{DecryptedMessage: plain})
}

// result records the result; tee-node would sign it and forward it to the proxy.
func (n *Node) result(w http.ResponseWriter, r *http.Request) {
	var res types.ActionResult
	if !decodeStrict(r, &res) {
		http.Error(w, "invalid result", http.StatusBadRequest)
		return
	}
	n.mu.Lock()
	n.results = append(n.results, res)
	n.mu.Unlock()
}

// keyInfo answers as tee-node's /key-info: the key's existence proof, as JSON.
func (n *Node) keyInfo(w http.ResponseWriter, r *http.Request) {
	if !walletKeyOf(w, r) {
		return
	}
	writeJSON(w, wallet.IWalletKeyManagerKeyExistence{
		TeeId:       Address(),
		WalletId:    walletID,
		KeyId:       WalletKeyID,
		KeyType:     pad32("EVM"),
		SigningAlgo: pad32("keccak256-secp256k1-ecdsa"),
		PublicKey:   crypto.FromECDSAPub(&walletKey.PublicKey)[1:],
		Nonce:       new(big.Int),
		ConfigConstants: wallet.IWalletKeyManagerKeyConfigConstants{
			AdminsPublicKeys: []wallet.PublicKey{},
			Cosigners:        []common.Address{},
		},
	})
}

// signWallet signs as tee-node's /sign/{walletId}/{keyId} with an EVM key:
// keccak256(message), no prefix, V 0 or 1; it takes an empty message too.
func (n *Node) signWallet(w http.ResponseWriter, r *http.Request) {
	var req types.SignRequest
	if !validKeyIDs(r) || !decodeStrict(r, &req) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if !walletKeyOf(w, r) {
		return
	}
	sig, err := crypto.Sign(crypto.Keccak256(req.Message), walletKey)
	if err != nil {
		http.Error(w, "can not sign", http.StatusInternalServerError)
		return
	}
	writeJSON(w, types.SignResponse{Message: req.Message, Signature: sig})
}

// decryptWallet decrypts as tee-node's /decrypt/{walletId}/{keyId}: ECIES with the wallet key.
func (n *Node) decryptWallet(w http.ResponseWriter, r *http.Request) {
	var req types.DecryptRequest
	if !validKeyIDs(r) || !decodeStrict(r, &req) || len(req.EncryptedMessage) == 0 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if !walletKeyOf(w, r) {
		return
	}
	plain, err := ecies.ImportECDSA(walletKey).Decrypt(req.EncryptedMessage, nil, nil)
	if err != nil {
		http.Error(w, "can not decrypt", http.StatusBadRequest)
		return
	}
	writeJSON(w, types.DecryptResponse{DecryptedMessage: plain})
}

// walletKeyOf answers 400 for ids tee-node can't parse and 404 for a key the fake doesn't hold.
func walletKeyOf(w http.ResponseWriter, r *http.Request) bool {
	id, k, ok := keyIDs(r)
	switch {
	case !ok:
		http.Error(w, "invalid id", http.StatusBadRequest)
		return false
	case id != walletID || k != WalletKeyID:
		http.Error(w, "wallet non-existent", http.StatusNotFound)
		return false
	}
	return true
}

func validKeyIDs(r *http.Request) bool {
	_, _, ok := keyIDs(r)
	return ok
}

// keyIDs parses the path's ids as tee-node does: 32 bytes of hex, 0x optional, and a decimal uint64.
func keyIDs(r *http.Request) (common.Hash, uint64, bool) {
	id, err := hex.DecodeString(strings.TrimPrefix(strings.ToLower(r.PathValue("walletId")), "0x"))
	if err != nil || len(id) != common.HashLength {
		return common.Hash{}, 0, false
	}
	k, err := strconv.ParseUint(r.PathValue("keyId"), 10, 64)
	if err != nil {
		return common.Hash{}, 0, false
	}
	return common.Hash(id), k, true
}

// pad32 right-pads s, as the chain's key types and signing algorithms are.
func pad32(s string) common.Hash {
	var h common.Hash
	copy(h[:], s)
	return h
}

func decodeStrict(r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v) == nil
}

func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "encoding the response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b) //nolint:errcheck,gosec // the response is already committed; nothing left to report to
}

func mustKey(hex string) *ecdsa.PrivateKey {
	k, err := crypto.HexToECDSA(hex)
	if err != nil {
		panic(err)
	}
	return k
}
