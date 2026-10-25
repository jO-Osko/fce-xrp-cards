package conformance

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/fce-tooling/internal/fakenode"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// Mirrors tee-node v0.0.29; re-check on every tee-node bump.
const (
	// actionTimeout is how long tee-node waits for an answer, body included (ProxyTimeout).
	actionTimeout = 2 * time.Second
	// resultVersion is tee-node's result encoding version (EncodingVersion).
	resultVersion = "1.0.0"
)

const (
	maxResponseSize = 32 << 20
	pollInterval    = 20 * time.Millisecond
	// settleWindow is how long no new post must arrive before posts are compared.
	settleWindow = 200 * time.Millisecond
)

// resultFields are types.ActionResult's JSON fields; every answer carries all of them.
var resultFields = []string{"id", "submissionTag", "status", "log", "opType", "opCommand", "additionalResultStatus", "version", "data"}

// Result is one check's outcome.
type Result struct {
	Name string
	// Err is set when the extension broke a rule tee-node or tee-proxy depends on, or one of its own fixtures.
	Err error
	// Advice is set when the extension broke a convention.
	Advice error
}

// verdict collects what a check found: broken required rules in errs, broken conventions in advice.
type verdict struct {
	errs, advice []error
}

func (v *verdict) fail(err error) {
	if err != nil {
		v.errs = append(v.errs, err)
	}
}

func (v *verdict) advise(err error) {
	if err != nil {
		v.advice = append(v.advice, err)
	}
}

func (v *verdict) merge(o verdict) {
	v.errs = append(v.errs, o.errs...)
	v.advice = append(v.advice, o.advice...)
}

// conventions is v for a check that covers only conventions: everything it found is advice.
func (v verdict) conventions() verdict {
	return verdict{advice: append(v.errs, v.advice...)}
}

func (v verdict) result(name string) Result {
	return Result{Name: name, Err: joinErrs(v.errs), Advice: joinErrs(v.advice)}
}

// sent is an action the runner sent, with the posts it expects for it.
type sent struct {
	id    common.Hash
	tag   types.SubmissionTag
	op    types.OpID
	posts []Expected
	// fixture marks a fixture's own action, whose posts are required.
	fixture bool
}

// checker runs the checks against an extension listening at url.
type checker struct {
	url    string
	node   *fakenode.Node
	client *http.Client
	sent   []sent
}

func newChecker(url string, node *fakenode.Node) *checker {
	return &checker{url: url, node: node, client: &http.Client{Timeout: actionTimeout}}
}

// run makes every check: the fixtures', then the generic ones, then the posts.
func (c *checker) run(ctx context.Context, fixtures []Fixture, postWait time.Duration) []Result {
	results := make([]Result, 0, 3*len(fixtures)+10)
	check := func(name string, f func(name string) verdict) {
		results = append(results, f(name).result(name))
	}
	// a check of conventions only
	advised := func(err error) verdict {
		var v verdict
		v.advise(err)
		return v
	}

	check("GET /state", func(string) verdict { return c.state(ctx) })
	for _, f := range fixtures {
		check("fixture "+f.Name, func(name string) verdict { return c.fixture(ctx, name, f, true, false) })
		if f.Again != nil {
			check("fixture "+f.Name+" sent again", func(name string) verdict { return c.fixture(ctx, name, f, false, false) })
		}
		check("fixture "+f.Name+" with unknown fields", func(name string) verdict {
			return c.fixture(ctx, name, f, false, true).conventions()
		})
		check("fixture "+f.Name+" on the other channel", func(name string) verdict {
			return c.refused(ctx, name, other(f.Channel), f.OPType, f.OPCommand, f.Message).conventions()
		})
		if f.Channel == types.Direct {
			check("fixture "+f.Name+" tagged threshold", func(name string) verdict {
				return c.taggedThreshold(ctx, name, f).conventions()
			})
		}
	}
	for _, ch := range []types.ActionType{types.Instruction, types.Direct} {
		check("unknown op, "+string(ch), func(name string) verdict {
			return c.refused(ctx, name, ch, "CONFORMANCE", "UNKNOWN_OP", []byte("x")).conventions()
		})
		// tee-node refuses these first; the extension should too
		check("system op, "+string(ch), func(name string) verdict {
			return c.refused(ctx, name, ch, "F_XRP", "PAY", []byte("x")).conventions()
		})
		check("undecodable message, "+string(ch), func(name string) verdict { return c.undecodable(ctx, name, ch).conventions() })
	}
	check("a body that isn't an action", func(string) verdict { return advised(c.notAnAction(ctx)) })
	check("routing", func(string) verdict { return advised(c.routing(ctx)) })
	check("posts", func(string) verdict { return c.posts(ctx, postWait) })
	return results
}

