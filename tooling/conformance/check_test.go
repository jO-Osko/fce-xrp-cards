package conformance

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/fce-go-scaffold/pkg/extension"
	"github.com/flare-foundation/fce-tooling/internal/fakenode"
	"github.com/flare-foundation/go-flare-common/pkg/logger"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
	"github.com/stretchr/testify/assert"
)

func TestGoodExtensionPasses(t *testing.T) {
	t.Parallel()
	c := target(t, func(signPort int) http.Handler { return goodExtension(extension.WithSignPort(signPort)) })
	got := results(t, c, goodFixtures())

	assert.Len(t, got, 3*len(goodFixtures())+3+1+10, "three direct fixtures, one has again")
	for name, r := range got {
		assert.NoError(t, r.Err, name)
		assert.NoError(t, r.Advice, name)
	}
}

// Each broken extension fails the check that covers it, with each reason: reasons as required, advice as conventions.
func TestBrokenExtensionsFail(t *testing.T) {
	t.Parallel()
	echoOnly := goodFixtures()[:1]
	good := func(signPort int) http.Handler { return goodExtension(extension.WithSignPort(signPort)) }
	ok200 := func(int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{}`)) //nolint:errcheck,gosec // test fake
		})
	}
	takesDirect := func(int) http.Handler {
		s := goodExtension()
		s.RegisterDefaultDirectHandler(func(context.Context, *types.DirectInstruction) ([]byte, extension.Run, error) {
			return []byte("taken"), nil, nil
		})
		return s
	}
	breaks := func(f func(w http.ResponseWriter, c call) bool) func(int) http.Handler {
		return func(int) http.Handler { return breaking(goodExtension(), f) }
	}
	rewrites := func(edit func(*types.ActionResult)) func(int) http.Handler {
		return func(int) http.Handler { return rewriting(goodExtension(), edit) }
	}
	job := func(run extension.Run) func(int) http.Handler {
		return func(signPort int) http.Handler { return withJob(run, extension.WithSignPort(signPort)) }
	}

	tests := []struct {
		name     string
		ext      func(signPort int) http.Handler
		fixtures []Fixture
		check    string
		reasons  []string
		advice   []string
	}{
		{
			name: "wrong data",
			ext:  func(int) http.Handler { return goodExtension() },
			fixtures: []Fixture{{
				Name: "echo", Channel: types.Instruction, OPType: testType, OPCommand: echoCommand,
				Message: []byte("hi"), Result: Expected{Status: 1, Log: "ok", Data: []byte("other")},
			}},
			check: "fixture echo", reasons: []string{"data 0x6869, want 0x6f74686572"},
		},
		{
			name: "wrong echoes",
			ext: rewrites(func(r *types.ActionResult) {
				r.ID, r.SubmissionTag, r.OPType = common.HexToHash("0x01"), types.End, op.Type("OTHER").Hash()
				r.Version, r.AdditionalResultStatus = "0.0.0", []byte{1}
			}),
			fixtures: echoOnly, check: "fixture echo",
			reasons: []string{
				"id 0x0000000000000000000000000000000000000000000000000000000000000001, want",
				"submissionTag end, want threshold",
			},
			advice: []string{
				`op pair "OTHER" / "ECHO", want "TEST" / "ECHO"`,
				`version "0.0.0", want "1.0.0"`,
				"additionalResultStatus 0x01, want 0x",
			},
		},
		{
			name: "missing field",
			ext: func(int) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(`{"status":1}`)) //nolint:errcheck,gosec // test fake
				})
			},
			fixtures: echoOnly, check: "fixture echo",
			reasons: []string{"id 0x0000000000000000000000000000000000000000000000000000000000000000, want"},
			advice:  []string{"missing fields: id, submissionTag"},
		},
		{
			name: "slow answer",
			ext: breaks(func(_ http.ResponseWriter, c call) bool {
				if c.action.Data.ID == actionID("fixture echo") {
					time.Sleep(actionTimeout + 100*time.Millisecond)
				}
				return false
			}),
			fixtures: echoOnly, check: "fixture echo", reasons: []string{"Client.Timeout exceeded"},
		},
		{
			name: "strict envelope",
			ext: breaks(func(w http.ResponseWriter, c call) bool {
				if bytes.Contains(c.body, []byte("conformanceFutureField")) {
					http.Error(w, "unknown field", http.StatusBadRequest)
					return true
				}
				return false
			}),
			fixtures: echoOnly, check: "fixture echo with unknown fields", advice: []string{"answered 400, want 200"},
		},
		{
			name: "takes a pair on both channels", ext: takesDirect,
			fixtures: echoOnly, check: "fixture echo on the other channel", advice: []string{"status 1, want 0"},
		},
		{
			name: "takes a direct action tagged threshold",
			ext: breaks(func(w http.ResponseWriter, c call) bool {
				if c.action.Data.Type != types.Direct || c.action.Data.SubmissionTag != types.Threshold {
					return false
				}
				accept(w, c)
				return true
			}),
			fixtures: goodFixtures()[3:], check: "fixture once tagged threshold", advice: []string{"status 1, want 0"},
		},
		{
			name: "default handler takes unknown ops", ext: takesDirect,
			fixtures: echoOnly, check: "unknown op, direct", advice: []string{"status 1, want 0"},
		},
		{
			name: "takes a system op",
			ext: breaks(func(w http.ResponseWriter, c call) bool {
				if !strings.HasPrefix(string(op.HashToOPType(c.op.OPType)), systemPrefix) {
					return false
				}
				accept(w, c)
				return true
			}),
			fixtures: echoOnly, check: "system op, instruction", advice: []string{"status 1, want 0"},
		},
		{
			name: "takes an undecodable message",
			ext: breaks(func(w http.ResponseWriter, c call) bool {
				if c.decoded {
					return false
				}
				accept(w, c)
				return true
			}),
			fixtures: echoOnly, check: "undecodable message, direct", advice: []string{"status 1, want 0"},
		},
		{
			name: "refusals carry data",
			ext: rewrites(func(r *types.ActionResult) {
				if r.Status == 0 {
					r.Log, r.Data = "refused", []byte("x")
				}
			}),
			fixtures: echoOnly, check: "unknown op, instruction",
			advice: []string{`log "refused", want "error: …"`, "data 0x78, want 0x"},
		},
		{
			name: "not JSON", fixtures: echoOnly, check: "GET /state", advice: []string{"want application/json"},
			ext: func(int) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Write([]byte(`{"stateVersion":"0x` + strings.Repeat("00", 32) + `","state":"0x"}`)) //nolint:errcheck,gosec // test fake
				})
			},
		},
		{
			name: "200 everywhere: state", ext: ok200,
			fixtures: echoOnly, check: "GET /state", reasons: []string{"missing fields: stateVersion, state"},
		},
		{
			name: "200 everywhere: not an action", ext: ok200,
			fixtures: echoOnly, check: "a body that isn't an action", advice: []string{"answered 200, want 400"},
		},
		{
			name: "200 everywhere: routing", ext: ok200,
			fixtures: echoOnly, check: "routing",
			advice: []string{
				"GET /action answered 200, want 405",
				"POST /state answered 200, want 405",
				"GET /conformance-unknown-path answered 200, want 404",
			},
		},
		{
			name: "refuses its own tag",
			ext: func(int) http.Handler {
				s := extension.New(extension.WithLogger(logger.Nop{}))
				s.RegisterDirectHandler(testType, directCommand, echoDirect)
				return s
			},
			fixtures: goodFixtures()[2:3], check: "fixture tagged", reasons: []string{"status 0, want 1"},
		},
		{
			name:     "a resend refused without again",
			ext:      good,
			fixtures: []Fixture{func() Fixture { f := goodFixtures()[3]; f.Again = nil; return f }()},
			check:    "fixture once with unknown fields", advice: []string{"status 0, want 1"},
		},
		{
			name: "a resend not refused",
			ext: func(int) http.Handler {
				s := extension.New(extension.WithLogger(logger.Nop{}))
				s.RegisterDirectHandler(testType, onceCommand, echoDirect)
				return s
			},
			fixtures: goodFixtures()[3:], check: "fixture once sent again", reasons: []string{"status 1, want 0"},
		},
		{
			name:     "unexpected post",
			ext:      good,
			fixtures: []Fixture{func() Fixture { f := goodFixtures()[1]; f.Result.Status = 1; f.Posts = nil; return f }()},
			check:    "posts", reasons: []string{"2 posts, want 0"},
		},
		{
			name: "missing post",
			ext: job(func(context.Context, *extension.Progress) ([]byte, error) {
				return []byte("done"), nil
			}),
			fixtures: goodFixtures(), check: "posts", reasons: []string{"1 posts, want 2"},
		},
		{
			name: "wrong post",
			ext: job(func(ctx context.Context, p *extension.Progress) ([]byte, error) {
				if err := p.Post(ctx, []byte("p2")); err != nil {
					return nil, err
				}
				return []byte("done"), nil
			}),
			fixtures: goodFixtures(), check: "posts", reasons: []string{"post 1: data 0x7032, want 0x7031"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := results(t, target(t, tt.ext), tt.fixtures)[tt.check]
			for _, c := range []struct {
				err  error
				want []string
			}{{got.Err, tt.reasons}, {got.Advice, tt.advice}} {
				if len(c.want) == 0 {
					continue
				}
				if assert.Error(t, c.err) {
					for _, r := range c.want {
						assert.Contains(t, c.err.Error(), r)
					}
				}
			}
			if len(tt.reasons) == 0 {
				assert.NoError(t, got.Err, "only conventions broken")
			}
		})
	}
}

// Posts that land out of order still match: tee-proxy keeps the highest.
func TestPostsInStatusOrder(t *testing.T) {
	t.Parallel()
	f := goodFixtures()[1]
	c := newChecker("", fakenode.New())
	id := actionID("job")
	c.sent = []sent{{id: id, tag: types.Submit, op: f.opID(), posts: f.Posts, fixture: true}}
	for _, p := range slices.Backward(f.Posts) {
		postResult(t, c.node, types.ActionResult{
			ID: id, SubmissionTag: types.Submit, Status: p.Status, Log: p.Log,
			OPType: f.OPType.Hash(), OPCommand: f.OPCommand.Hash(), Version: resultVersion, Data: p.Data,
		})
	}
	assert.Equal(t, verdict{}, c.posts(t.Context(), testPostWait))
}
