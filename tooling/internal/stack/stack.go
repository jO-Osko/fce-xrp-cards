// Package stack runs a test machine for an extension with Docker Compose: the
// extension's image in test mode, tee-proxy, Redis and, unless the developer
// brings a public URL, a cloudflared quick tunnel. tee-proxy reads an external
// indexer's database; the stack runs none.
package stack

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// The images the stack runs besides the extension's, pinned by digest.
const (
	ProxyImage  = "ghcr.io/flare-foundation/tee-proxy:v0.0.25@sha256:3904c261f3e825ed6acb5430d27f5bd082cdeef8ca37318654cc4e21bb683f17"
	RedisImage  = "redis:8.8.3-alpine@sha256:0b2b77d3ea5078274795e3177cdbdada8b96316684a38911d528534ed679b5ec"
	TunnelImage = "cloudflare/cloudflared:2026.9.3@sha256:072c067d25ccbe61d46e18f0d0723255f2bb5304f7317caa95b27031520ff92c"
)

// tee-proxy's ports inside the stack: internal for tee-node, external for everyone else.
const (
	internalPort = 6661
	ExternalPort = 6662
)

// DB is an indexer database tee-proxy reads, as tee-proxy's [db] table names it.
type DB struct {
	Host     string `toml:"host"`
	Port     int    `toml:"port"`
	Database string `toml:"database"`
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// ReadDB reads a DB from a TOML file, refusing unknown keys.
func ReadDB(path string) (DB, error) {
	var db DB
	md, err := toml.DecodeFile(path, &db)
	if err != nil {
		return db, fmt.Errorf("reading %s: %w", path, err)
	}
	if u := md.Undecoded(); len(u) > 0 {
		return db, fmt.Errorf("reading %s: unknown key %s; it takes host, port, database, username and password", path, u[0])
	}
	if db.Host == "" || db.Port == 0 || db.Database == "" || db.Username == "" {
		return db, fmt.Errorf("reading %s: host, port, database and username are required", path)
	}
	return db, nil
}

// Config is a stack's machine and chain.
type Config struct {
	// Image is the extension's image, run in test mode.
	Image       string
	ChainID     uint64
	ExtensionID *big.Int
	// Owner registers the machine: tee-node's INITIAL_OWNER.
	Owner     common.Address
	Signers   []common.Address
	Threshold uint64
	// The contracts tee-proxy follows through the indexer.
	FlareSystemsManager, Relay, VoterRegistry common.Address
	DB                                        DB
	// Port is the host port, on 127.0.0.1, of tee-proxy's external API; 0 lets Docker pick one.
	Port int
	// Tunnel starts a cloudflared quick tunnel to tee-proxy's external API.
	Tunnel bool
}

// Stack is a Compose project kept in a private directory, which also holds tee-proxy's key.
type Stack struct {
	Dir, Project string
}

// For is the stack of extension dir in the fork at root on chain, under base.
func For(base, chain, root, dir string) Stack {
	sum := sha256.Sum256([]byte(root + "\x00" + dir))
	key := strings.ToLower(chain + "-" + dir)
	key = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(key, "-") + "-" + hex.EncodeToString(sum[:4])
	return Stack{Dir: filepath.Join(base, key), Project: "fce-" + key}
}

// Base is where stacks live: the user's cache directory.
func Base() (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "fce", "run"), nil
}

// Exists reports whether the stack's directory exists.
func (s Stack) Exists() bool {
	_, err := os.Stat(s.Dir)
	return err == nil
}

