// Command fce is the tooling for Flare Confidential Compute extensions.
//
// Usage:
//
//	fce conformance [-i-know-what-im-doing] [flags] dir...
//	fce conformance [-i-know-what-im-doing] [flags] [-fixtures dir] -- command [args...]
//	fce image dir...
//	fce repro dir...
//	fce image-check [-i-know-what-im-doing] dir...
//	fce encrypt -pubkey key [-tee address] < plaintext
//	fce deploy (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] dir
//	fce add-version (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] (-platform names | -test) [-i-know-what-im-doing] dir
//	fce governance (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] -signers addresses -threshold n dir
//	fce disable-version (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] (-hash codehash | -test) [-platform names] dir
//	fce status (-chain name | -rpc url) dir
//	fce run (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] -indexer-db file [-url url] [-port n] [-detach] [-i-know-what-im-doing] dir
//	fce stop (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] dir
//	fce send (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] [-value wei] [-timeout duration] dir (signature [args...] | calldata)
//	fce direct (-chain name | -rpc url) [-timeout duration] dir opType opCommand [message]
//	fce version
//
// Each dir is an extension directory in a scaffold fork, such as ./template.
// fce refuses a fork whose fce-version file names other fce versions.
//
// A check is required or a recommendation. Both fail, unless the
// i-know-what-im-doing mode is on, by the flag or by a file
// fce-i-know-what-im-doing in the extension's directory: then
// recommendations only warn.
//
// conformance builds the extension in dir as its image does (or runs the
// command after --), starts it on free loopback ports against a fake sign
// port, runs the generic checks and the fixtures in dir/conformance, and stops
// it with SIGTERM. image and repro run the fork's make image and make repro
// for dir. image-check runs make image for dir, then checks the image from the
// outside: its Dockerfile and attested config, hadolint, the binaries it runs,
// and a smoke run of the image as shipped. Each exits 1 if anything failed.
//
// deploy, add-version and governance register the extension in dir on a chain,
// signing with a key from a Foundry keystore, and keep what they did in the
// extension's deployment record, dir/deployments/<chain>.json. deploy runs
// the steps an extension needs once: its contracts/script/Deploy.s.sol,
// ExtensionManager.register, the deployer as machine owner, and its
// contracts/script/PostRegister.s.sol; run again, it finishes what is left.
// add-version builds the image and allows its config digest, under its version
// label, on the platforms named. governance sets the signers that govern the
// extension's machines. disable-version disables a code hash, on all its
// platforms or those named, and pauses its machines in production there; the
// registry can't undo it. status compares the record with the chain.
//
// run starts a test machine of the extension in dir, tee-node in test mode
// with tee-proxy beside it, reading the indexer database -indexer-db names;
// it registers the machine, takes it to production, and shows its logs until
// interrupted. Stopping it, on Ctrl-C or with stop after -detach, pauses the
// machine on chain and removes it. send calls the extension's contract and
// waits for the result of each instruction the call sent; direct posts a
// direct action to the running machine and waits for its result. Both check
// the TEE's and the proxy's signatures.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/flare-foundation/fce-tooling/internal/fork"
)

const usage = `usage:
  fce conformance [-i-know-what-im-doing] [flags] dir...
  fce conformance [-i-know-what-im-doing] [flags] [-fixtures dir] -- command [args...]
  fce image dir...
  fce repro dir...
  fce image-check [-i-know-what-im-doing] dir...
  fce encrypt -pubkey key [-tee address] < plaintext
  fce deploy (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] dir
  fce add-version (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] (-platform names | -test) [-i-know-what-im-doing] dir
  fce governance (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] -signers addresses -threshold n dir
  fce disable-version (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] (-hash codehash | -test) [-platform names] dir
  fce status (-chain name | -rpc url) dir
  fce run (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] -indexer-db file [-url url] [-port n] [-detach] [-i-know-what-im-doing] dir
  fce stop (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] dir
  fce send (-chain name | -rpc url) (-account name | -keystore file) [-password-file file] [-value wei] [-timeout duration] dir (signature [args...] | calldata)
  fce direct (-chain name | -rpc url) [-timeout duration] dir opType opCommand [message]
  fce version`

// relaxFlag is the i-know-what-im-doing mode's flag; fork.RelaxFile turns it on per extension.
const relaxFlag = "i-know-what-im-doing"

const relaxUsage = "the i-know-what-im-doing mode: recommendations only warn; required checks still fail"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "conformance":
		return conformanceCmd(ctx, args[1:])
	case "image", "repro":
		return makeCmd(ctx, args[0], args[1:])
	case "image-check":
		return imageCheckCmd(ctx, args[1:])
	case "encrypt":
		return encryptCmd(args[1:], os.Stdin, os.Stdout)
	case "deploy":
		return deployCmd(ctx, args[1:])
	case "add-version":
		return addVersionCmd(ctx, args[1:])
	case "governance":
		return governanceCmd(ctx, args[1:])
	case "disable-version":
		return disableVersionCmd(ctx, args[1:])
	case "status":
		return statusCmd(ctx, args[1:])
	case "run":
		return runCmd(ctx, args[1:])
	case "stop":
		return stopCmd(ctx, args[1:])
	case "send":
		return sendCmd(ctx, args[1:])
	case "direct":
		return directCmd(ctx, args[1:])
	case "version":
		fmt.Println("fce", version())
		return 0
	default:
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
}

