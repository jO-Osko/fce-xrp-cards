package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		args   []string
		want   config
		reason string
	}{
		{
			name: "full",
			args: []string{"--node", "/app/tee-node", "--uid", "65532", "--gid", "65533", "--env", "A,B_1", "--env", "_C", "--grace", "3s", "--", "/app/extension", "-x"},
			want: config{node: "/app/tee-node", command: []string{"/app/extension", "-x"}, uid: 65532, gid: 65533, env: []string{"A", "B_1", "_C"}, grace: 3 * time.Second},
		},
		{
			name: "defaults",
			args: []string{"--node", "/app/tee-node", "--uid", "65532", "--", "/app/extension"},
			want: config{node: "/app/tee-node", command: []string{"/app/extension"}, uid: 65532, gid: 65532, grace: defaultGrace},
		},
		{name: "no node", args: []string{"--uid", "1", "--", "e"}, reason: "--node is required"},
		{name: "no command", args: []string{"--node", "n", "--uid", "1"}, reason: "command is required"},
		{name: "no uid", args: []string{"--node", "n", "--", "e"}, reason: "--uid is required"},
		{name: "root uid", args: []string{"--node", "n", "--uid", "0", "--", "e"}, reason: "must not be root"},
		{name: "root gid", args: []string{"--node", "n", "--uid", "1", "--gid", "0", "--", "e"}, reason: "must not be root"},
		{name: "uid too large", args: []string{"--node", "n", "--uid", "4294967296", "--", "e"}, reason: "not a decimal id"},
		{name: "negative uid", args: []string{"--node", "n", "--uid", "-1", "--", "e"}, reason: "not a decimal id"},
		{name: "uid -1 as unsigned", args: []string{"--node", "n", "--uid", "4294967295", "--", "e"}, reason: "not an id"},
		{name: "gid -1 as unsigned", args: []string{"--node", "n", "--uid", "1", "--gid", "4294967295", "--", "e"}, reason: "not an id"},
		{name: "empty env name", args: []string{"--node", "n", "--uid", "1", "--env", "A,", "--", "e"}, reason: `"" is not a variable name`},
		{name: "env name with a space", args: []string{"--node", "n", "--uid", "1", "--env", "A, B", "--", "e"}, reason: `" B" is not a variable name`},
		{name: "env name with =", args: []string{"--node", "n", "--uid", "1", "--env", "A=1", "--", "e"}, reason: "not a variable name"},
		{name: "env name starting with a digit", args: []string{"--node", "n", "--uid", "1", "--env", "1A", "--", "e"}, reason: "not a variable name"},
		{name: "zero grace", args: []string{"--node", "n", "--uid", "1", "--grace", "0s", "--", "e"}, reason: "--grace must be positive"},
		{name: "unknown flag", args: []string{"--node", "n", "--uid", "1", "--other", "--", "e"}, reason: "flag provided but not defined"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parse(tt.args)
			if tt.reason != "" {
				if err == nil || !strings.Contains(err.Error(), tt.reason) {
					t.Fatalf("error %v, want one containing %q", err, tt.reason)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExtensionEnv(t *testing.T) {
	t.Parallel()
	vars := map[string]string{
		"EXTENSION_PORT": "1",
		"SIGN_PORT":      "2",
		"LOG_LEVEL":      "",
		"EXTENSION_ID":   "0x01",
		"MY_VAR":         "x",
		"OTHER":          "y",
		"PATH":           "/bin",
	}
	lookup := func(k string) (string, bool) {
		v, ok := vars[k]
		return v, ok
	}

	// CHAIN_ID and ABSENT are unset: left out, not passed empty; LOG_LEVEL is set but empty: passed
	got := extensionEnv(lookup, []string{"MY_VAR", "ABSENT", "SIGN_PORT", "MY_VAR"})
	want := []string{"EXTENSION_PORT=1", "SIGN_PORT=2", "LOG_LEVEL=", "EXTENSION_ID=0x01", "MY_VAR=x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRunUsage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args []string
		code int
	}{
		{[]string{"-h"}, exitOK},
		{nil, exitUsage},
		{[]string{"--node", "n", "--uid", "0", "--", "e"}, exitUsage},
	}
	for _, tt := range tests {
		if got := run(tt.args); got != tt.code {
			t.Errorf("run(%q) = %d, want %d", tt.args, got, tt.code)
		}
	}
}
