package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/ecies"
)

// maxPlaintext is a direct action's message limit in tee-node; a ciphertext is longer still.
const maxPlaintext = 10 << 20

// encryptCmd encrypts stdin to a TEE's public key, as tee-node's sign port decrypts, and prints the
// ciphertext as hex. The plaintext comes from stdin, so it stays out of shell history and process lists.
func encryptCmd(args []string, in io.Reader, out io.Writer) int {
	fs := flag.NewFlagSet("encrypt", flag.ContinueOnError)
	pubkey := fs.String("pubkey", "", `the TEE's public key: teeInfo.publicKey from the proxy's GET /info, {"x":…,"y":…}, or x then y as 64 bytes of hex`)
	tee := fs.String("tee", "", "the TEE's address, its teeId: refuse a key that isn't its")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *pubkey == "" {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}

	plain, err := io.ReadAll(io.LimitReader(in, maxPlaintext+1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading the plaintext from stdin: %v\n", err)
		return 1
	}
	if len(plain) > maxPlaintext {
		fmt.Fprintf(os.Stderr, "the plaintext is over %d bytes, tee-node's limit for a direct action\n", maxPlaintext)
		return 1
	}
	ct, err := encrypt(*pubkey, *tee, plain)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := fmt.Fprintln(out, hexutil.Encode(ct)); err != nil {
		fmt.Fprintf(os.Stderr, "writing the ciphertext: %v\n", err)
		return 1
	}
	return 0
}

// encrypt encrypts plain to pubkey with ECIES on secp256k1, as tee-node's /decrypt expects.
func encrypt(pubkey, tee string, plain []byte) ([]byte, error) {
	pub, err := parsePubkey(pubkey)
	if err != nil {
		return nil, fmt.Errorf("-pubkey: %w", err)
	}
	if tee != "" {
		if !common.IsHexAddress(tee) {
			return nil, fmt.Errorf("-tee: %q is not an address", tee)
		}
		if a := crypto.PubkeyToAddress(*pub); a != common.HexToAddress(tee) {
			return nil, fmt.Errorf("-pubkey belongs to TEE %s, not %s", a, tee)
		}
	}
	ct, err := ecies.Encrypt(rand.Reader, ecies.ImportECDSAPublic(pub), plain, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("encrypting: %w", err)
	}
	return ct, nil
}

// parsePubkey reads {"x","y"}, as the proxy's GET /info serves it, or x then y in hex, with or without the 0x04 prefix.
func parsePubkey(s string) (*ecdsa.PublicKey, error) {
	s = strings.TrimSpace(s)
	var b []byte
	if strings.HasPrefix(s, "{") {
		var xy struct{ X, Y common.Hash }
		dec := json.NewDecoder(strings.NewReader(s))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&xy); err != nil {
			return nil, fmt.Errorf("decoding: %w", err)
		}
		b = append(xy.X.Bytes(), xy.Y.Bytes()...)
	} else {
		var err error
		if b, err = hexutil.Decode(s); err != nil {
			return nil, fmt.Errorf("decoding: %w", err)
		}
	}
	if len(b) == 64 {
		b = append([]byte{4}, b...)
	}
	pub, err := crypto.UnmarshalPubkey(b)
	if err != nil {
		return nil, errors.New("not a secp256k1 public key")
	}
	return pub, nil
}
