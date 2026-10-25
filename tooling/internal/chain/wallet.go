package chain

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"golang.org/x/term"
)

// Wallet is the key fce signs with, from a Foundry keystore, and the files
// that hand forge the same key.
type Wallet struct {
	Address common.Address
	// Keystore is the keystore file; PasswordFile holds its password, as forge's --password-file reads it.
	Keystore, PasswordFile string

	key *ecdsa.PrivateKey
	tmp string // a directory holding a password file Open wrote
}

// Open opens the keystore file account names in Foundry's keystores
// directory, or keystore, with the password in passwordFile or asked for on
// the terminal.
func Open(account, keystore, passwordFile string) (*Wallet, error) {
	path, err := keystorePath(account, keystore)
	if err != nil {
		return nil, err
	}
	w := &Wallet{Keystore: path, PasswordFile: passwordFile}
	var pw string
	if passwordFile != "" {
		b, err := os.ReadFile(passwordFile)
		if err != nil {
			return nil, err
		}
		pw = trimPassword(string(b))
	} else {
		if pw, err = ask(fmt.Sprintf("password for %s: ", path)); err != nil {
			return nil, err
		}
		if pw != trimPassword(pw) {
			return nil, errors.New("the password ends in whitespace, which forge's password files drop: pass it some other way, or change it")
		}
		if err := w.writePassword(pw); err != nil {
			return nil, err
		}
	}
	if err := w.decrypt(pw); err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

func keystorePath(account, keystore string) (string, error) {
	switch {
	case (account == "") == (keystore == ""):
		return "", errors.New("one of -account and -keystore")
	case keystore != "":
		return keystore, nil
	case strings.ContainsAny(account, `/\`) || account == "." || account == "..":
		return "", fmt.Errorf("-account %q: a name in Foundry's keystores directory; -keystore takes a path", account)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".foundry", "keystores", account), nil
}

// trimPassword reads a password file as forge does: trailing whitespace dropped, leading kept.
func trimPassword(s string) string { return strings.TrimRightFunc(s, unicode.IsSpace) }

func ask(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", errors.New("no terminal to ask for the password: -password-file")
	}
	defer tty.Close() //nolint:errcheck // the prompt is done
	if _, err := fmt.Fprint(tty, prompt); err != nil {
		return "", err
	}
	b, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty) //nolint:errcheck // cosmetic
	if err != nil {
		return "", fmt.Errorf("reading the password: %w", err)
	}
	return string(b), nil
}

// writePassword puts pw in a file only this user can read, for forge.
func (w *Wallet) writePassword(pw string) error {
	dir, err := os.MkdirTemp("", "fce-wallet")
	if err != nil {
		return err
	}
	w.tmp = dir
	w.PasswordFile = filepath.Join(dir, "password")
	if err := os.WriteFile(w.PasswordFile, []byte(pw), 0o600); err != nil {
		w.Close()
		return err
	}
	return nil
}

func (w *Wallet) decrypt(pw string) error {
	b, err := os.ReadFile(w.Keystore)
	if err != nil {
		return err
	}
	k, err := keystore.DecryptKey(b, pw)
	if err != nil {
		return fmt.Errorf("opening %s: %w", w.Keystore, err)
	}
	w.key, w.Address = k.PrivateKey, crypto.PubkeyToAddress(k.PrivateKey.PublicKey)
	return nil
}

// Close removes the password file Open wrote, if any.
func (w *Wallet) Close() {
	if w.tmp != "" {
		os.RemoveAll(w.tmp) //nolint:errcheck,gosec // best effort; a temporary directory
		w.tmp = ""
	}
}

// Opts are the transaction options that sign with the wallet's key on c.
func (w *Wallet) Opts(ctx context.Context, c *Chain) (*bind.TransactOpts, error) {
	o, err := bind.NewKeyedTransactorWithChainID(w.key, c.ID)
	if err != nil {
		return nil, err
	}
	o.Context = ctx
	return o, nil
}
