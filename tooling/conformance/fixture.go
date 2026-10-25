// Package conformance checks an extension from the outside, whatever its
// language: it starts the extension against a fake sign port, sends it actions
// as tee-node does, and compares the answers with the extension's fixtures and
// with the rules every extension follows.
package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
)

const (
	// systemPrefix marks op types reserved for tee-node.
	systemPrefix  = "F_"
	statusPending = 2
)

// Fixture is one golden case for one of the extension's own ops, stored as
// <Name>.json. The runner sends the fixtures in name order to one extension
// process, so a fixture may rely on the state the ones before it left.
type Fixture struct {
	Name        string           `json:"-"`
	Description string           `json:"description"`
	Channel     types.ActionType `json:"channel"`
	// SubmissionTag is the tag a direct action arrives with; empty means
	// submit, as tee-proxy's /direct sets. Instructions always get threshold.
	SubmissionTag types.SubmissionTag `json:"submissionTag,omitempty"`
	OPType        op.Type             `json:"opType"`
	OPCommand     op.Command          `json:"opCommand"`
	// Message is the payload: an instruction's originalMessage, or a direct action's message.
	Message hexutil.Bytes `json:"message"`
	Result  Expected      `json:"result"`
	// Again is the result every later send of the same message gets, for an op
	// that changes state, such as one refusing a replay; nil means Result.
	Again *Expected `json:"again,omitempty"`
	// Posts are the results a long-running op posts to /result; they are
	// compared in status order, partials then the final.
	Posts []Expected `json:"posts,omitempty"`
}

// Expected is the part of a result the extension decides; the runner checks
// the rest (id, submission tag, op pair, version).
type Expected struct {
	Status uint8         `json:"status"`
	Log    string        `json:"log"`
	Data   hexutil.Bytes `json:"data"`
}

// Encode returns f as its file holds it.
func (f Fixture) Encode() ([]byte, error) {
	if err := f.validate(); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding fixture %s: %w", f.Name, err)
	}
	return append(b, '\n'), nil
}

// Load reads every *.json file in dir as a fixture, sorted by name. A missing
// or empty dir is an error, so a misplaced one can't pass unnoticed.
func Load(dir string) ([]Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing fixtures: %w", err)
	}

	fixtures := make([]Fixture, 0, len(entries))
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading fixture: %w", err)
		}
		f := Fixture{Name: name}
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			return nil, fmt.Errorf("decoding fixture %s: %w", f.Name, err)
		}
		if err := f.validate(); err != nil {
			return nil, err
		}
		fixtures = append(fixtures, f)
	}
	if len(fixtures) == 0 {
		return nil, fmt.Errorf("no fixtures in %s", dir)
	}
	if err := oneChannel(fixtures); err != nil {
		return nil, err
	}
	return fixtures, nil
}

func (f Fixture) validate() error {
	var errs []error
	if f.Name == "" {
		errs = append(errs, errors.New("no name"))
	}
	if f.Channel != types.Instruction && f.Channel != types.Direct {
		errs = append(errs, fmt.Errorf("channel %q is neither %q nor %q", f.Channel, types.Instruction, types.Direct))
	}
	for _, name := range []string{string(f.OPType), string(f.OPCommand)} {
		if name == "" || len(name) > 32 {
			errs = append(errs, fmt.Errorf("op name %q is empty or longer than 32 bytes", name))
		}
	}
	if f.SubmissionTag != "" && f.Channel != types.Direct {
		errs = append(errs, errors.New("a submission tag is for direct actions only: tee-node sends instructions with threshold"))
	}
	if strings.HasPrefix(string(f.OPType), systemPrefix) {
		errs = append(errs, fmt.Errorf("op type %q is reserved for the system", f.OPType))
	}
	if (f.Result.Status == statusPending) != (len(f.Posts) > 0) {
		errs = append(errs, errors.New("a status-2 result needs posts, and only a status-2 result has them"))
	}
	if f.Again != nil && f.Again.Status == statusPending {
		errs = append(errs, errors.New("again can't be status 2: only the first send's posts are checked"))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("fixture %s: %w", f.Name, err)
	}
	return nil
}

// oneChannel refuses an op pair on both channels: each pair belongs to one.
func oneChannel(fixtures []Fixture) error {
	channels := map[types.OpID]types.ActionType{}
	for _, f := range fixtures {
		id := f.opID()
		if ch, ok := channels[id]; ok && ch != f.Channel {
			return fmt.Errorf("fixture %s: %s / %s is also on the %s channel", f.Name, f.OPType, f.OPCommand, ch)
		}
		channels[id] = f.Channel
	}
	return nil
}

func (f Fixture) tag() types.SubmissionTag {
	if f.SubmissionTag != "" {
		return f.SubmissionTag
	}
	return tagOf(f.Channel)
}

func (f Fixture) opID() types.OpID {
	return types.OpID{OPType: f.OPType.Hash(), OPCommand: f.OPCommand.Hash()}
}
