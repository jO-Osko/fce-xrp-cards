package golden

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/flare-foundation/fce-go-scaffold/conformance"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func fixture(name string, channel types.ActionType) conformance.Fixture {
	return conformance.Fixture{
		Name: name, Description: "echoes", Channel: channel, OPType: "TEST", OPCommand: "ECHO",
		Message: []byte("hi"), Result: conformance.Expected{Status: 1, Log: "ok", Data: []byte("hi")},
	}
}

func fixtures() []conformance.Fixture {
	return []conformance.Fixture{fixture("a", types.Instruction), fixture("b", types.Instruction)}
}

// written is a directory holding fixtures, as -update leaves it.
func written(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "conformance")
	require.NoError(t, Check(dir, fixtures(), true))
	return dir
}

func TestCheck(t *testing.T) {
	t.Parallel()
	dir := written(t)
	require.NoError(t, Check(dir, fixtures(), false))

	got, err := conformance.Load(dir)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestCheckFindsDifferences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(t *testing.T, dir string)
		want   string
	}{
		{"stale", func(t *testing.T, dir string) {
			t.Helper()
			f := fixture("a", types.Instruction)
			f.Description = "older"
			b, err := f.Encode()
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "a.json"), b, 0o644))
		}, "a.json: stale"},
		{"missing", func(t *testing.T, dir string) {
			t.Helper()
			require.NoError(t, os.Remove(filepath.Join(dir, "b.json")))
		}, "b.json: missing"},
		{"extra", func(t *testing.T, dir string) {
			t.Helper()
			b, err := fixture("c", types.Instruction).Encode()
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "c.json"), b, 0o644))
		}, "c.json: no such fixture"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := written(t)
			tt.change(t, dir)

			err := Check(dir, fixtures(), false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Contains(t, err.Error(), "run with -update")

			require.NoError(t, Check(dir, fixtures(), true))
			require.NoError(t, Check(dir, fixtures(), false))
		})
	}
}

func TestCheckUpdateKeepsOtherFiles(t *testing.T) {
	t.Parallel()
	dir := written(t)
	notes := filepath.Join(dir, "README.md")
	require.NoError(t, os.WriteFile(notes, []byte("notes"), 0o644))

	require.NoError(t, Check(dir, fixtures()[:1], true))
	assert.FileExists(t, notes)
	assert.NoFileExists(t, filepath.Join(dir, "b.json"))
}

func TestCheckRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		fixtures []conformance.Fixture
	}{
		{"two with one name", []conformance.Fixture{fixture("a", types.Instruction), fixture("a", types.Instruction)}},
		{"an invalid fixture", []conformance.Fixture{fixture("a", "other")}},
		{"an op pair on both channels", []conformance.Fixture{fixture("a", types.Instruction), fixture("b", types.Direct)}},
		{"none", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, update := range []bool{true, false} {
				dir := filepath.Join(t.TempDir(), "conformance")
				require.Error(t, Check(dir, tt.fixtures, update), "update %v", update)
			}
		})
	}
}