func (c *checker) state(ctx context.Context) verdict {
	var v verdict
	r, err := c.do(ctx, http.MethodGet, "/state", nil)
	if err != nil {
		v.fail(err)
		return v
	}
	v.advise(r.notJSON)
	if r.code != http.StatusOK {
		v.fail(fmt.Errorf("answered %d, want 200", r.code))
		return v
	}
	if err := hasFields(r.body, "stateVersion", "state"); err != nil {
		v.fail(err)
		return v
	}
	var ts types.TeeState
	if err := json.Unmarshal(r.body, &ts); err != nil {
		v.fail(fmt.Errorf("not a TeeState: %w", err))
	}
	return v
}

// fixture sends f's message, with unknown fields at every level when unknown. The first send
// expects f's result and posts; a later one expects f.Again, or the result without it.
func (c *checker) fixture(ctx context.Context, name string, f Fixture, first, unknown bool) verdict {
	id, opID, tag := actionID(name), f.opID(), f.tag()
	body, err := action(id, f.Channel, tag, opID, f.Message, unknown)
	if err != nil {
		return verdict{errs: []error{err}}
	}
	c.sent = append(c.sent, sent{id: id, tag: tag, op: opID, posts: f.Posts, fixture: first})

	got, v := c.send(ctx, body)
	if len(v.errs) == 0 {
		want := f.Result
		if !first && f.Again != nil {
			want = *f.Again
		}
		v.merge(expect{id: id, tag: tag, op: &opID, status: want.Status, log: want.Log, data: want.Data}.check(got))
	}
	return v
}

// refused sends an action no handler should take: status 0, an error log, no data.
func (c *checker) refused(ctx context.Context, name string, ch types.ActionType, opType op.Type, opCommand op.Command, msg []byte) verdict {
	id, tag := actionID(name), tagOf(ch)
	body, err := action(id, ch, tag, types.OpID{OPType: opType.Hash(), OPCommand: opCommand.Hash()}, msg, false)
	if err != nil {
		return verdict{errs: []error{err}}
	}
	return c.sendRefused(ctx, id, tag, body)
}

// taggedThreshold sends f's message as a direct action tagged threshold: tee-proxy
// picks a direct action's id, so its result could pass as an instruction's.
func (c *checker) taggedThreshold(ctx context.Context, name string, f Fixture) verdict {
	id := actionID(name)
	body, err := action(id, types.Direct, types.Threshold, f.opID(), f.Message, false)
	if err != nil {
		return verdict{errs: []error{err}}
	}
	return c.sendRefused(ctx, id, types.Threshold, body)
}

func (c *checker) undecodable(ctx context.Context, name string, ch types.ActionType) verdict {
	id := actionID(name)
	tag := tagOf(ch)
	body, err := json.Marshal(types.Action{Data: types.ActionData{ID: id, Type: ch, SubmissionTag: tag, Message: []byte("not json")}})
	if err != nil {
		return verdict{errs: []error{fmt.Errorf("encoding the action: %w", err)}}
	}
	return c.sendRefused(ctx, id, tag, body)
}

// sendRefused sends an action the extension must refuse.
func (c *checker) sendRefused(ctx context.Context, id common.Hash, tag types.SubmissionTag, body []byte) verdict {
	c.sent = append(c.sent, sent{id: id, tag: tag})
	got, v := c.send(ctx, body)
	if len(v.errs) == 0 {
		v.merge(expect{id: id, tag: tag, anyError: true}.check(got))
	}
	return v
}

// notAnAction: the extension can't echo an id, so it answers 400 and tee-node builds the failure itself.
func (c *checker) notAnAction(ctx context.Context) error {
	r, err := c.do(ctx, http.MethodPost, "/action", []byte("{"))
	if err != nil {
		return err
	}
	if r.code != http.StatusBadRequest {
		return fmt.Errorf("answered %d, want 400", r.code)
	}
	return nil
}

func (c *checker) routing(ctx context.Context) error {
	var errs []error
	for _, r := range []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/action", http.StatusMethodNotAllowed},
		{http.MethodPost, "/state", http.StatusMethodNotAllowed},
		{http.MethodGet, "/conformance-unknown-path", http.StatusNotFound},
	} {
		resp, err := c.do(ctx, r.method, r.path, nil)
		switch {
		case err != nil:
			errs = append(errs, err)
		case resp.code != r.code:
			errs = append(errs, fmt.Errorf("%s %s answered %d, want %d", r.method, r.path, resp.code, r.code))
		}
	}
	return joinErrs(errs)
}

