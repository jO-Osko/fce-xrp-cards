package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/flare-foundation/go-flare-common/pkg/tee/op"
	"github.com/flare-foundation/tee-node/pkg/types"
)

// Run is a handler's long-running part. The library starts it in the
// background once the answer is surely status 2, possibly before the node has
// that answer; ctx ends at server shutdown. Partial results go through p; the
// return becomes the final result, posted as status 1, or 0 on error, like a
// handler's.
type Run func(ctx context.Context, p *Progress) ([]byte, error)

// resultPoster is signport.Client's PostResult; tests swap it.
type resultPoster interface {
	PostResult(ctx context.Context, r *types.ActionResult) error
}

// Progress posts a long-running job's partial results to the node's /result.
type Progress struct {
	s   *Server
	res types.ActionResult

	mu     sync.Mutex
	status uint8 // the last status used
	done   bool
}

// Post posts data as the job's next partial result, status 4 upward.
// Statuses end at 255, so a job has 252 partials; Post errors beyond.
// It blocks until the node takes the result; after the post window, ctx's end
// or server shutdown, the partial is dropped and logged, and Post returns the error.
// A job that shouldn't wait posts from its own goroutine; statuses follow call order.
func (p *Progress) Post(ctx context.Context, data []byte) error {
	p.mu.Lock()
	switch {
	case p.done:
		p.mu.Unlock()
		return errJobDone
	case p.status == lastPartial:
		p.mu.Unlock()
		return errNoStatusLeft
	}
	p.status = max(p.status+1, firstPartial)
	r := partial(p.res, p.status, data)
	p.mu.Unlock()

	// a failed partial would end the action, so an oversized one isn't posted
	if p.s.tooLarge(&r) {
		return ErrResultTooLarge
	}
	return p.s.post(ctx, &r)
}

func (s *Server) start(res types.ActionResult, run Run) {
	p := &Progress{s: s, res: res, status: statusPending}
	go func() {
		data, err := s.runJob(p, run)

		p.mu.Lock()
		p.done = true
		p.mu.Unlock()

		final := succeed(res, data)
		if err != nil {
			final = fail(res, err)
		}
		final = s.fit(final)
		_ = s.post(s.jobs, &final) // a dropped result is logged; nothing waits for it
	}()
}

func (s *Server) runJob(p *Progress, run Run) (data []byte, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			// the panic value isn't logged: it may carry secrets
			s.log.Errorf("recovered a panic in the long-running job for %q / %q", op.HashToOPType(p.res.OPType), op.HashToOPCommand(p.res.OPCommand))
			data, err = nil, errPanic
		}
	}()
	return run(s.jobs, p)
}

// post retries within the post window, and stops at shutdown whatever ctx the job passed.
func (s *Server) post(ctx context.Context, r *types.ActionResult) error {
	ctx, cancel := context.WithTimeout(ctx, s.postWindow)
	defer cancel()
	stop := context.AfterFunc(s.jobs, cancel)
	defer stop()

	if err := s.poster.PostResult(ctx, r); err != nil {
		s.log.Errorf("dropping status %d for action %s (%q / %q): %v",
			r.Status, r.ID.Hex(), op.HashToOPType(r.OPType), op.HashToOPCommand(r.OPCommand), err)
		return fmt.Errorf("posting the result: %w", err)
	}
	return nil
}

// fit turns a result the proxy would refuse into a small failure.
func (s *Server) fit(r types.ActionResult) types.ActionResult {
	if s.tooLarge(&r) {
		return fail(r, ErrResultTooLarge)
	}
	return r
}

// tooLarge measures the body the node posts to the proxy: the result and its
// signature. Data is added as hex, so a large result is never encoded here.
func (s *Server) tooLarge(r *types.ActionResult) bool {
	head := *r
	head.Data = nil
	b, err := json.Marshal(types.ActionResponse{Result: head, Signature: make([]byte, crypto.SignatureLength)})
	return err != nil || len(b)+2*len(r.Data) > s.maxResult
}
