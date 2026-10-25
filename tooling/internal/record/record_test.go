package record

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveLoad(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := Path(root, "examples/direct", "coston2")
	assert.Equal(t, filepath.Join(root, "examples", "direct", "deployments", "coston2.json"), path)

	r, err := Load(path)
	require.NoError(t, err)
	assert.Nil(t, r)

	id := uint64(67274)
	verifier := common.HexToAddress("0x2")
	tx := common.HexToHash("0x3")
	want := &Record{
		ChainID:           114,
		FlareTeeManager:   common.HexToAddress("0x1a9C4A0f9D76c0b1D91d22E24E573a9b377618aE"),
		Deployer:          common.HexToAddress("0x1"),
		InstructionSender: verifier,
		StateVerifier:     &verifier,
		RegisterTx:        &tx,
		ExtensionID:       &id,
		Versions:          []Version{{Version: "0.1.0", CodeHash: common.HexToHash("0x4"), Platforms: []string{"TEST_PLATFORM"}}},
		Governance:        &Governance{Signers: []common.Address{common.HexToAddress("0x1")}, Threshold: 1},
	}
	require.NoError(t, want.Save(path))
	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), fi.Mode().Perm())
	left, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".record-*"))
	require.NoError(t, err)
	assert.Empty(t, left)
}

func TestLoadRefuses(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"an unknown field": `{"chainId": 114, "notes": "x"}`,
		"data after it":    `{"chainId": 114} {}`,
		"not JSON":         `chainId: 114`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "r.json")
			require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
			_, err := Load(path)
			assert.Error(t, err)
		})
	}
}