// posts waits, up to wait, for the expected posts and then for no new post
// within settleWindow; it compares them in status order and refuses any post
// no action expects. Only a fixture's own posts are required.
func (c *checker) posts(ctx context.Context, wait time.Duration) verdict {
	deadline := time.Now().Add(wait)
	count, quietSince := len(c.node.Results()), time.Now()
	for time.Now().Before(deadline) && (!c.postsArrived() || time.Since(quietSince) < settleWindow) {
		select {
		case <-ctx.Done():
			return verdict{errs: []error{ctx.Err()}}
		case <-time.After(pollInterval):
		}
		if n := len(c.node.Results()); n != count {
			count, quietSince = n, time.Now()
		}
	}

	byID := map[common.Hash][]types.ActionResult{}
	for _, r := range c.node.Results() {
		byID[r.ID] = append(byID[r.ID], r)
	}

	var v verdict
	for _, s := range c.sent {
		got := byID[s.id]
		delete(byID, s.id)
		report := v.advise
		if s.fixture {
			report = v.fail
		}
		if len(got) != len(s.posts) {
			report(fmt.Errorf("action %s: %d posts, want %d", s.id.Hex(), len(got), len(s.posts)))
			continue
		}
		// posts may land out of order; tee-proxy keeps the highest
		slices.SortStableFunc(got, func(a, b types.ActionResult) int { return cmp.Compare(rank(a.Status), rank(b.Status)) })
		want := slices.Clone(s.posts)
		slices.SortStableFunc(want, func(a, b Expected) int { return cmp.Compare(rank(a.Status), rank(b.Status)) })
		for i, p := range want {
			pv := expect{id: s.id, tag: s.tag, op: &s.op, status: p.Status, log: p.Log, data: p.Data}.check(got[i])
			if err := joinErrs(pv.errs); err != nil {
				report(fmt.Errorf("action %s, post %d: %w", s.id.Hex(), i+1, err))
			}
			if err := joinErrs(pv.advice); err != nil {
				v.advise(fmt.Errorf("action %s, post %d: %w", s.id.Hex(), i+1, err))
			}
		}
	}
	for id, got := range byID {
		v.advise(fmt.Errorf("%d posts for %s, an action the runner never sent", len(got), id.Hex()))
	}
	return v
}

// rank orders statuses as tee-proxy does: a final (0 or 1) outranks any other.
func rank(status uint8) int {
	if status <= 1 {
		return math.MaxUint8 + 1
	}
	return int(status)
}

func (c *checker) postsArrived() bool {
	n := map[common.Hash]int{}
	for _, r := range c.node.Results() {
		n[r.ID]++
	}
	for _, s := range c.sent {
		if n[s.id] < len(s.posts) {
			return false
		}
	}
	return true
}

// send posts an action and decodes the answer: a 200 ActionResult is required; JSON with every field, a convention.
func (c *checker) send(ctx context.Context, body []byte) (types.ActionResult, verdict) {
	var (
		res types.ActionResult
		v   verdict
	)
	r, err := c.do(ctx, http.MethodPost, "/action", body)
	if err != nil {
		v.fail(err)
		return res, v
	}
	v.advise(r.notJSON)
	if r.code != http.StatusOK {
		v.fail(fmt.Errorf("answered %d, want 200", r.code))
		return res, v
	}
	if err := json.Unmarshal(r.body, &res); err != nil {
		v.fail(fmt.Errorf("not an ActionResult: %w", err))
		return res, v
	}
	v.advise(hasFields(r.body, resultFields...))
	return res, v
}

// response is an answer to one request.
type response struct {
	code int
	body []byte
	// notJSON is set when a 200 answer isn't application/json, a convention.
	notJSON error
}

func (c *checker) do(ctx context.Context, method, path string, body []byte) (response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, bytes.NewReader(body))
	if err != nil {
		return response{}, fmt.Errorf("building the request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // the body is only read; a close error changes nothing

	out, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return response{}, fmt.Errorf("reading the answer to %s %s: %w", method, path, err)
	}
	r := response{code: resp.StatusCode, body: out}
	if resp.StatusCode == http.StatusOK {
		mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			r.notJSON = fmt.Errorf("%s %s: Content-Type %q, want application/json", method, path, resp.Header.Get("Content-Type"))
		}
	}
	return r, nil
}

