package wikiclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests are network-free and dependency-free: they point the client at an
// httptest server. They prove the TRANSPORT contract (auth, retry, error mapping,
// no-secret-logging) — distinct from the real-BookStack integration test, which is
// tracked as a follow-up issue (bats, against a live stack).

const (
	testToken  = "tokid123:secretABC456"
	testSecret = "secretABC456"
)

// fastClient builds a Client pointed at baseURL with a 1ms retry delay so retry
// tests don't sleep for real.
func fastClient(t *testing.T, baseURL string, maxRetries int) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:        baseURL,
		Token:          testToken,
		HTTPTimeout:    2 * time.Second,
		MaxRetries:     maxRetries,
		RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestAuthHeaderSent(t *testing.T) {
	var gotAuth, gotAccept, gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		gotMethod = r.Method
		_, _ = w.Write([]byte(`{"id":7,"name":"Runbook"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	if _, err := c.GetPage(context.Background(), 7); err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if want := "Token " + testToken; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/pages/7" {
		t.Errorf("got %s %s, want GET /api/pages/7", gotMethod, gotPath)
	}
}

func TestRetriesOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"name":"ok"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 3)
	b, err := c.GetBook(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3 (two 503s then success)", calls.Load())
	}
	if b.Name != "ok" {
		t.Errorf("decoded book name = %q, want ok", b.Name)
	}
}

func TestNoRetryOn4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":422,"message":"The name field is required."}}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 3)
	_, err := c.CreatePage(context.Background(), Page{BookID: 1})
	if err == nil {
		t.Fatal("expected an error on 422")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (a 4xx must not be retried)", calls.Load())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *APIError: %T", err)
	}
	if apiErr.StatusCode != 422 || apiErr.Retryable() {
		t.Errorf("status=%d retryable=%v, want 422 non-retryable", apiErr.StatusCode, apiErr.Retryable())
	}
}

func TestErrorEnvelopeParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Page not found"}}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	_, err := c.GetPage(context.Background(), 999)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *APIError: %v", err)
	}
	if apiErr.StatusCode != 404 || apiErr.Code != 404 || apiErr.Message != "Page not found" {
		t.Errorf("got %+v, want {404, 404, \"Page not found\"}", apiErr)
	}
}

func TestErrorNonJSONFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`<html>502 Bad Gateway</html>`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0) // no retries: fail immediately so the test is fast
	_, err := c.ListBooks(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *APIError: %v", err)
	}
	if apiErr.StatusCode != 502 || apiErr.Message != "Bad Gateway" {
		t.Errorf("got %+v, want 502 'Bad Gateway' fallback", apiErr)
	}
}

// TestTokenNeverLogged makes the no-secret-in-logs promise a fitness function: the
// token (and its secret half) must appear in neither the logger output nor the error.
func TestTokenNeverLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"unavailable"}}`))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	c, err := New(Config{
		BaseURL: srv.URL, Token: testToken,
		HTTPTimeout: 2 * time.Second, MaxRetries: 2, RetryBaseDelay: time.Millisecond,
	}, WithLogger(logger))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.GetPage(context.Background(), 1)
	if err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	logs := buf.String()
	for _, secret := range []string{testToken, testSecret} {
		if strings.Contains(logs, secret) {
			t.Errorf("token/secret leaked into logs: %q", logs)
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("token/secret leaked into error: %q", err.Error())
		}
	}
	if buf.Len() == 0 {
		t.Error("expected retry warnings to be logged (sanity check the logger is wired)")
	}
}

