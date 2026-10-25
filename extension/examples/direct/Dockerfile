# The extension's TEE image: the launcher as PID 1, tee-node as root, the extension as 65532.
# Context: the repository root. Build it with make image: Confidential Space attests the image's hash,
# and REPRODUCIBILITY.md lists what the hash depends on.
# A "# per extension:" comment marks the lines below it, up to the next blank line, as the extension's own;
# the rest stays as in the template.

# a constant, not the commit time, so the same code gives the same hash at any commit; a bump changes every hash
ARG SOURCE_DATE_EPOCH=1790812800

FROM golang:1.26.9-trixie@sha256:f06b7122f888680048afb247c4e5f49c0069eb6c4c1ccae0f3f4834527a8d22c AS go
# a go.mod newer than this image fails the build instead of downloading a toolchain
ENV GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
WORKDIR /src

# tee-node at the go.mod pin, built with its own go.mod and upstream's flags; cgo: its VRF signing needs it.
# The touch: rewrite-timestamp only clamps newer mtimes down (moby/buildkit#3180).
FROM go AS tee-node
ARG SOURCE_DATE_EPOCH
COPY --chmod=644 --chown=0:0 go.mod go.sum ./
RUN m=github.com/flare-foundation/tee-node && \
    { test -z "$(go list -m -f '{{with .Replace}}x{{end}}' "$m")" || \
        { echo "go.mod replaces $m: the image runs only the version go.mod requires, so drop the replace" >&2; exit 1; }; } && \
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GOBIN=/out go install -tags=netgo,osusergo -trimpath \
        -ldflags="-buildid= -s -w -linkmode=external -extldflags=-static" \
        "$m/cmd/extension@$(go list -m -f '{{.Version}}' "$m")" && \
    mv /out/extension /out/tee-node && \
    find /out -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +

FROM go AS launcher
ARG SOURCE_DATE_EPOCH
WORKDIR /src/launcher
COPY --chmod=644 --chown=0:0 launcher/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-buildid= -s -w" -o /out/launcher . && \
    find /out -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +

FROM go AS extension
ARG SOURCE_DATE_EPOCH
COPY --chmod=644 --chown=0:0 go.mod go.sum ./
RUN go mod download
COPY --chmod=644 --chown=0:0 pkg/ pkg/

# per extension: its packages, then its main package
COPY --chmod=644 --chown=0:0 examples/direct/cmd/ examples/direct/cmd/
COPY --chmod=644 --chown=0:0 examples/direct/internal/ examples/direct/internal/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-buildid= -s -w" -o /out/extension ./examples/direct/cmd/extension && \
    find /out -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +

FROM gcr.io/distroless/static-debian12@sha256:20bc6c0bc4d625a22a8fde3e55f6515709b32055ef8fb9cfbddaa06d1760f838

WORKDIR /app
COPY --chmod=755 --chown=0:0 --from=launcher /out/launcher /app/launcher
COPY --chmod=755 --chown=0:0 --from=tee-node /out/tee-node /app/tee-node
COPY --chmod=755 --chown=0:0 --from=extension /out/extension /app/extension

# production mode; a local run outside Confidential Space passes MODE=1
ENV MODE=0
# not in the launch policy, so fixed by the image
ENV SIGN_PORT=8888 EXTENSION_PORT=8889

# per extension: its name, and the version tooling registers (at most 32 bytes)
LABEL org.opencontainers.image.title="direct-example" \
      org.opencontainers.image.version="0.1.0"

# the variables an operator may set at launch: tee-node's, then the extension's own, also listed in the launcher's --env
# per extension: its own variables, appended
LABEL "tee.launch_policy.allow_env_override"="LOG_LEVEL,PROXY_URL,INITIAL_OWNER,EXTENSION_ID,CHAIN_ID,GOVERNANCE_SIGNERS,GOVERNANCE_THRESHOLD,GOVERNANCE_SAFE,GOVERNANCE_TEE_MANAGER"

# tee-node's config port; the sign and extension ports stay inside
EXPOSE 5500

# root: tee-node needs Confidential Space's root-only attestation socket; the launcher drops the extension to --uid
# hadolint ignore=DL3002
USER 0:0

# no CMD: every launcher argument stays here, in the attested config
# per extension: its own variables as "--env", "NAME,OTHER", before "--"
ENTRYPOINT ["/app/launcher", "--node", "/app/tee-node", "--uid", "65532", "--", "/app/extension"]
