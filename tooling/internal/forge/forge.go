// Package forge runs an extension's forge scripts against a chain.
package forge

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/flare-foundation/fce-tooling/internal/chain"
)

// Install installs the Foundry project's dependencies, pinned by its soldeer.lock.
func Install(ctx context.Context, dir string) error {
	return run(ctx, dir, nil, "soldeer", "install", "--quiet")
}

// Script runs the script at script, a path in the Foundry project at dir such as
// script/Deploy.s.sol, and broadcasts its transactions to c, signed by w. env
// adds to fce's environment; the RPC's URL goes in Foundry's config variable, out of argv.
func Script(ctx context.Context, dir, script string, c *chain.Chain, w *chain.Wallet, env []string) error {
	name := strings.TrimSuffix(path.Base(script), ".s.sol")
	return run(ctx, dir, append(env, "FOUNDRY_ETH_RPC_URL="+c.RPC),
		"script", script+":"+name, "--broadcast", "--slow",
		"--keystore", w.Keystore, "--password-file", w.PasswordFile)
}

// run runs forge in dir; its output shows only when it fails.
func run(ctx context.Context, dir string, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "forge", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("forge %s in %s: %w\n%s", args[0], dir, err, strings.TrimSpace(out.String()))
	}
	return nil
}
