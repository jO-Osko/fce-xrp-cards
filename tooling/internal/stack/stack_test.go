package stack

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFor(t *testing.T) {
	t.Parallel()
	s := For("/base", "coston2", "/home/dev/fork", "examples/direct")
	assert.Equal(t, "/base/coston2-examples-direct-"+s.Dir[len(s.Dir)-8:], s.Dir)
	assert.Equal(t, "fce-"+filepath.Base(s.Dir), s.Project)
	assert.NotEqual(t, s, For("/base", "coston2", "/home/dev/other", "examples/direct"), "another fork")
	assert.NotEqual(t, s, For("/base", "anvil-coston2", "/home/dev/fork", "examples/direct"), "another chain")
	assert.Regexp(t, `^fce-[a-z0-9-]+$`, For("/b", "anvil-31337", "/r", "Ex_1/A.b").Project)
}

func TestReadDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "db.toml")
		require.NoError(t, os.WriteFile(p, []byte(s), 0o600))
		return p
	}
	db, err := ReadDB(write("host = \"db.example\"\nport = 3306\ndatabase = \"indexer\"\nusername = \"u\"\npassword = \"p\\\"w\"\n"))
	require.NoError(t, err)
	assert.Equal(t, DB{Host: "db.example", Port: 3306, Database: "indexer", Username: "u", Password: `p"w`}, db)

	_, err = ReadDB(write("host = \"h\"\nport = 1\ndatabase = \"d\"\nusername = \"u\"\n[tls]\nmode = \"x\"\n"))
	require.ErrorContains(t, err, "unknown key tls")
	_, err = ReadDB(write("host = \"h\"\ndatabase = \"d\"\nusername = \"u\"\n"))
	require.ErrorContains(t, err, "required")
	_, err = ReadDB(filepath.Join(dir, "none.toml"))
	require.Error(t, err)
}

