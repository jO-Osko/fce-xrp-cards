// Package chain connects fce to a Flare network: its RPC, Flare's contract
// registry, the FlareTeeManager it names, and transactions signed with a key
// from a keystore.
package chain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/contractregistry"
)

// Registry is Flare's contract registry, at the same address on every Flare network.
var Registry = common.HexToAddress("0xaD67FE66660Fb8dFE9d6b1b4240d8650e30F6019")

// TestCodeHash is the code hash tee-node reports in test mode (MODE other than 0),
// on TestPlatform, whatever the image: it binds no code.
var TestCodeHash = common.HexToHash("0x194844cf417dde867073e5ab7199fa4d21fd82b5dbe2bdea8b3d7fc18d10fdc2")

// TestPlatform is the platform tee-node reports in test mode.
const TestPlatform = "TEST_PLATFORM"

// Network is a Flare network with a public RPC.
type Network struct {
	Name string
	ID   uint64
	RPC  string
}

// Networks are the networks -chain names.
var Networks = []Network{
	{"flare", 14, "https://flare-api.flare.network/ext/C/rpc"},
	{"songbird", 19, "https://songbird-api.flare.network/ext/C/rpc"},
	{"coston", 16, "https://coston-api.flare.network/ext/C/rpc"},
	{"coston2", 114, "https://coston2-api.flare.network/ext/C/rpc"},
}

// Chain is a connection to a network.
type Chain struct {
	Client *ethclient.Client
	// RPC is the RPC's URL; it may carry an API key, so it stays out of records and argv.
	RPC string
	ID  *big.Int
	// Name names the network's deployment records: the network's name, the
	// chain id for other chains, prefixed with anvil- on anvil, so a fork's
	// record never passes for the network's.
	Name string
	// Manager is the FlareTeeManager the registry names.
	Manager common.Address
}

// Dial connects to the network name names, or to rpc when set; with both,
// the chain at rpc must be that network.
func Dial(ctx context.Context, name, rpc string) (*Chain, error) {
	var want *Network
	if name != "" {
		for i := range Networks {
			if Networks[i].Name == name {
				want = &Networks[i]
			}
		}
		if want == nil {
			return nil, fmt.Errorf("unknown chain %q: %s, or -rpc", name, names())
		}
		if rpc == "" {
			rpc = want.RPC
		}
	}
	if rpc == "" {
		return nil, errors.New("no chain: -chain or -rpc")
	}
	client, err := ethclient.DialContext(ctx, rpc)
	if err != nil {
		return nil, fmt.Errorf("connecting to the RPC: %w", err)
	}
	c, err := connect(ctx, client, want)
	if err != nil {
		client.Close()
		return nil, err
	}
	c.RPC = rpc
	return c, nil
}

func connect(ctx context.Context, client *ethclient.Client, want *Network) (*Chain, error) {
	id, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the chain id: %w", err)
	}
	if want != nil && id.Uint64() != want.ID {
		return nil, fmt.Errorf("the RPC serves chain %s, not %s (%d)", id, want.Name, want.ID)
	}
	var v string
	if err := client.Client().CallContext(ctx, &v, "web3_clientVersion"); err != nil {
		return nil, fmt.Errorf("reading the client version: %w", err)
	}
	c := &Chain{Client: client, ID: id, Name: recordName(id, strings.HasPrefix(strings.ToLower(v), "anvil/"))}

	if code, err := client.CodeAt(ctx, Registry, nil); err != nil {
		return nil, fmt.Errorf("reading the contract registry: %w", err)
	} else if len(code) == 0 {
		return nil, fmt.Errorf("chain %s has no Flare contract registry at %s", id, Registry)
	}
	reg, err := contractregistry.NewContractRegistryCaller(Registry, client)
	if err != nil {
		return nil, err
	}
	c.Manager, err = reg.GetContractAddressByName(&bind.CallOpts{Context: ctx}, "FlareTeeManager")
	if err != nil {
		return nil, fmt.Errorf("reading FlareTeeManager's address: %w", err)
	}
	if c.Manager == (common.Address{}) {
		return nil, fmt.Errorf("chain %s has no FlareTeeManager yet", id)
	}
	return c, nil
}

func recordName(id *big.Int, anvil bool) string {
	name := id.String()
	for _, n := range Networks {
		if id.IsUint64() && n.ID == id.Uint64() {
			name = n.Name
		}
	}
	if anvil {
		return "anvil-" + name
	}
	return name
}

func names() string {
	s := make([]string, 0, len(Networks))
	for _, n := range Networks {
		s = append(s, n.Name)
	}
	return strings.Join(s, ", ")
}

// Close closes the connection.
func (c *Chain) Close() { c.Client.Close() }

// ErrReverted means a transaction was mined and reverted.
var ErrReverted = errors.New("reverted")

// Wait waits for the transaction and fails with ErrReverted if it reverted.
func (c *Chain) Wait(ctx context.Context, tx common.Hash) (*types.Receipt, error) {
	r, err := bind.WaitMinedHash(ctx, c.Client, tx)
	if err != nil {
		return nil, fmt.Errorf("waiting for %s: %w", tx, err)
	}
	if r.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("transaction %s %w", tx, ErrReverted)
	}
	return r, nil
}

// Bytes32 is s as a bytes32 the contracts compare, such as a version or a
// platform name: its bytes, zero-padded on the right.
func Bytes32(s string) ([32]byte, error) {
	var b [32]byte
	if s == "" || len(s) > 32 {
		return b, fmt.Errorf("%q is not 1 to 32 bytes", s)
	}
	copy(b[:], s)
	return b, nil
}

// String32 is Bytes32's inverse, quoted when b holds more than printable ASCII.
func String32(b [32]byte) string {
	s := strings.TrimRight(string(b[:]), "\x00")
	for _, r := range s {
		if r < 0x20 || r > 0x7e {
			return strconv.Quote(s)
		}
	}
	return s
}

// Lookup is the address Flare's contract registry gives name.
func (c *Chain) Lookup(ctx context.Context, name string) (common.Address, error) {
	reg, err := contractregistry.NewContractRegistryCaller(Registry, c.Client)
	if err != nil {
		return common.Address{}, err
	}
	a, err := reg.GetContractAddressByName(&bind.CallOpts{Context: ctx}, name)
	if err != nil {
		return common.Address{}, fmt.Errorf("reading %s's address: %w", name, err)
	}
	if a == (common.Address{}) {
		return common.Address{}, fmt.Errorf("chain %s's registry names no %s", c.ID, name)
	}
	return a, nil
}

// Explain names the custom error in a call's revert data, from the errors in abis.
func Explain(err error, abis ...*abi.ABI) error {
	var de rpc.DataError
	if !errors.As(err, &de) {
		return err
	}
	s, ok := de.ErrorData().(string)
	if !ok {
		return err
	}
	data, derr := hexutil.Decode(s)
	if derr != nil || len(data) < 4 {
		return err
	}
	for _, a := range abis {
		for _, e := range a.Errors {
			if !bytes.Equal(e.ID[:4], data[:4]) {
				continue
			}
			args, uerr := e.Inputs.Unpack(data[4:])
			if uerr != nil {
				continue
			}
			parts := make([]string, len(args))
			for i, v := range args {
				parts[i] = fmt.Sprint(v)
			}
			return fmt.Errorf("reverted: %s(%s)", e.Name, strings.Join(parts, ", "))
		}
	}
	return fmt.Errorf("%w (revert data %s)", err, s)
}