// version is the module version fce was installed at, with the commit when built from a checkout.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := []string{bi.Main.Version}
	for _, s := range bi.Settings {
		switch {
		case s.Key == "vcs.revision":
			v = append(v, s.Value)
		case s.Key == "vcs.modified" && s.Value == "true":
			v = append(v, "(modified)")
		}
	}
	return strings.Join(v, " ")
}

// errUsage means the arguments were wrong; usage is printed.
var errUsage = errors.New("usage")

// start prints fce's version, then finds each dir's extension and checks that its fork works with this fce.
func start(dirs []string) ([]fork.Extension, error) {
	if len(dirs) == 0 {
		return nil, errUsage
	}
	fmt.Println("fce", version())
	var mainVersion, install string
	if bi, ok := debug.ReadBuildInfo(); ok {
		mainVersion, install = bi.Main.Version, bi.Path
	}
	exts := make([]fork.Extension, 0, len(dirs))
	checked := map[string]bool{}
	for _, d := range dirs {
		e, err := fork.Find(d)
		if err != nil {
			return nil, err
		}
		if !checked[e.Root] {
			checked[e.Root] = true
			warn, err := fork.Check(e.Want, mainVersion)
			if err != nil {
				return nil, fmt.Errorf("%s: %w\ninstall it: go install %s@%s", e.Root, err, install, e.Want)
			}
			if warn != "" {
				fmt.Printf("WARN  %s\n", warn)
			}
		}
		exts = append(exts, e)
	}
	return exts, nil
}

// startErr prints start's error and returns the exit code.
func startErr(err error) int {
	if errors.Is(err, errUsage) {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// relaxed reports whether the i-know-what-im-doing mode is on for e, by flag or by its file, and says so when it is.
func relaxed(flagSet bool, e fork.Extension) bool {
	switch {
	case flagSet:
		fmt.Printf("WARN  the i-know-what-im-doing mode is on (-%s): recommendations only warn\n", relaxFlag)
	case e.Relaxed && e.Reason == "":
		fmt.Printf("WARN  the i-know-what-im-doing mode is on (%s/%s, no reason given): recommendations only warn\n", e.Dir, fork.RelaxFile)
	case e.Relaxed:
		fmt.Printf("WARN  the i-know-what-im-doing mode is on (%s/%s: %s): recommendations only warn\n", e.Dir, fork.RelaxFile, e.Reason)
	}
	return flagSet || e.Relaxed
}

// check is one check's outcome as fce prints it: err for a required check, advice for a recommendation.
type check struct {
	name        string
	err, advice error
}

// tally counts printed checks.
type tally struct {
	total, failed, recommended, warned int
}

func (t tally) ok() bool { return t.failed == 0 }

// printChecks prints each check; relaxed, a recommendation not followed warns instead of failing.
func printChecks(checks []check, relaxed bool) tally {
	t := tally{total: len(checks)}
	for _, c := range checks {
		if c.err != nil {
			fmt.Printf("FAIL  %s: %s\n", c.name, indent(c.err))
		}
		switch {
		case c.advice != nil && relaxed:
			fmt.Printf("WARN  %s: %s\n", c.name, indent(c.advice))
		case c.advice != nil:
			fmt.Printf("FAIL  %s (recommended): %s\n", c.name, indent(c.advice))
		case c.err == nil:
			fmt.Printf("ok    %s\n", c.name)
		}
		switch {
		case c.err != nil:
			t.failed++
		case c.advice != nil && relaxed:
			t.warned++
		case c.advice != nil:
			t.failed++
			t.recommended++
		}
	}
	return t
}

// indent aligns a multi-line message's continuation lines under its first.
func indent(err error) string { return strings.ReplaceAll(err.Error(), "\n", "\n      ") }

// print prints the summary line.
func (t tally) print() {
	switch {
	case t.failed > 0 && t.recommended > 0:
		fmt.Printf("\n%d of %d checks failed, %d of them recommendations: the i-know-what-im-doing mode makes those warnings\n", t.failed, t.total, t.recommended)
	case t.failed > 0:
		fmt.Printf("\n%d of %d checks failed\n", t.failed, t.total)
	case t.warned > 0:
		fmt.Printf("\nevery required check passed; %d of %d checks warned\n", t.warned, t.total)
	default:
		fmt.Printf("\nall %d checks passed\n", t.total)
	}
}

// runMake runs the fork's make target for e, passing its output through.
func runMake(ctx context.Context, e fork.Extension, target string) error {
	cmd := exec.CommandContext(ctx, "make", "--no-print-directory", "-C", e.Root, target, "EXTENSIONS="+e.Dir)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("make %s for %s: %w", target, e.Dir, err)
	}
	return nil
}
