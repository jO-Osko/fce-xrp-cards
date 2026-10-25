package extension

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getenvMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		vars map[string]string
		want Env
	}{
		{"unset", nil, Env{8889, 8888, "DEBUG"}},
		{"empty", map[string]string{"EXTENSION_PORT": "", "SIGN_PORT": "", "LOG_LEVEL": ""}, Env{8889, 8888, "DEBUG"}},
		{"set", map[string]string{"EXTENSION_PORT": "9001", "SIGN_PORT": "9002", "LOG_LEVEL": "INFO"}, Env{9001, 9002, "INFO"}},
		{"swapped defaults", map[string]string{"EXTENSION_PORT": "8888", "SIGN_PORT": "8889"}, Env{8888, 8889, "DEBUG"}},
		{"one set", map[string]string{"SIGN_PORT": "9002"}, Env{8889, 9002, "DEBUG"}},
		// Atoi accepts a sign and leading zeros
		{"plus sign", map[string]string{"EXTENSION_PORT": "+9001"}, Env{9001, 8888, "DEBUG"}},
		{"leading zero", map[string]string{"EXTENSION_PORT": "09001"}, Env{9001, 8888, "DEBUG"}},
		{"range ends", map[string]string{"EXTENSION_PORT": "1", "SIGN_PORT": "65535"}, Env{1, 65535, "DEBUG"}},
		// passed through; go-flare-common falls back to DEBUG
		{"unknown log level", map[string]string{"LOG_LEVEL": "loud"}, Env{8889, 8888, "loud"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env, err := parseEnv(getenvMap(tt.vars))
			require.NoError(t, err)
			assert.Equal(t, tt.want, env)
		})
	}
}

func TestParseEnvErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		vars map[string]string
		errs []string
	}{
		{"not a number", map[string]string{"EXTENSION_PORT": "abc"}, []string{`EXTENSION_PORT="abc" is not a number`}},
		{"not trimmed", map[string]string{"SIGN_PORT": " 9002"}, []string{`SIGN_PORT=" 9002" is not a number`}},
		{"zero", map[string]string{"EXTENSION_PORT": "0"}, []string{"EXTENSION_PORT=0 is not a valid port"}},
		{"negative", map[string]string{"EXTENSION_PORT": "-1"}, []string{"EXTENSION_PORT=-1 is not a valid port"}},
		{"too large", map[string]string{"SIGN_PORT": "65536"}, []string{"SIGN_PORT=65536 is not a valid port"}},
		{"config port", map[string]string{"SIGN_PORT": "5500"}, []string{"SIGN_PORT=5500 collides"}},
		{"equal", map[string]string{"EXTENSION_PORT": "9000", "SIGN_PORT": "9000"}, []string{"EXTENSION_PORT and SIGN_PORT are both 9000"}},
		{"equal to the other default", map[string]string{"EXTENSION_PORT": "8888"}, []string{"EXTENSION_PORT and SIGN_PORT are both 8888"}},
		{"both invalid", map[string]string{"EXTENSION_PORT": "x", "SIGN_PORT": "0"}, []string{"EXTENSION_PORT", "SIGN_PORT=0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseEnv(getenvMap(tt.vars))
			for _, e := range tt.errs {
				require.ErrorContains(t, err, e)
			}
		})
	}
}