// TestReturnsWhatYouWrote confirms create returns the server-assigned fields the
// caller couldn't have known (id/slug), saving a follow-up GET.
func TestReturnsWhatYouWrote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"name":"Runbook"`) {
			t.Errorf("request body missing name: %s", b)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":42,"name":"Runbook","slug":"runbook","book_id":3}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	p, err := c.CreatePage(context.Background(), Page{BookID: 3, Name: "Runbook", Markdown: "# hi"})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if p.ID != 42 || p.Slug != "runbook" {
		t.Errorf("returned page = %+v, want id=42 slug=runbook", p)
	}
}

func TestRejectsMalformedToken(t *testing.T) {
	_, err := New(Config{BaseURL: "http://x", Token: "no-colon-here"})
	if err == nil {
		t.Fatal("expected an error for a malformed token")
	}
	if strings.Contains(err.Error(), "no-colon-here") {
		t.Errorf("validation error must not echo the token value: %q", err.Error())
	}
}

// New must normalize a trailing slash on BaseURL even when a consumer constructs
// Config directly (not via Load) — otherwise baseURL+path yields a double slash.
func TestNewNormalizesTrailingSlash(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":7}`))
	}))
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL + "/", Token: testToken, MaxRetries: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.GetPage(context.Background(), 7); err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if gotPath != "/api/pages/7" {
		t.Errorf("path = %q, want /api/pages/7 (trailing slash not normalized -> double slash)", gotPath)
	}
}

// Load must fail loud on a present-but-malformed WIKI_* value rather than silently
// falling back to the default.
func TestLoadRejectsBadEnvValue(t *testing.T) {
	t.Setenv("WIKI_BASE_URL", "http://x")
	t.Setenv("WIKI_API_TOKEN", "id:secret")
	t.Setenv("WIKI_HTTP_TIMEOUT", "nope")
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error for a malformed WIKI_HTTP_TIMEOUT")
	}
	if !strings.Contains(err.Error(), "WIKI_HTTP_TIMEOUT") {
		t.Errorf("error should name the offending variable: %v", err)
	}
}

func TestListBooksEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"total":0}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	books, err := c.ListBooks(context.Background())
	if err != nil {
		t.Fatalf("ListBooks: %v", err)
	}
	if len(books) != 0 {
		t.Errorf("got %d books, want 0", len(books))
	}
}

func TestListBooksMultiple(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"A"},{"id":2,"name":"B"}],"total":2}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	books, err := c.ListBooks(context.Background())
	if err != nil {
		t.Fatalf("ListBooks: %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("got %d books, want 2", len(books))
	}
	if books[0].Name != "A" || books[1].Name != "B" {
		t.Errorf("book names = %q %q, want A B", books[0].Name, books[1].Name)
	}
}

func TestUpdateBook(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1,"name":"Updated"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	b, err := c.UpdateBook(context.Background(), 1, Book{Name: "Updated"})
	if err != nil {
		t.Fatalf("UpdateBook: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/api/books/1" {
		t.Errorf("path = %s, want /api/books/1", gotPath)
	}
	if !strings.Contains(string(gotBody), `"name":"Updated"`) {
		t.Errorf("body missing Updated: %s", gotBody)
	}
	if b.Name != "Updated" {
		t.Errorf("returned name = %q, want Updated", b.Name)
	}
}

func TestUpdatePage(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1,"name":"Updated Page"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	p, err := c.UpdatePage(context.Background(), 1, Page{Name: "Updated Page", Markdown: "# v2"})
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/api/pages/1" {
		t.Errorf("path = %s, want /api/pages/1", gotPath)
	}
	if p.Name != "Updated Page" {
		t.Errorf("returned name = %q, want Updated Page", p.Name)
	}
}

func TestCreateBook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":42,"name":"New Book","slug":"new-book"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	b, err := c.CreateBook(context.Background(), Book{Name: "New Book"})
	if err != nil {
		t.Fatalf("CreateBook: %v", err)
	}
	if b.ID != 42 || b.Slug != "new-book" {
		t.Errorf("returned book = %+v, want id=42 slug=new-book", b)
	}
}

func TestRetryBudgetExhausted(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"always down"}}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 2) // 2 retries = 3 total attempts
	_, err := c.GetBook(context.Background(), 1)
	if err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3 (initial + 2 retries exhausted)", calls.Load())
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("final error is not *APIError: %T", err)
	}
	if !apiErr.Retryable() {
		t.Error("the final exhausted-retry error should still be retryable (5xx)")
	}
}

func TestTransportErrorNotRetriedWhenNoRetries(t *testing.T) {
	c := fastClient(t, "http://127.0.0.1:1", 0) // unreachable, 0 retries
	_, err := c.GetBook(context.Background(), 1)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("transport error must not leak the token: %q", err.Error())
	}
}

func TestTransportErrorWithRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Close the connection without responding to trigger a transport error.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijack")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 2)
	_, err := c.GetBook(context.Background(), 1)
	if err == nil {
		t.Fatal("expected a transport error after retries")
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3 (initial + 2 retries)", calls.Load())
	}
}

func TestConfigValidateEmptyBaseURL(t *testing.T) {
	err := (Config{Token: "a:b"}).validate()
	if err == nil {
		t.Fatal("expected error for empty BaseURL")
	}
}

func TestConfigValidateNoScheme(t *testing.T) {
	err := (Config{BaseURL: "localhost:8080", Token: "a:b"}).validate()
	if err == nil {
		t.Fatal("expected error for missing scheme")
	}
}

func TestConfigValidateEmptyToken(t *testing.T) {
	err := (Config{BaseURL: "http://localhost", Token: ""}).validate()
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestConfigValidateNoColonInToken(t *testing.T) {
	err := (Config{BaseURL: "http://localhost", Token: "justid"}).validate()
	if err == nil {
		t.Fatal("expected error for token without colon")
	}
}

func TestConfigValidateEmptyIdInToken(t *testing.T) {
	err := (Config{BaseURL: "http://localhost", Token: ":secret"}).validate()
	if err == nil {
		t.Fatal("expected error for token with empty id")
	}
}

func TestConfigValidateNegativeRetries(t *testing.T) {
	err := (Config{BaseURL: "http://localhost", Token: "a:b", MaxRetries: -1}).validate()
	if err == nil {
		t.Fatal("expected error for negative MaxRetries")
	}
}

func TestWithHTTPClientNil(t *testing.T) {
	c, err := New(Config{BaseURL: "http://x", Token: "a:b", MaxRetries: 0}, WithHTTPClient(nil))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.httpc == nil {
		t.Error("WithHTTPClient(nil) must not replace the default client")
	}
}

func TestGetBookNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Book not found"}}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	_, err := c.GetBook(context.Background(), 999)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *APIError: %v", err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("status = %d, want 404", apiErr.StatusCode)
	}
}

func TestGetPageNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Page not found"}}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	_, err := c.GetPage(context.Background(), 999)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not *APIError: %v", err)
	}
	if apiErr.Message != "Page not found" {
		t.Errorf("message = %q, want 'Page not found'", apiErr.Message)
	}
}

func TestListPages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"Page A"},{"id":2,"name":"Page B"}],"total":2}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	pages, err := c.ListPages(context.Background())
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
}

func TestApiErrorFromEmptyBody(t *testing.T) {
	e := apiErrorFrom(503, []byte{}, "GET", "/api/books")
	if e.StatusCode != 503 || e.Message != "Service Unavailable" {
		t.Errorf("apiErrorFrom empty body = %+v, want 503 'Service Unavailable'", e)
	}
}

func TestApiErrorFromNonJSONBody(t *testing.T) {
	e := apiErrorFrom(502, []byte(`<html>bad gateway</html>`), "POST", "/api/pages")
	if e.StatusCode != 502 || e.Message != "Bad Gateway" {
		t.Errorf("apiErrorFrom non-JSON = %+v, want 502 'Bad Gateway'", e)
	}
}

func TestConcurrentClientSafe(t *testing.T) {
	var mu sync.Mutex
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"id":1,"name":"ok"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetBook(context.Background(), 1)
			if err != nil {
				t.Errorf("concurrent GetBook: %v", err)
			}
		}()
	}
	wg.Wait()
	if callCount != 20 {
		t.Errorf("call count = %d, want 20", callCount)
	}
}

func TestNewNormalizesBaseURL(t *testing.T) {
	// Add the missing sync import if needed.
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"data":[],"total":0}`))
	}))
	defer srv.Close()

	for _, base := range []string{srv.URL, srv.URL + "/"} {
		c, err := New(Config{BaseURL: base, Token: testToken, MaxRetries: 0})
		if err != nil {
			t.Fatalf("New(%q): %v", base, err)
		}
		_, _ = c.ListBooks(context.Background())
		if gotPath != "/api/books" {
			t.Errorf("base=%q path=%q, want /api/books", base, gotPath)
		}
	}
}
