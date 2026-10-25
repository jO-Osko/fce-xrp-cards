// Package payload decodes an extension's own payloads strictly: ABI only in
// its canonical encoding, JSON without unknown fields or anything after the value.
//
// Errors have fixed texts that never quote the payload. errors.Is matches
// ErrInvalid or ErrNotCanonical; the decoder's own error, which may quote it,
// stays reachable through errors.As.
package payload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/flare-foundation/go-flare-common/pkg/abicoder"
)

var (
	// ErrInvalid means the payload doesn't decode.
	ErrInvalid = errors.New("payload doesn't decode")
	// ErrNotCanonical means an ABI payload decodes, but isn't the canonical
	// encoding of what it decodes to.
	ErrNotCanonical = errors.New("payload not canonically encoded")
)

var errTrailing = errors.New("data after the JSON value")

// DecodeABI decodes b, encoded as args, into v: a pointer to a struct with a
// field per argument, matched as abi.Arguments.Copy matches them, or, for a
// single argument, to its value. go-ethereum's decoder alone accepts trailing
// bytes, dirty padding and offsets that point anywhere, even at one region
// many times over; DecodeABI decodes with go-flare-common's abicoder, which
// checks the layout before decoding. On error, v is unchanged.
func DecodeABI[T any](args abi.Arguments, b []byte, v *T) error {
	err := abicoder.DecodeArgumentsTo(args, b, v)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, abicoder.ErrTruncated):
		return &decodeError{kind: ErrInvalid, cause: err}
	case errors.Is(err, abicoder.ErrNonCanonical):
		return &decodeError{kind: ErrNotCanonical, cause: err}
	default:
		// args or v not matching is the caller's mistake, not the payload's
		return fmt.Errorf("decoding into %T: %w", v, err)
	}
}

// DecodeJSON decodes b into v, refusing unknown fields and anything after the
// JSON value but whitespace. A repeated name is accepted, the last value
// winning, as encoding/json does.
func DecodeJSON(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &decodeError{kind: ErrInvalid, cause: err}
	}
	if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return &decodeError{kind: ErrInvalid, cause: errTrailing}
	}
	return nil
}

// decodeError reads as kind alone: cause may quote the payload.
type decodeError struct {
	kind  error
	cause error
}

func (e *decodeError) Error() string { return e.kind.Error() }

func (e *decodeError) Unwrap() []error { return []error{e.kind, e.cause} }
