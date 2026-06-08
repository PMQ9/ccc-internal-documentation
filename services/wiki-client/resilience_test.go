package wikiclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests harden the transport's RESILIENCE and ERROR-MAPPING contract beyond
// the happy-path coverage in client_test.go: what the retry loop does when the
// caller's context dies mid-flight, how a malformed-but-2xx body is handled (a
// decode bug that the original suite never exercised), which status codes are
// retried vs treated as terminal, and that the secret never rides on a GET. They
// are network-free (httptest) and fast (sub-millisecond retry delay).

// ---- context lifecycle: a dead context must short-circuit the retry budget ----

// A context cancelled while a 5xx retry is pending must abort with ctx.Err() and
// NOT burn the remaining retry budget. The long base delay guarantees the cancel
// wins the backoff select before the timer fires.
func TestContextCanceledAbortsRetryBudget(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		cancel() // cancel as soon as the first attempt is in flight
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, err := New(Config{
		BaseURL: srv.URL, Token: testToken,
		HTTPTimeout: 2 * time.Second, MaxRetries: 5, RetryBaseDelay: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.GetBook(ctx, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (cancel must abort before the 2nd attempt)", got)
	}
}

// A per-request deadline must bound total wall-clock: a 5xx server with a generous
// retry budget still returns context.DeadlineExceeded ~at the deadline, not after
// exhausting all retries.
func TestContextDeadlineDuringBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c, err := New(Config{
		BaseURL: srv.URL, Token: testToken,
		HTTPTimeout: 2 * time.Second, MaxRetries: 10, RetryBaseDelay: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = c.GetBook(ctx, 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %v — must bail at the deadline, not exhaust 10 retries", elapsed)
	}
}

// ---- body decoding on a 2xx response --------------------------------------

// A 200 with a truncated/garbage JSON body is a decode failure, surfaced as a
// plain error (NOT *APIError) and NOT retried — a malformed success is the
// server's bug, and retrying it just wastes the budget.
func TestMalformed2xxBodyIsDecodeErrorNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"id":1, "name": `)) // truncated JSON, HTTP 200
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 3)
	_, err := c.GetBook(context.Background(), 1)
	if err == nil {
		t.Fatal("expected a decode error on a malformed 2xx body")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want a decode error", err)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Errorf("a decode failure must not map to *APIError: %v", apiErr)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (a 2xx decode failure is not retried)", calls.Load())
	}
}

// A 200 with an EMPTY body is not an error: out is left at its zero value (the
// `len(respBody) > 0` guard). Proves an empty-but-successful response doesn't
// spuriously fail.
func TestEmpty2xxBodyYieldsZeroValueNoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // no body
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	p, err := c.GetPage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetPage on empty 2xx body: %v", err)
	}
	if p.ID != 0 || p.Name != "" {
		t.Errorf("page = %+v, want the zero value on an empty body", p)
	}
}

// ---- the retry/terminal classification, exhaustively ----------------------

// Only 5xx is retried; every 4xx is terminal. This locks the classification down
// status-by-status so a future "retry on 429" or "don't retry 500" regression is
// caught with the exact attempt count.
func TestRetryableStatusMatrix(t *testing.T) {
	cases := []struct {
		status    int
		wantCalls int
		retryable bool
	}{
		{http.StatusInternalServerError, 3, true},  // 500
		{http.StatusBadGateway, 3, true},           // 502
		{http.StatusServiceUnavailable, 3, true},   // 503
		{http.StatusGatewayTimeout, 3, true},       // 504
		{http.StatusBadRequest, 1, false},          // 400
		{http.StatusUnauthorized, 1, false},        // 401
		{http.StatusForbidden, 1, false},           // 403 (least-privilege denial)
		{http.StatusNotFound, 1, false},            // 404
		{http.StatusConflict, 1, false},            // 409
		{http.StatusUnprocessableEntity, 1, false}, // 422
		{http.StatusTooManyRequests, 1, false},     // 429
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"code":` + strconv.Itoa(tc.status) + `,"message":"x"}}`))
			}))
			defer srv.Close()

			c := fastClient(t, srv.URL, 2) // 2 retries => 3 attempts when retryable
			_, err := c.GetBook(context.Background(), 1)
			if err == nil {
				t.Fatalf("status %d: expected an error", tc.status)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("status %d: error is not *APIError: %v", tc.status, err)
			}
			if apiErr.Retryable() != tc.retryable {
				t.Errorf("status %d: Retryable()=%v, want %v", tc.status, apiErr.Retryable(), tc.retryable)
			}
			if int(calls.Load()) != tc.wantCalls {
				t.Errorf("status %d: calls=%d, want %d", tc.status, calls.Load(), tc.wantCalls)
			}
		})
	}
}

// ---- request shape --------------------------------------------------------

// Content-Type: application/json is set on a request WITH a body (POST) and
// omitted on a bodyless GET — sending it on a GET is a minor protocol smell some
// strict servers reject.
func TestContentTypeHeaderOnlyOnWrite(t *testing.T) {
	var getCT, postCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			getCT = r.Header.Get("Content-Type")
		case http.MethodPost:
			postCT = r.Header.Get("Content-Type")
		}
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	if _, err := c.GetBook(context.Background(), 1); err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if _, err := c.CreateBook(context.Background(), Book{Name: "x"}); err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	if getCT != "" {
		t.Errorf("GET sent Content-Type %q, want none (no request body)", getCT)
	}
	if postCT != "application/json" {
		t.Errorf("POST Content-Type = %q, want application/json", postCT)
	}
}

// countingRT wraps a RoundTripper and counts trips, to prove WithHTTPClient's
// transport actually carries the request (not just that New accepted it).
type countingRT struct {
	n  *atomic.Int32
	rt http.RoundTripper
}

func (c countingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.rt.RoundTrip(r)
}

func TestWithHTTPClientIsUsed(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	custom := &http.Client{Transport: countingRT{n: &n, rt: http.DefaultTransport}}
	c, err := New(Config{BaseURL: srv.URL, Token: testToken, MaxRetries: 0}, WithHTTPClient(custom))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GetBook(context.Background(), 1); err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if n.Load() != 1 {
		t.Errorf("injected transport used %d times, want 1", n.Load())
	}
}

// jitter must stay within [d/2, d] (full jitter on the top half) and clamp a
// non-positive duration to 0 — the property the backoff math relies on.
func TestJitterBounds(t *testing.T) {
	for _, d := range []time.Duration{time.Millisecond, 10 * time.Millisecond, time.Second} {
		for i := 0; i < 200; i++ {
			j := jitter(d)
			if j < d/2 || j > d {
				t.Fatalf("jitter(%v) = %v, want within [%v, %v]", d, j, d/2, d)
			}
		}
	}
	if got := jitter(0); got != 0 {
		t.Errorf("jitter(0) = %v, want 0", got)
	}
	if got := jitter(-5 * time.Second); got != 0 {
		t.Errorf("jitter(negative) = %v, want 0", got)
	}
}
