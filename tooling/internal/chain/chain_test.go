package chain

import (
	"context"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestCodeHash(t *testing.T) {
	t.Parallel()
	// tee-node v0.0.28 internal/settings TestCodeHash; Coston2 extension 66988's test machine registered it
	assert.Equal(t, "0x194844cf417dde867073e5ab7199fa4d21fd82b5dbe2bdea8b3d7fc18d10fdc2", TestCodeHash.Hex())
}

func TestRecordName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "coston2", recordName(big.NewInt(114), false))
	assert.Equal(t, "flare", recordName(big.NewInt(14), false))
	assert.Equal(t, "anvil-coston2", recordName(big.NewInt(114), true))
	assert.Equal(t, "31337", recordName(big.NewInt(31337), false))
	assert.Equal(t, "anvil-31337", recordName(big.NewInt(31337), true))
}

func TestDialRefuses(t *testing.T) {
	t.Parallel()
	_, err := Dial(context.Background(), "", "")
	require.ErrorContains(t, err, "-chain or -rpc")
	_, err = Dial(context.Background(), "costn2", "")
	require.ErrorContains(t, err, `unknown chain "costn2"`)
}

func TestBytes32(t *testing.T) {
	t.Parallel()
	b, err := Bytes32("TEST_PLATFORM")
	require.NoError(t, err)
	assert.Equal(t, "0x544553545f504c4154464f524d00000000000000000000000000000000000000", "0x"+hex.EncodeToString(b[:]))
	assert.Equal(t, "TEST_PLATFORM", String32(b))
	_, err = Bytes32("")
	require.Error(t, err)
	_, err = Bytes32("123456789012345678901234567890123")
	require.Error(t, err)
	assert.Equal(t, `"a\x01"`, String32([32]byte{'a', 1}))
}

func TestTrimPassword(t *testing.T) {
	t.Parallel()
	// as forge reads --password-file
	assert.Equal(t, "pw x", trimPassword("pw x\n"))
	assert.Equal(t, "pw x", trimPassword("pw x\r\n"))
	assert.Equal(t, "pw", trimPassword("pw \t\n"))
	assert.Equal(t, " pw", trimPassword(" pw"))
}

func TestKeystorePath(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	p, err := keystorePath("dev", "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, ".foundry", "keystores", "dev"), p)
	p, err = keystorePath("", "/k/dev.json")
	require.NoError(t, err)
	assert.Equal(t, "/k/dev.json", p)

	for _, c := range [][2]string{{"", ""}, {"dev", "/k/dev.json"}, {"../dev", ""}, {"a/b", ""}, {"..", ""}} {
		_, err := keystorePath(c[0], c[1])
		assert.Error(t, err, c)
	}
}

func TestOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	addr := crypto.PubkeyToAddress(key.PublicKey)
	j, err := keystore.EncryptKey(&keystore.Key{Address: addr, PrivateKey: key}, "pw", keystore.LightScryptN, keystore.LightScryptP)
	require.NoError(t, err)
	ks := filepath.Join(dir, "key.json")
	require.NoError(t, os.WriteFile(ks, j, 0o600))
	pw := filepath.Join(dir, "password")
	require.NoError(t, os.WriteFile(pw, []byte("pw\n"), 0o600))

	w, err := Open("", ks, pw)
	require.NoError(t, err)
	defer w.Close()
	assert.Equal(t, addr, w.Address)
	assert.Equal(t, ks, w.Keystore)
	assert.Equal(t, pw, w.PasswordFile, "forge reads the same file")
	o, err := w.Opts(context.Background(), &Chain{ID: big.NewInt(114)})
	require.NoError(t, err)
	assert.Equal(t, addr, o.From)

	require.NoError(t, os.WriteFile(pw, []byte("wrong"), 0o600))
	_, err = Open("", ks, pw)
	require.ErrorContains(t, err, "opening "+ks)
}

func TestWritePassword(t *testing.T) {
	t.Parallel()
	w := &Wallet{}
	require.NoError(t, w.writePassword("pw"))
	b, err := os.ReadFile(w.PasswordFile)
	require.NoError(t, err)
	assert.Equal(t, "pw", string(b))
	fi, err := os.Stat(w.PasswordFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	dir := filepath.Dir(w.PasswordFile)
	w.Close()
	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err))
}