// expect is what a check expects of a result. op nil skips the op pair;
// anyError accepts status 0 with any "error: " log and no data.
type expect struct {
	id       common.Hash
	tag      types.SubmissionTag
	op       *types.OpID
	status   uint8
	log      string
	data     []byte
	anyError bool
}

// check compares got with e: the echoed id and tag, and a fixture's status, log and data, are required; the rest are conventions.
func (e expect) check(got types.ActionResult) verdict {
	var v verdict
	mismatch := func(field string, got, want any) error { return fmt.Errorf("%s %v, want %v", field, got, want) }
	required := func(field string, got, want any) { v.fail(mismatch(field, got, want)) }
	convention := func(field string, got, want any) { v.advise(mismatch(field, got, want)) }
	byFixture := required
	if e.anyError {
		byFixture = convention
	}

	if got.ID != e.id {
		required("id", got.ID.Hex(), e.id.Hex())
	}
	if got.SubmissionTag != e.tag {
		required("submissionTag", got.SubmissionTag, e.tag)
	}
	if e.op != nil && (got.OPType != e.op.OPType || got.OPCommand != e.op.OPCommand) {
		convention("op pair", fmt.Sprintf("%q / %q", op.HashToOPType(got.OPType), op.HashToOPCommand(got.OPCommand)),
			fmt.Sprintf("%q / %q", op.HashToOPType(e.op.OPType), op.HashToOPCommand(e.op.OPCommand)))
	}
	if e.anyError {
		if got.Status != 0 {
			byFixture("status", got.Status, 0)
		}
		if !strings.HasPrefix(got.Log, "error: ") {
			byFixture("log", fmt.Sprintf("%q", got.Log), `"error: …"`)
		}
		if len(got.Data) != 0 {
			byFixture("data", got.Data, "0x")
		}
	} else {
		if got.Status != e.status {
			byFixture("status", got.Status, e.status)
		}
		if got.Log != e.log {
			byFixture("log", fmt.Sprintf("%q", got.Log), fmt.Sprintf("%q", e.log))
		}
		if !bytes.Equal(got.Data, e.data) {
			byFixture("data", got.Data, hexutil.Bytes(e.data))
		}
	}
	if len(got.AdditionalResultStatus) != 0 {
		convention("additionalResultStatus", got.AdditionalResultStatus, "0x")
	}
	if got.Version != resultVersion {
		convention("version", fmt.Sprintf("%q", got.Version), fmt.Sprintf("%q", resultVersion))
	}
	return v
}

// action builds the body tee-node would post, optionally with a field from a
// future tee-node at each level.
func action(id common.Hash, ch types.ActionType, tag types.SubmissionTag, opID types.OpID, msg []byte, unknown bool) ([]byte, error) {
	var inner any = types.DirectInstruction{OPType: opID.OPType, OPCommand: opID.OPCommand, Message: msg}
	if ch == types.Instruction {
		inner = instruction.DataFixed{InstructionID: id, TeeID: fakenode.Address(), OPType: opID.OPType, OPCommand: opID.OPCommand, OriginalMessage: msg}
	}

	message, err := encode(inner, unknown)
	if err != nil {
		return nil, err
	}
	data, err := encode(types.ActionData{ID: id, Type: ch, SubmissionTag: tag, Message: message}, unknown)
	if err != nil {
		return nil, err
	}
	return encode(map[string]json.RawMessage{"data": data}, unknown)
}

func encode(v any, unknown bool) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || !unknown {
		return b, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m["conformanceFutureField"] = json.RawMessage(`"x"`)
	return json.Marshal(m)
}

func hasFields(body []byte, fields ...string) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("not a JSON object: %w", err)
	}
	var missing []string
	for _, f := range fields {
		if _, ok := m[f]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// actionID is fixed per check, so its posts can be matched and runs repeat exactly.
func actionID(check string) common.Hash {
	return crypto.Keccak256Hash([]byte("conformance: " + check))
}

// tagOf is the tag each channel arrives with by default: threshold, or submit from tee-proxy's /direct.
func tagOf(ch types.ActionType) types.SubmissionTag {
	if ch == types.Instruction {
		return types.Threshold
	}
	return types.Submit
}

func other(ch types.ActionType) types.ActionType {
	if ch == types.Instruction {
		return types.Direct
	}
	return types.Instruction
}

func joinErrs(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return errors.New(strings.Join(msgs, "; "))
}
