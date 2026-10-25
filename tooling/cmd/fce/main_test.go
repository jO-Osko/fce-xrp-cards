package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/flare-foundation/fce-tooling/conformance"
)

func TestCommandMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"./template"}, false},
		{[]string{"-env", "A=B", "./template", "./examples/sign"}, false},
		{[]string{"--", "./extension"}, true},
		{[]string{"-fixtures", "f", "-env", "A=B", "--", "./extension", "-x"}, true},
		{[]string{"-i-know-what-im-doing", "--", "./extension"}, true},
		{[]string{"-i-know-what-im-doing", "./template"}, false},
		{[]string{"./template", "--", "x"}, false},
	}
	for _, tt := range tests {
		var cfg conformance.Config
		fs, _, _ := conformanceFlags(&cfg)
		require.NoError(t, fs.Parse(tt.args))
		assert.Equal(t, tt.want, commandMode(tt.args, fs.Args()), tt.args)
	}
}

func TestIndent(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a\n      b\n      c", indent(errors.Join(errors.New("a"), errors.New("b\nc"))))
}

func TestPrintChecks(t *testing.T) {
	t.Parallel()
	checks := []check{
		{name: "fine"},
		{name: "required", err: assert.AnError},
		{name: "recommended", advice: assert.AnError},
		{name: "both", err: assert.AnError, advice: assert.AnError},
	}
	assert.Equal(t, tally{total: 4, failed: 3, recommended: 1}, printChecks(checks, false))
	assert.Equal(t, tally{total: 4, failed: 2, warned: 1}, printChecks(checks, true))
	assert.True(t, printChecks(checks[2:3], true).ok())
	assert.False(t, printChecks(checks[2:3], false).ok())
}
