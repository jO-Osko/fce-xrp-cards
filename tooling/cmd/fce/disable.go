package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/fce-tooling/internal/chain"
	"github.com/flare-foundation/fce-tooling/internal/record"
	"github.com/flare-foundation/go-flare-common/pkg/contracts/tee/extensionmanager"
)

func disableVersionCmd(ctx context.Context, args []string) int {
	var hash, platforms string
	var test bool
	extra := func(fs *flag.FlagSet) {
		fs.StringVar(&hash, "hash", "", "the code hash to disable, 0x and 64 hex digits")
		fs.BoolVar(&test, "test", false, "disable tee-node's test code hash instead")
		fs.StringVar(&platforms, "platform", "", "the platforms to disable it on, comma-separated; all of its platforms by default")
	}
	return sessionCmd(ctx, "disable-version", args, true, extra, func(s *session) error {
		return s.disableVersion(ctx, hash, test, platforms)
	})
}

// disableVersion disables a code hash on platforms, by ExtensionManager.disableCodeHashPlatforms,
// which pauses the extension's machines in production there; the registry can't undo it.
func (s *session) disableVersion(ctx context.Context, hashArg string, test bool, platformList string) error {
	id, err := s.extensionID()
	if err != nil {
		return err
	}
	if err := s.owner(ctx, id); err != nil {
		return err
	}
	var h common.Hash
	switch {
	case test && hashArg != "":
		return errors.New("-hash or -test, not both")
	case test:
		h = chain.TestCodeHash
	case hashArg == "":
		return errors.New("-hash, or -test for tee-node's test code hash")
	default:
		b, err := hexutil.Decode(hashArg)
		if err != nil || len(b) != common.HashLength {
			return fmt.Errorf("-hash %q is not a code hash, 0x and 64 hex digits", hashArg)
		}
		h = common.BytesToHash(b)
	}
	em, err := extensionmanager.NewExtensionManager(s.c.Manager, s.c.Client)
	if err != nil {
		return err
	}
	info, err := em.GetCodeHashInfo(s.call(ctx), id, h)
	if err != nil {
		return fmt.Errorf("reading code hash %s: %w", h, err)
	}
	if len(info.Platforms) == 0 {
		return fmt.Errorf("extension %s doesn't allow code hash %s", id, h)
	}
	targets := info.Platforms
	if platformList != "" {
		if targets, err = s.platforms(ctx, em, strings.Split(platformList, ",")); err != nil {
			return err
		}
		for _, p := range targets {
			if !slices.Contains(info.Platforms, p) {
				return fmt.Errorf("code hash %s isn't allowed on %s", h, chain.String32(p))
			}
		}
	}
	names := make([]string, 0, len(targets))
	var todo [][32]byte
	for _, p := range targets {
		names = append(names, chain.String32(p))
		off, err := em.IsCodeHashPlatformDisabled(s.call(ctx), id, h, p)
		if err != nil {
			return fmt.Errorf("reading code hash %s on %s: %w", h, chain.String32(p), err)
		}
		if !off {
			todo = append(todo, p)
		}
	}

	if len(todo) > 0 {
		fmt.Println("WARN  disabling is permanent: the registry can't enable a code hash again, nor allow it as a new version; its machines in production on those platforms are paused")
		opts, err := s.w.Opts(ctx, s.c)
		if err != nil {
			return err
		}
		tx, err := em.DisableCodeHashPlatforms(opts, id, h, todo)
		if err != nil {
			return fmt.Errorf("disableCodeHashPlatforms: %w", err)
		}
		if _, err := s.c.Wait(ctx, tx.Hash()); err != nil {
			return fmt.Errorf("disableCodeHashPlatforms: %w", err)
		}
	}
	if i := slices.IndexFunc(s.rec.Versions, func(v record.Version) bool { return v.CodeHash == h }); i >= 0 {
		v := &s.rec.Versions[i]
		for _, n := range names {
			if !slices.Contains(v.Disabled, n) {
				v.Disabled = append(v.Disabled, n)
			}
		}
		slices.Sort(v.Disabled)
		if err := s.save(); err != nil {
			return err
		}
	}
	if len(todo) == 0 {
		stepOK("disable-version", "%s on %s (disabled already)", h, strings.Join(names, ", "))
		return nil
	}
	stepOK("disable-version", "%s on %s", h, strings.Join(names, ", "))
	return nil
}
