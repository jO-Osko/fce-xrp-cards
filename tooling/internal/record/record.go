// Package record reads and writes an extension's deployment record: what fce
// deployed and registered on one chain, kept in the fork next to its code.
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
)

// Dir is the directory, in an extension's directory, that holds its records.
const Dir = "deployments"

// Record is an extension's deployment on one chain.
type Record struct {
	ChainID uint64 `json:"chainId"`
	// FlareTeeManager is the FlareTeeManager the extension is registered at.
	FlareTeeManager common.Address `json:"flareTeeManager"`
	// Deployer deployed and registered the extension, so first owned it.
	Deployer          common.Address  `json:"deployer"`
	InstructionSender common.Address  `json:"instructionSender"`
	StateVerifier     *common.Address `json:"stateVerifier,omitempty"`
	// RegisterTx is ExtensionManager.register's transaction, recorded before it is mined.
	RegisterTx  *common.Hash `json:"registerTx,omitempty"`
	ExtensionID *uint64      `json:"extensionId,omitempty"`
	Versions    []Version    `json:"versions,omitempty"`
	Governance  *Governance  `json:"governance,omitempty"`
}

// Version is a code hash the extension allows, by ExtensionManager.addTeeVersion.
type Version struct {
	Version   string      `json:"version"`
	CodeHash  common.Hash `json:"codeHash"`
	Platforms []string    `json:"platforms"`
	// Disabled are the platforms ExtensionManager.disableCodeHashPlatforms disabled it on, for good.
	Disabled []string `json:"disabled,omitempty"`
	// Commit is the fork's commit the image was built from; empty for a test version, or a build
	// with uncommitted changes.
	Commit string `json:"commit,omitempty"`
}

// Governance is the extension's TEE governance, by ExtensionGovernance.setNewTeeGovernance.
type Governance struct {
	Signers   []common.Address `json:"signers"`
	Threshold uint64           `json:"threshold"`
}

// Path is the record of the extension in dir, under root, on the chain named name.
func Path(root, dir, name string) string {
	return filepath.Join(root, filepath.FromSlash(dir), Dir, name+".json")
}

// Load reads the record at path; nil when there is none.
func Load(path string) (*Record, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var r Record
	if err := d.Decode(&r); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if d.More() {
		return nil, fmt.Errorf("reading %s: data after the record", path)
	}
	return &r, nil
}

// Save writes r to path, replacing the old record only once the new one is complete.
func (r *Record) Save(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) //nolint:errcheck // gone after the rename
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close() //nolint:errcheck,gosec // the write failed already
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
