package imagecheck

import (
	"context"
	"os"
)

// Image is what fce registers of a built image.
type Image struct {
	// Digest is the config digest, sha256:<hex>, the hash Confidential Space attests.
	Digest  string
	Version string
	// GitLabels are the labels that carry the commit.
	GitLabels []string
}

// Inspect reads the built image's config.
func Inspect(ctx context.Context, image string) (Image, error) {
	dir, err := os.MkdirTemp("", "imagecheck")
	if err != nil {
		return Image{}, err
	}
	defer os.RemoveAll(dir) //nolint:errcheck // a temporary directory
	c := &checker{cfg: Config{Image: image}, d: newDocker()}
	ic, digest, err := c.config(ctx, dir)
	if err != nil {
		return Image{}, err
	}
	img := Image{Digest: digest, Version: ic.Config.Labels[labelVersion]}
	for _, l := range gitLabels {
		if _, set := ic.Config.Labels[l]; set {
			img.GitLabels = append(img.GitLabels, l)
		}
	}
	return img, nil
}
