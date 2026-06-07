package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeMailer captures sent messages (and can simulate a transport failure).
type fakeMailer struct {
	sent []*Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m *Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

func testConfig() *Config {
	return &Config{
		Listen: ":0", Recipient: "dest@example.org", FromAddress: "from@proton.test",
		FromName: "CCC Wiki Contact", WikiName: "CCC Wiki", AllowedDomain: "vanderbilt.edu",
		Transport: "smtp", SMTPHost: "smtp.test", SMTPPort: 587, SMTPEncryption: "starttls",
		GitHubAPIBase: "https://api.github.com", RateLimitPerHour: 5,
	}
}

func newTestServer(t *testing.T, cfg *Config) (*server, *fakeMailer) {
	t.Helper()
	s, err := newServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	fm := &fakeMailer{}
	s.mailer = fm
	s.now = func() time.Time { return time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC) }
	return s, fm
}

func validVals() url.Values {
	v := url.Values{}
	v.Set("type", "bug")
	v.Set("name", "Jane Doe")
	v.Set("email", "jane@vanderbilt.edu")
	v.Set("summary", "Login 500")
	v.Set("details", "Steps to reproduce")
	return v
}

func post(s *server, v url.Values, withCSRF bool) *httptest.ResponseRecorder {
	if withCSRF {
		v.Set("_csrf", "tok123")
	}
	req := httptest.NewRequest(http.MethodPost, "/contact/submit", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if withCSRF {
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "tok123"})
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

func TestFormGET(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodGet, "/contact", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`name="website"`, `name="_csrf"`, "Bug report", "Send"} {
		if !strings.Contains(body, want) {
			t.Errorf("form body missing %q", want)
		}
	}
	var hasCookie bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookie && c.Value != "" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Error("GET /contact did not set a CSRF cookie")
	}
}

func TestSubmitHappyPath(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	rec := post(s, validVals(), true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
	}
	if len(fm.sent) != 1 {
		t.Fatalf("sent %d messages, want 1", len(fm.sent))
	}
	m := fm.sent[0]
	if m.To != "dest@example.org" {
		t.Errorf("To = %q", m.To)
	}
	if m.FromAddr != "from@proton.test" {
		t.Errorf("From = %q", m.FromAddr)
	}
	if m.ReplyTo != "jane@vanderbilt.edu" {
		t.Errorf("Reply-To = %q, want submitter", m.ReplyTo)
	}
	if m.Subject != "[CCC Wiki] Bug report - Login 500" {
		t.Errorf("Subject = %q", m.Subject)
	}
	if m.Headers["X-CCC-Contact-Type"] != "bug" {
		t.Errorf("X-CCC-Contact-Type = %q", m.Headers["X-CCC-Contact-Type"])
	}
}

func TestSubmitNoCSRF(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	rec := post(s, validVals(), false)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if len(fm.sent) != 0 {
		t.Error("message sent despite missing CSRF")
	}
}

func TestSubmitHoneypot(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	v := validVals()
	v.Set("website", "http://spam.example")
	rec := post(s, v, true)
	if rec.Code != http.StatusOK {
		t.Errorf("honeypot status = %d, want 200 (silent success)", rec.Code)
	}
	if len(fm.sent) != 0 {
		t.Error("honeypot submission was actually sent")
	}
}

func TestSubmitBadDomain(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	v := validVals()
	v.Set("email", "jane@gmail.com")
	rec := post(s, v, true)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "vanderbilt.edu") {
		t.Error("expected domain-rejection message in re-rendered form")
	}
	if len(fm.sent) != 0 {
		t.Error("rejected submission was sent")
	}
}

func TestSubmitMissingSummary(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	v := validVals()
	v.Del("summary")
	if rec := post(s, v, true); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSubmitMailNotConfigured(t *testing.T) {
	cfg := testConfig()
	cfg.SMTPHost = "" // not ready
	s, _ := newTestServer(t, cfg)
	if rec := post(s, validVals(), true); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestSubmitMailerError(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	fm.err = io.ErrUnexpectedEOF
	if rec := post(s, validVals(), true); rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestSubmitRateLimit(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitPerHour = 2
	s, _ := newTestServer(t, cfg)
	if rec := post(s, validVals(), true); rec.Code != http.StatusOK {
		t.Fatalf("attempt 1 = %d, want 200", rec.Code)
	}
	if rec := post(s, validVals(), true); rec.Code != http.StatusOK {
		t.Fatalf("attempt 2 = %d, want 200", rec.Code)
	}
	if rec := post(s, validVals(), true); rec.Code != http.StatusTooManyRequests {
		t.Errorf("attempt 3 = %d, want 429", rec.Code)
	}
}

func TestHealthAndReady(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	get := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		return rec.Code
	}
	if c := get("/healthz"); c != http.StatusOK {
		t.Errorf("/healthz = %d, want 200", c)
	}
	if c := get("/contact/healthz"); c != http.StatusOK {
		t.Errorf("/contact/healthz = %d, want 200", c)
	}
	if c := get("/readyz"); c != http.StatusOK {
		t.Errorf("/readyz = %d, want 200 (mail configured)", c)
	}

	cfg := testConfig()
	cfg.SMTPHost = ""
	notReady, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	notReady.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz (unconfigured) = %d, want 503", rec.Code)
	}
}

func TestRootRedirect(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/contact" {
		t.Errorf("Location = %q, want /contact", loc)
	}
}
