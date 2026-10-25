// Package golden keeps an extension's conformance fixtures in step with the
// fixtures its tests build, for its TestFixtures.
package golden

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/flare-foundation/fce-go-scaffold/conformance"
)

// Check compares the files in dir with fixtures, each encoded as <Name>.json,
// and fails on a missing, extra or stale file; with update, it rewrites dir to
// match instead. Either way it then loads dir as the runner does, which also
// checks across fixtures, such as one channel per op pair.
func Check(dir string, fixtures []conformance.Fixture, update bool) error {
	want := make(map[string][]byte, len(fixtures))
	for _, f := range fixtures {
		b, err := f.Encode()
		if err != nil {
			return err
		}
		name := f.Name + ".json"
		if _, ok := want[name]; ok {
			return fmt.Errorf("two fixtures named %s", f.Name)
		}
		want[name] = b
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return fmt.Errorf("listing %s: %w", dir, err)
	}
	if update {
		err = write(dir, files, want)
	} else {
		err = compare(files, want)
	}
	if err != nil {
		return err
	}

	_, err = conformance.Load(dir)
	return err
}

// write removes the files not in want, then writes want.
func write(dir string, files []string, want map[string][]byte) error {
	for _, p := range files {
		if _, ok := want[filepath.Base(p)]; !ok {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("removing %s: %w", p, err)
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	for name, b := range want {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", p, err)
		}
	}
	return nil
}

// compare names every missing, extra or stale file.
func compare(files []string, want map[string][]byte) error {
	var errs []error
	have := map[string]bool{}
	for _, p := range files {
		name := filepath.Base(p)
		have[name] = true
		b, ok := want[name]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: no such fixture", name))
			continue
		}
		got, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		if !bytes.Equal(got, b) {
			errs = append(errs, fmt.Errorf("%s: stale", name))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(want)) {
		if !have[name] {
			errs = append(errs, fmt.Errorf("%s: missing", name))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("fixture files differ; run with -update:\n%w", err)
	}
	return nil
}
