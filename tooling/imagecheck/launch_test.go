package imagecheck

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLaunch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want launch
	}{
		{
			name: "template",
			args: []string{"--node", "/n", "--uid", "65532", "--", "/e"},
			want: launch{node: "/n", uid: 65532, gid: 65532, grace: 10 * time.Second, command: []string{"/e"}},
		},
		{
			name: "every form",
			args: []string{"-node=/n", "--uid=7", "-gid", "8", "--env", "A,B", "--env=C", "--grace", "2s", "/e", "-x"},
			want: launch{node: "/n", uid: 7, gid: 8, env: []string{"A", "B", "C"}, grace: 2 * time.Second, command: []string{"/e", "-x"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseLaunch(tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseLaunchRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"--node", "/n", "--uid", "1", "--user", "x", "--", "/e"}},
		{"no node", []string{"--uid", "1", "--", "/e"}},
		{"no command", []string{"--node", "/n", "--uid", "1"}},
		{"no uid", []string{"--node", "/n", "--", "/e"}},
		{"root uid", []string{"--node", "/n", "--uid", "0", "--", "/e"}},
		{"root gid", []string{"--node", "/n", "--uid", "1", "--gid", "0", "--", "/e"}},
		{"uid -1", []string{"--node", "/n", "--uid", "4294967295", "--", "/e"}},
		{"bad name", []string{"--node", "/n", "--uid", "1", "--env", "A-B", "--", "/e"}},
		{"empty name", []string{"--node", "/n", "--uid", "1", "--env", "A,", "--", "/e"}},
		{"no grace", []string{"--node", "/n", "--uid", "1", "--grace", "0s", "--", "/e"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseLaunch(tt.args)
			require.Error(t, err)
		})
	}
}