// Write creates the stack's directory with tee-proxy's config, its key and the
// direct actions' API key, and the Compose file. The directory must not exist.
func (s Stack) Write(c Config, proxyKey *ecdsa.PrivateKey, directKey string) error {
	if err := os.MkdirAll(filepath.Dir(s.Dir), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(s.Dir, 0o700); err != nil {
		return err
	}
	cfg, err := proxyConfig(c)
	if err != nil {
		return err
	}
	compose, err := json.MarshalIndent(s.compose(c), "", "  ")
	if err != nil {
		return err
	}
	env := fmt.Sprintf("PRIVATE_KEY=%x\nDIRECT_API_KEY=%s\n", crypto.FromECDSA(proxyKey), directKey)
	for name, b := range map[string][]byte{"proxy.toml": cfg, "proxy.env": []byte(env), "compose.json": compose} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), b, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// proxyConfig is tee-proxy v0.0.25's config; it refuses unknown keys.
func proxyConfig(c Config) ([]byte, error) {
	type addresses struct {
		FlareSystemsManager string `toml:"flare_systems_manager"`
		Relay               string `toml:"relay"`
		VoterRegistry       string `toml:"voter_registry"`
	}
	type ports struct {
		Internal string `toml:"internal"`
		External string `toml:"external"`
	}
	type logging struct {
		Level   string `toml:"level"`
		Console bool   `toml:"console"`
	}
	type direct struct {
		Enable bool `toml:"enable"`
	}
	cfg := struct {
		ChainID   uint64    `toml:"chain_id"`
		RedisPort string    `toml:"redis_port"`
		DB        DB        `toml:"db"`
		Logging   logging   `toml:"logging"`
		Addresses addresses `toml:"addresses"`
		Ports     ports     `toml:"ports"`
		Direct    direct    `toml:"direct"`
	}{
		ChainID:   c.ChainID,
		RedisPort: "redis:6379",
		DB:        c.DB,
		Logging:   logging{Level: "INFO", Console: true},
		Addresses: addresses{c.FlareSystemsManager.Hex(), c.Relay.Hex(), c.VoterRegistry.Hex()},
		Ports:     ports{strconv.Itoa(internalPort), strconv.Itoa(ExternalPort)},
		Direct:    direct{Enable: true}, // the key comes from DIRECT_API_KEY
	}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(cfg); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// compose is the Compose file; JSON is YAML too.
func (s Stack) compose(c Config) map[string]any {
	signers := make([]string, 0, len(c.Signers))
	for _, a := range c.Signers {
		signers = append(signers, a.Hex())
	}
	port := map[string]any{"target": ExternalPort, "host_ip": "127.0.0.1"}
	if c.Port != 0 {
		port["published"] = strconv.Itoa(c.Port)
	}
	services := map[string]any{
		"redis": map[string]any{
			"image":   RedisImage,
			"command": []string{"redis-server", "--save", "", "--appendonly", "no"},
		},
		"node": map[string]any{
			"image":       c.Image,
			"pull_policy": "never",
			"environment": map[string]string{
				"MODE":                 "1",
				"CHAIN_ID":             strconv.FormatUint(c.ChainID, 10),
				"PROXY_URL":            fmt.Sprintf("http://proxy:%d", internalPort),
				"INITIAL_OWNER":        c.Owner.Hex(),
				"EXTENSION_ID":         common.BigToHash(c.ExtensionID).Hex(),
				"GOVERNANCE_SIGNERS":   strings.Join(signers, ","),
				"GOVERNANCE_THRESHOLD": strconv.FormatUint(c.Threshold, 10),
				"LOG_LEVEL":            "INFO",
			},
		},
		"proxy": map[string]any{
			"image": ProxyImage,
			// the host user, so it reads its 0600 config
			"user":     fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			"env_file": []string{"proxy.env"},
			"volumes": []map[string]any{{
				"type": "bind", "source": filepath.Join(s.Dir, "proxy.toml"),
				"target": "/app/config/config.toml", "read_only": true,
			}},
			// only the external API leaves the stack, and only to loopback
			"ports":       []map[string]any{port},
			"extra_hosts": []string{"host.docker.internal:host-gateway"},
			"depends_on":  []string{"redis", "node"},
		},
	}
	if c.Tunnel {
		services["tunnel"] = map[string]any{
			"image":      TunnelImage,
			"command":    []string{"tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://proxy:%d", ExternalPort)},
			"depends_on": []string{"proxy"},
		}
	}
	return map[string]any{"name": s.Project, "services": services}
}

func (s Stack) cmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose", "-p", s.Project, "-f", filepath.Join(s.Dir, "compose.json")}, args...)...)
	cmd.Dir = s.Dir
	return cmd
}

