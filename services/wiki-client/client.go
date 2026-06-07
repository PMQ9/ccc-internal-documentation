package wikiclient

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"time"
)

// Client is a configured BookStack REST client, safe for concurrent use. Construct
// it with New; the token it holds is unexported and never logged.
type Client struct {
	baseURL    string
	token      string // secret: never logged; only ever set as an Authorization header
	httpc      *http.Client
	maxRetries int
	baseDelay  time.Duration
	log        *slog.Logger // optional; never receives the token
}

// Option customizes a Client without widening Config. The CLI (#28) and MCP server
// (#29) use these to inject their own *http.Client (transport/proxy) or a logger.
type Option func(*Client)

// WithHTTPClient replaces the default *http.Client (e.g. a custom transport). The
// Client still applies its own per-attempt context, auth, and retry budget.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpc = h
		}
	}
}

// WithLogger attaches a structured logger for request diagnostics. The logger is
// never passed the token or its secret half.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) { c.log = l }
}

// New builds a Client from validated configuration.
func New(cfg Config, opts ...Option) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := &Client{
		baseURL:    cfg.BaseURL,
		token:      cfg.Token,
		httpc:      &http.Client{Timeout: cfg.HTTPTimeout},
		maxRetries: cfg.MaxRetries,
		baseDelay:  cfg.RetryBaseDelay,
	}
	if c.baseDelay <= 0 {
		c.baseDelay = 200 * time.Millisecond
	}
	for _, o := range opts {
		o(c)
	}
	return c, nil
}

const maxRespBody = 8 << 20 // 8 MiB cap on a response body we buffer/parse

// do is the single request path: it builds the authed request, executes it under
// the retry policy, maps a non-2xx to *APIError, and decodes a 2xx body into out
// (out may be nil to discard). Every entity method routes through here, so auth,
// retry, and error mapping live in exactly one place.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var bodyBytes []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("wikiclient: marshal %s %s: %w", method, path, err)
		}
		bodyBytes = b
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := c.backoff(ctx, attempt); err != nil {
				return err // context cancelled/expired during backoff
			}
		}

		// A fresh request + body reader each attempt (a consumed reader can't replay).
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
		if err != nil {
			return fmt.Errorf("wikiclient: build request %s %s: %w", method, path, err)
		}
		req.Header.Set("Authorization", "Token "+c.token) // the one place the token touches the wire
		req.Header.Set("Accept", "application/json")
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpc.Do(req)
		if err != nil {
			// Transport error (incl. timeout / context). Retry within budget.
			lastErr = fmt.Errorf("wikiclient: %s %s: %w", method, path, err)
			if attempt < c.maxRetries {
				c.logRetry(method, path, attempt, 0, true)
			}
			continue
		}

		// Read + close the body so the connection can be reused.
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBody))
		_ = resp.Body.Close()

		if resp.StatusCode >= 500 {
			lastErr = apiErrorFrom(resp.StatusCode, respBody, method, path)
			if attempt < c.maxRetries {
				c.logRetry(method, path, attempt, resp.StatusCode, false)
			}
			continue // server-side: retry within budget
		}
		if resp.StatusCode/100 != 2 {
			return apiErrorFrom(resp.StatusCode, respBody, method, path) // 4xx: caller error, do not retry
		}

		if out != nil && len(respBody) > 0 {
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("wikiclient: decode %s %s response: %w", method, path, err)
			}
		}
		return nil
	}
	return lastErr
}

// backoff sleeps before a retry: exponential (baseDelay * 2^(attempt-1)) capped,
// plus full jitter, aborting early if the context is done.
func (c *Client) backoff(ctx context.Context, attempt int) error {
	const maxDelay = 5 * time.Second
	d := c.baseDelay << (attempt - 1)
	if d > maxDelay || d <= 0 { // <=0 guards shift overflow at large attempt counts
		d = maxDelay
	}
	t := time.NewTimer(jitter(d))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// jitter returns a random duration in [d/2, d] (full jitter on the top half), using
// crypto/rand so there's no seeding and no math/rand global to disturb in a library.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	half := int64(d) / 2
	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(half+1))
	if err != nil {
		return d // never block on an RNG hiccup; fall back to the full delay
	}
	return time.Duration(half + n.Int64())
}

func (c *Client) logRetry(method, path string, attempt, status int, transportErr bool) {
	if c.log == nil {
		return
	}
	// Logs method/path/attempt/status only — never the token or the full URL.
	c.log.Warn("wikiclient retrying request",
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("attempt", attempt),
		slog.Int("status", status),
		slog.Bool("transport_error", transportErr),
	)
}

// apiErrorFrom parses BookStack's {"error":{"code","message"}} envelope, tolerating
// an empty or non-JSON body by falling back to the HTTP status text.
func apiErrorFrom(status int, body []byte, method, path string) *APIError {
	e := &APIError{StatusCode: status, Method: method, Path: path}
	var env struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		e.Code = env.Error.Code
		e.Message = env.Error.Message
	} else {
		if e.Message = http.StatusText(status); e.Message == "" {
			e.Message = "request failed"
		}
	}
	return e
}
