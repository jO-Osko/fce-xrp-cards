package imagecheck

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// probeModule is the probe's import path root, as in its source.
const probeModule = "github.com/flare-foundation/fce-tooling"

// probeSrc is the probe's source; it imports only the standard library, so it builds as a module of its own.
//
//go:embed cmd/imageprobe internal/probe
var probeSrc embed.FS

// BuildProbe builds the probe for the image's platform into dir, with the go command on PATH, and returns its path.
func BuildProbe(ctx context.Context, dir string) (string, error) {
	src := filepath.Join(dir, "src")
	err := fs.WalkDir(probeSrc, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := probeSrc.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(src, "imagecheck", filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		return "", fmt.Errorf("writing the probe's source: %w", err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module "+probeModule+"\n\ngo 1.26\n"), 0o644); err != nil {
		return "", fmt.Errorf("writing the probe's go.mod: %w", err)
	}

	bin := filepath.Join(dir, "imageprobe")
	goos, goarch, _ := strings.Cut(platform, "/")
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", bin, "./imagecheck/cmd/imageprobe")
	cmd.Dir = src
	cmd.Env = append(cmd.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch, "GOWORK=off", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("building the probe: %w\n%s", err, out)
	}
	return bin, nil
}
