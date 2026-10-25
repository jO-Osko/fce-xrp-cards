package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/flare-foundation/fce-tooling/imagecheck"
)

// makeCmd runs the fork's make target, image or repro, for each dir: the recipe is the fork's.
func makeCmd(ctx context.Context, target string, args []string) int {
	fs := flag.NewFlagSet(target, flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	exts, err := start(fs.Args())
	if err != nil {
		return startErr(err)
	}
	for _, e := range exts {
		if err := runMake(ctx, e, target); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}

func imageCheckCmd(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("image-check", flag.ContinueOnError)
	relax := fs.Bool(relaxFlag, false, relaxUsage)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	exts, err := start(fs.Args())
	if err != nil {
		return startErr(err)
	}
	dir, err := os.MkdirTemp("", "fce-image-check")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temporary directory
	probe, err := imagecheck.BuildProbe(ctx, dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	code := 0
	for _, e := range exts {
		fmt.Printf("\n%s:\n", e.Dir)
		r := relaxed(*relax, e)
		if err := runMake(ctx, e, "image"); err != nil {
			fmt.Printf("FAIL  %v\n", err)
			code = 1
			continue
		}
		rep := imagecheck.Run(ctx, imagecheck.Config{
			Root:       e.Root,
			Image:      "fce/" + e.Dir,
			Dockerfile: filepath.Join(e.Root, filepath.FromSlash(e.Dir), "Dockerfile"),
			Probe:      probe,
		})
		if !printImageReport(rep, r) {
			code = 1
		}
	}
	return code
}

// printImageReport prints every check, and the logs of containers whose checks found something; it reports whether all passed.
func printImageReport(rep imagecheck.Report, relaxed bool) bool {
	if rep.Digest != "" {
		fmt.Printf("config %s\n", rep.Digest)
	}
	checks := make([]check, 0, len(rep.Results))
	for _, r := range rep.Results {
		checks = append(checks, check{name: r.Name, err: r.Err, advice: r.Advice})
	}
	t := printChecks(checks, relaxed)
	if !t.ok() && len(rep.Logs) > 0 {
		fmt.Printf("\n%s", strings.Join(rep.Logs, ""))
	}
	t.print()
	return t.ok()
}
