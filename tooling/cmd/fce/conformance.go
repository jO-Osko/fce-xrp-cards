package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/flare-foundation/fce-tooling/conformance"
	"github.com/flare-foundation/fce-tooling/internal/fork"
)

// envFlag collects repeated -env flags.
type envFlag []string

func (e *envFlag) String() string { return strings.Join(*e, ",") }

func (e *envFlag) Set(kv string) error {
	if !strings.Contains(kv, "=") {
		return fmt.Errorf("%q is not KEY=VALUE", kv)
	}
	*e = append(*e, kv)
	return nil
}

// conformanceFlags returns conformance's flags, parsed into cfg, the -fixtures directory and the mode's flag.
func conformanceFlags(cfg *conformance.Config) (*flag.FlagSet, *string, *bool) {
	fs := flag.NewFlagSet("conformance", flag.ContinueOnError)
	relax := fs.Bool(relaxFlag, false, relaxUsage)
	fixtures := fs.String("fixtures", "", "with -- command: the directory of the extension's fixtures (*.json); empty runs only the generic checks")
	fs.Var((*envFlag)(&cfg.Env), "env", "a variable for the extension, KEY=VALUE; repeatable")
	fs.DurationVar(&cfg.Startup, "startup", conformance.DefaultStartup, "how long the extension may take to answer GET /state")
	fs.DurationVar(&cfg.Shutdown, "shutdown", conformance.DefaultShutdown, "how long the extension may take to exit after SIGTERM")
	fs.DurationVar(&cfg.PostWait, "postwait", conformance.DefaultPostWait, "how long to wait for the posts long-running fixtures expect")
	return fs, fixtures, relax
}

// commandMode reports whether args, parsed down to rest, ended their flags with --; flag.Parse drops the --.
func commandMode(args, rest []string) bool {
	n := len(args) - len(rest)
	return n > 0 && args[n-1] == "--"
}

func conformanceCmd(ctx context.Context, args []string) int {
	var cfg conformance.Config
	fs, fixtures, relax := conformanceFlags(&cfg)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if commandMode(args, fs.Args()) {
		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, usage)
			return 2
		}
		fmt.Println("fce", version())
		cfg.Command = fs.Args()
		if *fixtures != "" {
			f, err := conformance.Load(*fixtures)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 2
			}
			cfg.Fixtures = f
		}
		if !printConformanceReport(conformance.Run(ctx, cfg), relaxed(*relax, fork.Extension{})) {
			return 1
		}
		return 0
	}

	if *fixtures != "" {
		fmt.Fprintln(os.Stderr, "-fixtures goes with -- command; an extension directory's fixtures are its conformance/")
		return 2
	}
	exts, err := start(fs.Args())
	if err != nil {
		return startErr(err)
	}
	dir, err := os.MkdirTemp("", "fce-conformance")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temporary directory

	code := 0
	for i, e := range exts {
		fmt.Printf("\n%s:\n", e.Dir)
		r := relaxed(*relax, e)
		extDir := filepath.Join(e.Root, filepath.FromSlash(e.Dir))
		f, err := conformance.Load(filepath.Join(extDir, "conformance"))
		if err != nil {
			fmt.Printf("FAIL  %v\n", err)
			code = 1
			continue
		}
		// as the image builds it: no cgo, the fork's go.mod alone
		bin := filepath.Join(dir, fmt.Sprintf("extension-%d", i))
		build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/extension")
		build.Dir, build.Env = extDir, append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			fmt.Printf("FAIL  building %s: %v\n%s", e.Dir, err, out)
			code = 1
			continue
		}
		c := cfg
		c.Command, c.Fixtures = []string{bin}, f
		if !printConformanceReport(conformance.Run(ctx, c), r) {
			code = 1
		}
	}
	return code
}

// printConformanceReport prints every check, and the extension's output when one failed; it reports whether all passed.
func printConformanceReport(rep conformance.Report, relaxed bool) bool {
	checks := make([]check, 0, len(rep.Results))
	for _, r := range rep.Results {
		checks = append(checks, check{name: r.Name, err: r.Err, advice: r.Advice})
	}
	t := printChecks(checks, relaxed)
	if rep.Err != nil {
		fmt.Printf("FAIL  %v\n", rep.Err)
	}
	if !rep.OK(relaxed) {
		fmt.Printf("\n--- extension output ---\n%s--- end of output ---\n", rep.Output)
	}
	t.print()
	return t.ok() && rep.Err == nil
}
