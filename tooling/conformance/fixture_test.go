package conformance

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := goodFixtures()
	for _, f := range want {
		b, err := f.Encode()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, f.Name+".json"), b, 0o644))
	}

	got, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, got, len(want))
	slices.SortFunc(want, func(a, b Fixture) int { return strings.Compare(a.Name, b.Name) })
	for i := range want {
		assert.Equal(t, want[i].Name, got[i].Name)
		assert.Equal(t, want[i].SubmissionTag, got[i].SubmissionTag)
		assert.Equal(t, want[i].Message, got[i].Message)
		assert.Equal(t, want[i].Result, got[i].Result)
		assert.Equal(t, want[i].Again, got[i].Again)
		assert.Equal(t, want[i].Posts, got[i].Posts)
	}
}

func TestLoadRefuses(t *testing.T) {
	t.Parallel()
	const pair = `"opType":"A","opCommand":"B"`
	tests := []struct {
		name   string
		files  map[string]string
		reason string
	}{
		{"empty dir", nil, "no fixtures"},
		{"unknown field", map[string]string{"f.json": `{"channel":"direct",` + pair + `,"extra":1}`}, "unknown field"},
		{"bad channel", map[string]string{"f.json": `{"channel":"other",` + pair + `}`}, "channel"},
		{"empty op", map[string]string{"f.json": `{"channel":"direct","opType":"","opCommand":"B"}`}, "empty"},
		{"long op", map[string]string{"f.json": `{"channel":"direct","opType":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","opCommand":"B"}`}, "longer than 32 bytes"},
		{"tagged instruction", map[string]string{"f.json": `{"channel":"instruction","submissionTag":"custom",` + pair + `}`}, "for direct actions only"},
		{"system op", map[string]string{"f.json": `{"channel":"direct","opType":"F_XRP","opCommand":"PAY"}`}, "reserved for the system"},
		{"pending without posts", map[string]string{"f.json": `{"channel":"direct",` + pair + `,"result":{"status":2}}`}, "status-2 result needs posts"},
		{"posts without pending", map[string]string{"f.json": `{"channel":"direct",` + pair + `,"result":{"status":1},"posts":[{"status":1}]}`}, "status-2 result needs posts"},
		{"pending again", map[string]string{"f.json": `{"channel":"direct",` + pair + `,"result":{"status":1},"again":{"status":2}}`}, "again can't be status 2"},
		{"both channels", map[string]string{
			"a.json": `{"channel":"direct",` + pair + `}`,
			"b.json": `{"channel":"instruction",` + pair + `}`,
		}, "also on the direct channel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, body := range tt.files {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
			}
			_, err := Load(dir)
			assert.ErrorContains(t, err, tt.reason)
		})
	}
}

func TestLoadMissingDir(t *testing.T) {
	t.Parallel()
	_, err := Load(filepath.Join(t.TempDir(), "missing"))
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