func (s Stack) run(ctx context.Context, args ...string) (string, error) {
	cmd := s.cmd(ctx, args...)
	// fce handles Ctrl-C itself, and stopping must not be cut short by a second one
	detach(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker compose %s: %w\n%s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Up starts the stack, pulling missing images.
func (s Stack) Up(ctx context.Context) error {
	_, err := s.run(ctx, "up", "--detach", "--pull", "missing", "--quiet-pull")
	return err
}

// Down stops the stack and removes its containers, network and volumes.
func (s Stack) Down(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(s.Dir, "compose.json")); err != nil {
		return nil
	}
	_, err := s.run(ctx, "down", "--volumes", "--remove-orphans", "--timeout", "10")
	return err
}

// Remove removes the stack's directory, keys included.
func (s Stack) Remove() error { return os.RemoveAll(s.Dir) }

// Port is the host port of tee-proxy's external API.
func (s Stack) Port(ctx context.Context) (int, error) {
	out, err := s.run(ctx, "port", "proxy", strconv.Itoa(ExternalPort))
	if err != nil {
		return 0, err
	}
	_, p, ok := strings.Cut(strings.TrimSpace(out), ":")
	if !ok {
		return 0, fmt.Errorf("docker compose port: %q", out)
	}
	return strconv.Atoi(p)
}

// Exited is the services that stopped, each with its exit code.
func (s Stack) Exited(ctx context.Context) (map[string]int, error) {
	out, err := s.cmd(ctx, "ps", "--all", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	type container struct {
		Service, State string
		ExitCode       int
	}
	var cs []container
	if bytes.HasPrefix(bytes.TrimSpace(out), []byte("[")) {
		if err := json.Unmarshal(out, &cs); err != nil {
			return nil, fmt.Errorf("docker compose ps: %w", err)
		}
	} else {
		// one object per line since Compose 2.21
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			var c container
			if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
				return nil, fmt.Errorf("docker compose ps: %w", err)
			}
			cs = append(cs, c)
		}
	}
	exited := map[string]int{}
	for _, c := range cs {
		if c.State == "exited" || c.State == "dead" {
			exited[c.Service] = c.ExitCode
		}
	}
	return exited, nil
}

// Logs writes the services' logs to w, their last tail lines, following them until ctx ends if follow.
func (s Stack) Logs(ctx context.Context, w io.Writer, follow bool, tail int, services ...string) error {
	args := []string{"logs", "--no-color", "--tail", strconv.Itoa(tail)}
	if follow {
		args = append(args, "--follow")
	}
	cmd := s.cmd(ctx, append(args, services...)...)
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

var quickTunnel = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// ErrNoTunnelURL means cloudflared hasn't logged its quick tunnel's URL yet.
var ErrNoTunnelURL = errors.New("no quick tunnel URL yet")

// TunnelURL is the quick tunnel's URL, from cloudflared's log.
func (s Stack) TunnelURL(ctx context.Context) (string, error) {
	var b bytes.Buffer
	if err := s.Logs(ctx, &b, false, 200, "tunnel"); err != nil {
		return "", err
	}
	if u := tunnelURL(b.String()); u != "" {
		return u, nil
	}
	return "", ErrNoTunnelURL
}

func tunnelURL(log string) string {
	for _, u := range quickTunnel.FindAllString(log, -1) {
		// cloudflared's own API, in its request and error lines
		if u != "https://api.trycloudflare.com" {
			return u
		}
	}
	return ""
}

// State is what a run did, kept so fce stop can undo it.
type State struct {
	ChainID      uint64          `json:"chainId"`
	ExtensionID  uint64          `json:"extensionId"`
	Owner        common.Address  `json:"owner"`
	URL          string          `json:"url,omitempty"`
	TeeID        *common.Address `json:"teeId,omitempty"`
	ProxyID      *common.Address `json:"proxyId,omitempty"`
	RegisterTx   *common.Hash    `json:"registerTx,omitempty"`
	ProductionTx *common.Hash    `json:"productionTx,omitempty"`
}

// LoadState reads the stack's state; nil when it has none.
func (s Stack) LoadState() (*State, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := new(State)
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("reading %s: %w", filepath.Join(s.Dir, "state.json"), err)
	}
	return st, nil
}

// SaveState writes the stack's state.
func (s Stack) SaveState(st *State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.Dir, "state.json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.Dir, "state.json"))
}

// DirectKey is the API key tee-proxy wants on direct actions.
func (s Stack) DirectKey() (string, error) {
	b, err := os.ReadFile(filepath.Join(s.Dir, "proxy.env"))
	if err != nil {
		return "", err
	}
	for l := range strings.SplitSeq(string(b), "\n") {
		if k, ok := strings.CutPrefix(l, "DIRECT_API_KEY="); ok {
			return k, nil
		}
	}
	return "", errors.New("no DIRECT_API_KEY in proxy.env")
}
