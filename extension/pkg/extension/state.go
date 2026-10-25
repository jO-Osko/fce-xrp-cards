package extension

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// StateFunc returns the extension's state for GET /state: a version naming
// its encoding, and the state bytes.
type StateFunc func(ctx context.Context) (version common.Hash, state []byte, err error)

// RegisterStateFunc sets f to serve GET /state.
// Without it, the server answers a zero version and empty state.
// It panics if f is nil or a state function was already registered.
// Register before serving.
func (s *Server) RegisterStateFunc(f StateFunc) {
	if f == nil {
		panic("nil state function")
	}
	if s.state != nil {
		panic("state function already registered")
	}
	s.state = f
}

type stateOutcome struct {
	version common.Hash
	state   []byte
	err     error
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	var ts types.TeeState
	if s.state != nil {
		o := s.runState(r.Context())
		if o.err != nil {
			// tee-node records any non-200 as state unavailable
			http.Error(w, "state unavailable", http.StatusInternalServerError)
			return
		}
		ts.StateVersion, ts.State = o.version, o.state
	}

	body, err := json.Marshal(ts)
	if err != nil {
		http.Error(w, "state unavailable", http.StatusInternalServerError)
		return
	}
	// tee-node cuts the response here, so a larger one would arrive truncated
	if len(body) > MaxStateResponseSize {
		s.log.Errorf("state response of %d bytes exceeds %d", len(body), MaxStateResponseSize)
		http.Error(w, "state unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, body)
}

func (s *Server) runState(ctx context.Context) stateOutcome {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	ch := make(chan stateOutcome, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				// the panic value isn't logged: it may carry secrets
				s.log.Errorf("recovered a panic in the state function")
				ch <- stateOutcome{err: errPanic}
			}
		}()
		v, st, err := s.state(ctx)
		ch <- stateOutcome{version: v, state: st, err: err}
	}()

	select {
	case o := <-ch:
		return o
	case <-ctx.Done():
		return stateOutcome{err: errDeadline}
	}
}
