// Package signport calls tee-node's sign port on loopback. pkg/node wraps it
// for extensions; result posting stays here, so only the library can post.
package signport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"syscall"
	"time"

	"github.com/flare-foundation/tee-node/pkg/types"
)

// Errors never include the node's response body, which may echo inputs.
var (
	// ErrBadRequest means the node refused the input.
	ErrBadRequest = errors.New("sign port refused the request")
	// ErrNotFound means the node has nothing at the path, such as a wallet key the TEE doesn't hold.
	ErrNotFound = errors.New("sign port found nothing at the path")
	// ErrInvalidResponse means the node's answer couldn't be decoded.
	ErrInvalidResponse = errors.New("invalid sign port response")
)

const (
	// the node echoes signed messages back, base64-encoded
	maxResponseSize = 32 << 20

	retryMin = 50 * time.Millisecond
	retryMax = 500 * time.Millisecond
)

// Client is safe for concurrent use.
type Client struct {
	url  string
	http *http.Client
}

// New returns a Client for the sign port on 127.0.0.1:port.
func New(port int) *Client {
	return &Client{
		url: "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		// no proxy and no client timeout: each call's ctx bounds it
		http: &http.Client{Transport: &http.Transport{}},
	}
}

// Call posts in as JSON to path and decodes the answer into out.
// It retries a refused connection, which never reached the node, until ctx ends.
func (c *Client) Call(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding the request: %w", err)
	}
	return c.call(ctx, http.MethodPost, path, body, out, false)
}

// Get gets path and decodes the answer into out; it retries as Call does.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.call(ctx, http.MethodGet, path, nil, out, false)
}

// PostResult posts r to /result; the node signs it and forwards it to the proxy.
// It also retries 5xx until ctx ends: the node answers 5xx while it has no
// proxy URL or the proxy didn't take the result, and the proxy ignores a re-post.
func (c *Client) PostResult(ctx context.Context, r *types.ActionResult) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding the request: %w", err)
	}
	return c.call(ctx, http.MethodPost, "/result", body, nil, true)
}

// call sends body to path, none when nil.
func (c *Client) call(ctx context.Context, method, path string, body []byte, out any, retry5xx bool) error {
	wait := retryMin
	for {
		resp, err := c.send(ctx, method, path, body)
		var last error
		switch {
		case err == nil && retry5xx && resp.StatusCode >= http.StatusInternalServerError:
			resp.Body.Close() //nolint:errcheck,gosec // retried; the body is never read
			last = fmt.Errorf("sign port answered %d", resp.StatusCode)
		case err == nil:
			return decode(resp, out)
		case errors.Is(err, syscall.ECONNREFUSED):
			last = errors.New("sign port not listening")
		default:
			return fmt.Errorf("calling the sign port: %w", err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("giving up: %w (last: %w)", ctx.Err(), last)
		case <-time.After(wait):
		}
		wait = min(2*wait, retryMax)
	}
}

func (c *Client) send(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, r)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// decode reads out from a 200 answer; a nil out ignores the body.
func decode(resp *http.Response, out any) error {
	defer resp.Body.Close() //nolint:errcheck // the body is only read; a close error changes nothing

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest:
		return ErrBadRequest
	case http.StatusNotFound:
		return ErrNotFound
	default:
		return fmt.Errorf("sign port answered %d", resp.StatusCode)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(out); err != nil {
		return ErrInvalidResponse
	}
	return nil
}
