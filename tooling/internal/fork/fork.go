// Package fork finds the scaffold fork an extension directory belongs to, and
// the fce versions that fork works with.
package fork

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// VersionFile, at a fork's root, names the fce versions the fork works with: a version prefix such as v0.1.
const VersionFile = "fce-version"

// RelaxFile, in an extension's directory, turns on the i-know-what-im-doing mode for it; its text is the reason.
const RelaxFile = "fce-i-know-what-im-doing"

var prefixRE = regexp.MustCompile(`^v[0-9]+(\.[0-9]+)?$`)

// pseudoRE matches a pseudo-version, an untagged commit; as golang.org/x/mod/module.
var pseudoRE = regexp.MustCompile(`^v[0-9]+\.(0\.0-|\d+\.\d+-([^+]*\.)?0\.)\d{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// Extension is an extension directory in a fork.
type Extension struct {
	// Root is the fork's root, the nearest directory up with VersionFile; absolute.
	Root string
	// Dir is the extension's directory relative to Root, slash-separated, as the fork's Makefile names it.
	Dir string
	// Want is VersionFile's version prefix.
	Want string
	// Relaxed is set when the extension has RelaxFile; Reason is its text.
	Relaxed bool
	Reason  string
}

// Find returns the extension at dir.
func Find(dir string) (Extension, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Extension{}, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Extension{}, fmt.Errorf("%s is not a directory", dir)
	}
	root := abs
	for {
		b, err := os.ReadFile(filepath.Join(root, VersionFile))
		if err == nil {
			want := strings.TrimSpace(string(b))
			if !prefixRE.MatchString(want) {
				return Extension{}, fmt.Errorf("%s: %q is not a version prefix such as v0.1", filepath.Join(root, VersionFile), want)
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				return Extension{}, err
			}
			if rel == "." {
				return Extension{}, fmt.Errorf("%s is the fork's root, not an extension directory", dir)
			}
			e := Extension{Root: root, Dir: filepath.ToSlash(rel), Want: want}
			switch reason, err := os.ReadFile(filepath.Join(abs, RelaxFile)); {
			case err == nil:
				e.Relaxed, e.Reason = true, strings.TrimSpace(string(reason))
			case !errors.Is(err, os.ErrNotExist):
				return Extension{}, err
			}
			return e, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Extension{}, err
		}
		up := filepath.Dir(root)
		if up == root {
			return Extension{}, fmt.Errorf("no %s in %s or above: is it in a scaffold fork?", VersionFile, dir)
		}
		root = up
	}
}

// ErrUnsupported means the fork wants another fce version.
var ErrUnsupported = errors.New("unsupported fce version")

// Check reports whether fce at version, its module version, may run on a fork that wants the version prefix want;
// a development build (untagged, modified or unversioned) may, with a warning.
func Check(want, version string) (string, error) {
	if version == "" || version == "(devel)" || strings.HasSuffix(version, "+dirty") || pseudoRE.MatchString(version) {
		return fmt.Sprintf("fce %s is a development build; this fork wants fce %s", version, want), nil
	}
	if version != want && !strings.HasPrefix(version, want+".") {
		return "", fmt.Errorf("%w: fce is %s, this fork wants fce %s", ErrUnsupported, version, want)
	}
	return "", nil
}
