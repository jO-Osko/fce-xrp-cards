package imagecheck

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const digest = "@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// goodDockerfile has the template's shape: a global epoch, pinned images, stages, --chmod on context copies.
const goodDockerfile = `# a header comment, not a directive
ARG SOURCE_DATE_EPOCH=1790812800

FROM golang:1.26` + digest + ` AS go
RUN go version

from go as build
ARG SOURCE_DATE_EPOCH
COPY --chmod=644 --chown=0:0 go.mod go.sum ./
# a comment inside a continuation
RUN --mount=type=cache,target=/root/.cache \
    # another
    go build -o /out/x . && \
    touch /out/x

FROM scratch AS empty

FROM gcr.io/distroless/static` + digest + `
COPY --chmod=755 --from=build /out/x /x
COPY --from=0 /etc/hostname /h
COPY --from=empty / /e
ENTRYPOINT ["/x"]
`

func results(t *testing.T, src string) map[string]error {
	t.Helper()
	df, err := parseDockerfile(src)
	require.NoError(t, err)
	m := map[string]error{}
	for _, r := range dockerfileResults(df) {
		require.NoError(t, r.Err, r.Name)
		m[r.Name] = r.Advice
	}
	return m
}

func TestParseDockerfile(t *testing.T) {
	t.Parallel()
	df, err := parseDockerfile("# syntax=docker/dockerfile:1\n# Escape=\\\n\n" + goodDockerfile)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"syntax": "docker/dockerfile:1", "escape": `\`}, df.directives)

	cmds := make([]string, 0, len(df.instructions))
	for _, in := range df.instructions {
		cmds = append(cmds, in.cmd)
	}
	assert.Equal(t, []string{"ARG", "FROM", "RUN", "FROM", "ARG", "COPY", "RUN", "FROM", "FROM", "COPY", "COPY", "COPY", "ENTRYPOINT"}, cmds)
	run := df.instructions[6]
	assert.Equal(t, 14, run.line)
	assert.Equal(t, "--mount=type=cache,target=/root/.cache go build -o /out/x . && touch /out/x", run.args)

	// a directive after the first other line is a comment
	df, err = parseDockerfile("FROM scratch\n# syntax=x\n")
	require.NoError(t, err)
	assert.Empty(t, df.directives)
}

func TestParseDockerfileRefuses(t *testing.T) {
	t.Parallel()
	_, err := parseDockerfile("# escape=`\nFROM scratch\n")
	require.ErrorContains(t, err, "escape")
	_, err = parseDockerfile("FROM scratch\nRUN a \\\n")
	require.ErrorContains(t, err, "continuation")
}

func TestEpoch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src string
		want      int64
		err       string
	}{
		{name: "global", src: "ARG A=1 SOURCE_DATE_EPOCH=42\nFROM scratch\n", want: 42},
		{name: "quoted", src: "ARG SOURCE_DATE_EPOCH=\"42\"\nFROM scratch\n", want: 42},
		{name: "only in a stage", src: "FROM scratch\nARG SOURCE_DATE_EPOCH=42\n", err: "before the first FROM"},
		{name: "no value", src: "ARG SOURCE_DATE_EPOCH\nFROM scratch\n", err: "before the first FROM"},
		{name: "not a number", src: "ARG SOURCE_DATE_EPOCH=$NOW\nFROM scratch\n", err: "positive number"},
		{name: "zero", src: "ARG SOURCE_DATE_EPOCH=0\nFROM scratch\n", err: "positive number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			df, err := parseDockerfile(tt.src)
			require.NoError(t, err)
			got, err := df.epoch()
			if tt.err != "" {
				require.ErrorContains(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDockerfileResultsPass(t *testing.T) {
	t.Parallel()
	for name, err := range results(t, goodDockerfile) {
		assert.NoError(t, err, name)
	}
	// a syntax directive pinned by digest is allowed
	for name, err := range results(t, "# syntax=docker/dockerfile:1"+digest+"\n"+goodDockerfile) {
		assert.NoError(t, err, name)
	}
}

func TestDockerfileResultsFail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, from, to, rule string
	}{
		{"no epoch", "ARG SOURCE_DATE_EPOCH=1790812800\n", "", "SOURCE_DATE_EPOCH"},
		{"syntax", "# a header", "# syntax=docker/dockerfile:1\n# a header", "syntax directive"},
		{"tag only", "golang:1.26" + digest, "golang:1.26", "pinned by digest"},
		{"variable", "golang:1.26" + digest, "golang:${GO}" + digest, "pinned by digest"},
		{"copy from an image", "--from=0", "--from=busybox:1", "pinned by digest"},
		{"mount from an image", "type=cache,target", "type=bind,from=busybox,target", "pinned by digest"},
		{"no chmod", "COPY --chmod=644 --chown=0:0 go.mod", "COPY --chown=0:0 go.mod", "--chmod"},
		{"add", "ENTRYPOINT", "ADD x /x\nENTRYPOINT", "no ADD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := strings.Replace(goodDockerfile, tt.from, tt.to, 1)
			require.NotEqual(t, goodDockerfile, src)
			for name, err := range results(t, src) {
				if strings.Contains(name, tt.rule) {
					assert.Error(t, err, name)
				} else {
					assert.NoError(t, err, name)
				}
			}
		})
	}
}
