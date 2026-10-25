package extension

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/require"
)

// The zero state is the only one the registry accepts without a state verifier.
func TestZeroStateGolden(t *testing.T) {
	t.Parallel()
	rec := do(t, newTestServer(t), http.MethodGet, "/state", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{
		"systemState": "0x",
		"systemStateVersion": "0x0000000000000000000000000000000000000000000000000000000000000000",
		"state": "0x",
		"stateVersion": "0x0000000000000000000000000000000000000000000000000000000000000000"
	}`, rec.Body.String())
}

func TestStateFunc(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	version := common.HexToHash("0x02")
	s.RegisterStateFunc(func(context.Context) (common.Hash, []byte, error) {
		return version, []byte("state"), nil
	})

	rec := do(t, s, http.MethodGet, "/state", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var ts types.TeeState
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ts))
	require.Equal(t, version, ts.StateVersion)
	require.Equal(t, []byte("state"), []byte(ts.State))
}

func TestStateUnavailable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		f    StateFunc
	}{
		{"error", func(context.Context) (common.Hash, []byte, error) {
			return common.Hash{}, nil, errors.New("secret detail")
		}},
		{"panic", func(context.Context) (common.Hash, []byte, error) {
			panic("secret detail")
		}},
		{"deadline", func(ctx context.Context) (common.Hash, []byte, error) {
			<-ctx.Done()
			return common.Hash{}, nil, nil
		}},
		// hex doubles it past the response cap
		{"too large", func(context.Context) (common.Hash, []byte, error) {
			return common.Hash{1}, make([]byte, MaxStateResponseSize/2), nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newTestServer(t)
			s.timeout = 20 * time.Millisecond
			s.RegisterStateFunc(tt.f)

			rec := do(t, s, http.MethodGet, "/state", nil)
			require.Equal(t, http.StatusInternalServerError, rec.Code)
			require.Equal(t, "state unavailable\n", rec.Body.String())
		})
	}
}

func TestRegisterStateFuncPanics(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	require.Panics(t, func() { s.RegisterStateFunc(nil) })

	f := func(context.Context) (common.Hash, []byte, error) { return common.Hash{}, nil, nil }
	s.RegisterStateFunc(f)
	require.Panics(t, func() { s.RegisterStateFunc(f) })
}
