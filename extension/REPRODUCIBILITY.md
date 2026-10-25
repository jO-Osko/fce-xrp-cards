# Reproducibility

Confidential Space attests an extension's image by its config digest: the image's hash.
The chain registers that hash for the extension, and a TEE whose image hashes differently doesn't get in.
This repository's images build reproducibly: byte-for-byte the same code gives the same hash, on any machine, on any day, whatever the commit.
So anyone can check a registered hash by rebuilding it from source; the images are never published.

## Verify a registered hash

You need the extension's repository, Docker with buildx, and network access to the base images and the Go module proxy.

1. Check out the commit that was registered.
   `fce add-version` records it with each version in `<extension directory>/deployments/<chain>.json`, unless the version is a test one or was built from uncommitted changes, and `fce status` shows it.
2. Run `make image`.
   It prints each extension's digest, e.g. `template: fce/template sha256:…`.
3. Compare the digest with the code hashes registered on the chain for the extension.
   `fce status -chain <network> <extension directory>`, with the tooling repo's CLI, lists them, using the deployment record the fork keeps in `<extension directory>/deployments/`.

The same digest means the image the TEE runs was built from that commit.
If it differs, run `make repro`, which builds twice without cache.
If those two match each other but not the chain, the registered image was built from other code.

The image is for `linux/amd64`, the platform of every Confidential Space machine on Google Cloud.
Builds are tested on Linux hosts on amd64; other hosts build that image through emulation, which isn't tested yet.

## Keep the build reproducible

### What the hash depends on

| Input | Where it's set |
|---|---|
| the Dockerfile | `<extension>/Dockerfile` |
| the files the Dockerfile copies | `go.mod`, `go.sum`, `pkg/`, `launcher/`, and the extension's own packages |
| tee-node | the version `go.mod` requires |
| the base images, and with the `golang` image the C toolchain tee-node is built with | their digests in the Dockerfile |
| BuildKit | its digest in the `Makefile` |
| the build time written into the image | `SOURCE_DATE_EPOCH` in the Dockerfile |

Nothing else changes it: docs, contracts and tests don't reach the image.
Every extension shares the root `go.mod`, so a dependency bump for one changes every extension's hash.

### SOURCE_DATE_EPOCH is a constant

The Dockerfile sets it to a fixed time, not the commit's.
Every file in the image and the config's `created` field carry it, so the same code gives the same hash whatever the commit.
tee-node's own image uses the commit time instead, so identical code at two commits gets two hashes.
Raising it changes every hash, so it is reviewed like a release.

### Build with make image

`make image` builds the way the hash needs:

- with the BuildKit the `Makefile` pins, in a builder of its own;
- for `linux/amd64`, the TEE's platform;
- with `--build-arg SOURCE_DATE_EPOCH` set to the Dockerfile's constant: BuildKit before v0.30 ignores the Dockerfile's default, and a `SOURCE_DATE_EPOCH` in your environment would override it;
- with `rewrite-timestamp=true`, which caps every file's time in the image at that constant;
- with `BUILDX_GIT_LABELS` unset: set, buildx adds the commit as labels, so the hash changes with every commit.

A plain `docker build` gives another hash, even with the build arg.

### Traps

| Trap | What happens |
|---|---|
| a `# syntax=` line not pinned by digest | it swaps the Dockerfile frontend, and the build can change with it |
| a base image by tag, not digest | the build changes when the tag moves |
| a `COPY` from the repository without `--chmod` | file modes follow each checkout's umask |
| a `replace` of tee-node in `go.mod` | the image build fails: the image runs only the version `go.mod` requires |
| a comment or blank line that moves code in the extension, `pkg/` or `launcher/` | it changes the hash: Go records line numbers in the binary |

`fce image-check` reports the first three, and `fce repro` checks a build end to end.