func TestWrite(t *testing.T) {
	t.Parallel()
	s := For(t.TempDir(), "coston2", "/fork", "template")
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	owner := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	c := Config{
		Image: "fce/template", ChainID: 114, ExtensionID: big.NewInt(67274), Owner: owner,
		Signers: []common.Address{owner, common.HexToAddress("0x2")}, Threshold: 1,
		FlareSystemsManager: common.HexToAddress("0xA90Db6D10F856799b10ef2A77EBCbF460aC71e52"),
		Relay:               common.HexToAddress("0x5017728F117501A24EF9C3756C07f0d564598596"),
		VoterRegistry:       common.HexToAddress("0x6a0AF07b7972177B176d3D422555cbc98DfDe914"),
		DB:                  DB{Host: "db", Port: 3306, Database: "indexer", Username: "u", Password: `p"w\`},
		Tunnel:              true,
	}
	require.NoError(t, s.Write(c, key, "directkey"))
	require.Error(t, s.Write(c, key, "directkey"), "the directory exists")
	assert.True(t, s.Exists())

	fi, err := os.Stat(s.Dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), fi.Mode().Perm())
	for _, f := range []string{"proxy.toml", "proxy.env", "compose.json"} {
		fi, err := os.Stat(filepath.Join(s.Dir, f))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), f)
	}

	var cfg map[string]any
	_, err = toml.DecodeFile(filepath.Join(s.Dir, "proxy.toml"), &cfg)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"chain_id", "redis_port", "db", "logging", "addresses", "ports", "direct"}, keys(cfg))
	assert.Equal(t, int64(114), cfg["chain_id"])
	assert.Equal(t, map[string]any{"host": "db", "port": int64(3306), "database": "indexer", "username": "u", "password": `p"w\`}, cfg["db"])
	assert.Equal(t, map[string]any{"internal": "6661", "external": "6662"}, cfg["ports"])
	assert.Equal(t, map[string]any{"enable": true}, cfg["direct"])
	assert.Equal(t, map[string]any{
		"flare_systems_manager": "0xA90Db6D10F856799b10ef2A77EBCbF460aC71e52",
		"relay":                 "0x5017728F117501A24EF9C3756C07f0d564598596",
		"voter_registry":        "0x6a0AF07b7972177B176d3D422555cbc98DfDe914",
	}, cfg["addresses"])

	env, err := os.ReadFile(filepath.Join(s.Dir, "proxy.env"))
	require.NoError(t, err)
	assert.Equal(t, "PRIVATE_KEY="+common.Bytes2Hex(crypto.FromECDSA(key))+"\nDIRECT_API_KEY=directkey\n", string(env))
	k, err := s.DirectKey()
	require.NoError(t, err)
	assert.Equal(t, "directkey", k)

	b, err := os.ReadFile(filepath.Join(s.Dir, "compose.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(b), common.Bytes2Hex(crypto.FromECDSA(key)), "the key stays in proxy.env")
	assert.NotContains(t, string(b), "directkey")
	var compose struct {
		Name     string
		Services map[string]struct {
			Image       string
			Environment map[string]string
			Ports       []map[string]any
			EnvFile     []string `json:"env_file"`
		}
	}
	require.NoError(t, json.Unmarshal(b, &compose))
	assert.Equal(t, s.Project, compose.Name)
	assert.ElementsMatch(t, []string{"redis", "node", "proxy", "tunnel"}, keys(compose.Services))
	node := compose.Services["node"].Environment
	assert.Equal(t, "1", node["MODE"])
	assert.Equal(t, "114", node["CHAIN_ID"])
	assert.Equal(t, "http://proxy:6661", node["PROXY_URL"])
	assert.Equal(t, owner.Hex(), node["INITIAL_OWNER"])
	assert.Equal(t, "0x00000000000000000000000000000000000000000000000000000000000106ca", node["EXTENSION_ID"])
	assert.Equal(t, owner.Hex()+",0x0000000000000000000000000000000000000002", node["GOVERNANCE_SIGNERS"])
	assert.Equal(t, "1", node["GOVERNANCE_THRESHOLD"])
	assert.Equal(t, []map[string]any{{"target": float64(6662), "host_ip": "127.0.0.1"}}, compose.Services["proxy"].Ports)
	assert.Empty(t, compose.Services["node"].Ports, "5500 and the internal port stay inside")
	assert.Equal(t, []string{"proxy.env"}, compose.Services["proxy"].EnvFile)
	for name, svc := range compose.Services {
		if name != "node" {
			assert.Contains(t, svc.Image, "@sha256:", name)
		}
	}

	c.Tunnel, c.Port = false, 16662
	s2 := For(t.TempDir(), "coston2", "/fork", "template")
	require.NoError(t, s2.Write(c, key, "k"))
	b, err = os.ReadFile(filepath.Join(s2.Dir, "compose.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(b), "cloudflared")
	assert.True(t, strings.Contains(string(b), `"published": "16662"`))

	st := &State{ChainID: 114, ExtensionID: 67274, Owner: owner, URL: "https://x.trycloudflare.com"}
	require.NoError(t, s.SaveState(st))
	got, err := s.LoadState()
	require.NoError(t, err)
	assert.Equal(t, st, got)
	require.NoError(t, s.Remove())
	assert.False(t, s.Exists())
	got, err = s.LoadState()
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestTunnelURL(t *testing.T) {
	t.Parallel()
	log := `tunnel-1  | 2026-10-12T12:00:00Z INF Requesting new quick Tunnel on trycloudflare.com...
tunnel-1  | 2026-10-12T12:00:01Z ERR failed to reach https://api.trycloudflare.com, retrying
tunnel-1  | 2026-10-12T12:00:02Z INF |  Your quick Tunnel has been created! Visit it at (it may take some time to be reachable):  |
tunnel-1  | 2026-10-12T12:00:02Z INF |  https://brave-otter-lake-sky.trycloudflare.com                                            |`
	assert.Equal(t, "https://brave-otter-lake-sky.trycloudflare.com", tunnelURL(log))
	assert.Empty(t, tunnelURL("tunnel-1  | ERR failed to reach https://api.trycloudflare.com"))
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
