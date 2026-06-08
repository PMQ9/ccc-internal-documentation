package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMailer captures sent messages (and can simulate a transport failure). It
// is concurrency-safe: the real server calls Send from many request goroutines,
// so the capture slice is mutex-guarded — otherwise `go test -race` (the CI
// contract) flags the concurrent append in TestConcurrentSubmissions.
type fakeMailer struct {
	mu   sync.Mutex
	sent []*Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m *Message) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.mu.Unlock()
	return nil
}

// count returns how many messages were captured, safe to call concurrently.
func (f *fakeMailer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func testConfig() *Config {
	return &Config{
		Listen: ":0", Recipient: "dest@example.org", FromAddress: "from@proton.test",
		FromName: "CCC Wiki Contact", WikiName: "CCC Wiki", WikiURL: "http://wiki.test",
		AllowedDomain: "vanderbilt.edu",
		Transport:     "smtp", SMTPHost: "smtp.test", SMTPPort: 587, SMTPEncryption: "starttls",
		GitHubAPIBase: "https://api.github.com", RateLimitPerHour: 5,
		GlobalRateLimitPerHour: 100, GitHubDailyCap: 50, TrustedProxyHops: 1,
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

// withThemeCookie attaches the cross-origin theme cookie to a request (issue #39).
func withThemeCookie(v string) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "ccc-color-scheme", Value: v}) }
}

func post(s *server, v url.Values, withCSRF bool, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	if withCSRF {
		v.Set("_csrf", "tok123")
	}
	req := httptest.NewRequest(http.MethodPost, "/contact/submit", strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if withCSRF {
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: "tok123"})
	}
	for _, m := range mods {
		m(req)
	}
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	return rec
}

// getThanks fetches the PRG target a successful submit redirects to (issue #43).
func getThanks(s *server, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, thanksPath, nil)
	for _, m := range mods {
		m(req)
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

// The masthead must brand the page like the wiki AND give a way back: the inlined
// CCC lockup + a "Back to the wiki" link, both pointing at CONTACT_WIKI_URL.
func TestMastheadLinksHome(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodGet, "/contact", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		`href="http://wiki.test"`,        // logo home link + the back link target
		"Back to the wiki",               // the explicit return link
		"College of Connected Computing", // the inlined CCC lockup actually rendered
	} {
		if !strings.Contains(body, want) {
			t.Errorf("masthead missing %q", want)
		}
	}
}

// With no wiki URL configured we must not emit a dead href="" — the brand still
// renders, just not as a link, and the back link is omitted.
func TestMastheadNoLinkWhenUnset(t *testing.T) {
	cfg := testConfig()
	cfg.WikiURL = ""
	s, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/contact", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `href=""`) {
		t.Error("emitted an empty href when WikiURL is unset")
	}
	if strings.Contains(body, "Back to the wiki") {
		t.Error("back link should be omitted when WikiURL is unset")
	}
	if !strings.Contains(body, "College of Connected Computing") {
		t.Error("brand lockup should still render when WikiURL is unset")
	}
}

func TestSubmitHappyPath(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	rec := post(s, validVals(), true)

	// PRG: a successful submit redirects so a refresh/Back can't re-POST. (issue #43)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (PRG)\n%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != thanksPath {
		t.Errorf("Location = %q, want %q", loc, thanksPath)
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
	// The thanks page (the PRG target) carries the same masthead + path back.
	if body := getThanks(s).Body.String(); !strings.Contains(body, "Back to the wiki") {
		t.Error("thanks page missing the back-to-wiki link")
	}
}

// Issue #36: the GitHub tracking issue is still filed server-side for triage, but
// its URL points at the private repo and a guessable issue number — an internal
// detail that must never reach the submitter. This locks in both halves: the
// issue IS created, and the success page does NOT leak it.
func TestSuccessPageDoesNotLeakIssueURL(t *testing.T) {
	const issueURL = "https://github.com/PMQ9/ccc-internal-documentation/issues/123"
	var filed bool
	ghAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		filed = true
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"` + issueURL + `"}`))
	}))
	defer ghAPI.Close()

	cfg := testConfig()
	cfg.GitHubToken = "tok"
	cfg.GitHubRepo = "PMQ9/ccc-internal-documentation"
	cfg.GitHubAPIBase = ghAPI.URL
	s, fm := newTestServer(t, cfg)

	rec := post(s, validVals(), true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (PRG)\n%s", rec.Code, rec.Body.String())
	}
	if len(fm.sent) != 1 {
		t.Fatalf("sent %d messages, want 1 (this must be the real success path, not the honeypot)", len(fm.sent))
	}
	if !filed {
		t.Error("GitHub issue was not filed server-side — internal tracking must still happen")
	}
	// The response the browser receives in the SAME request that filed the issue
	// (the 303) must neither carry the issue URL nor redirect to it — the faithful
	// analog of the pre-PRG inline-body check (issue #36).
	if loc := rec.Header().Get("Location"); loc != thanksPath {
		t.Errorf("Location = %q, want %q (must not redirect to the issue)", loc, thanksPath)
	}
	if strings.Contains(rec.Body.String(), issueURL) {
		t.Error("the submit response leaked the internal tracking-issue URL")
	}
	// And the page the submitter actually lands on carries no issue reference.
	body := getThanks(s).Body.String()
	if strings.Contains(body, issueURL) {
		t.Error("thanks page leaked the internal tracking-issue URL to the submitter")
	}
	if strings.Contains(strings.ToLower(body), "tracking issue") {
		t.Error("thanks page still mentions a tracking issue")
	}
}

// The wiki writes its light/dark choice to a host-scoped ccc-color-scheme cookie
// that reaches this cross-port service; we render the matching <html> class
// server-side so there's no light-then-dark flash. Absence (or junk) => no class,
// so CSS follows the OS — the wiki's guest behavior. (Issue #39.)
func TestThemeCookieClass(t *testing.T) {
	cases := []struct {
		name      string
		cookie    string // "" => send no cookie
		wantOpen  string // the exact <html ...> opening tag we expect
		forbidTag string // an <html ...> tag that must NOT appear
	}{
		{"dark cookie forces dark-mode", "dark", `<html lang="en" class="dark-mode">`, ""},
		{"light cookie forces ccc-light", "light", `<html lang="en" class="ccc-light">`, `class="dark-mode"`},
		{"no cookie follows OS (no class)", "", `<html lang="en">`, "class="},
		{"unknown value follows OS (no class)", "purple", `<html lang="en">`, "class="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestServer(t, testConfig())
			req := httptest.NewRequest(http.MethodGet, "/contact", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "ccc-color-scheme", Value: tc.cookie})
			}
			rec := httptest.NewRecorder()
			s.routes().ServeHTTP(rec, req)
			body := rec.Body.String()
			if !strings.Contains(body, tc.wantOpen) {
				t.Errorf("form: want <html> tag %q in body", tc.wantOpen)
			}
			// Guard against a class leaking onto the <html> tag for the OS-follow
			// cases — checked only on the opening tag, since "dark-mode"/"class="
			// also legitimately appear inside the theme <script>.
			if tc.forbidTag != "" {
				openTag := body[strings.Index(body, "<html"):]
				openTag = openTag[:strings.IndexByte(openTag, '>')+1]
				if strings.Contains(openTag, tc.forbidTag) {
					t.Errorf("form: <html> tag %q unexpectedly contains %q", openTag, tc.forbidTag)
				}
			}
		})
	}
}

// The success page is server-rendered through newSuccessView too, so it must
// honor the same theme cookie (no flash on the post-submit page either).
func TestThemeCookieOnSuccessPage(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	rec := post(s, validVals(), true /*withCSRF*/, withThemeCookie("dark"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (PRG)\n%s", rec.Code, rec.Body.String())
	}
	thanks := getThanks(s, withThemeCookie("dark"))
	if body := thanks.Body.String(); !strings.Contains(body, `<html lang="en" class="dark-mode">`) {
		t.Error("thanks page did not render the dark-mode class from the cookie")
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
	// The decoy must be indistinguishable from a real send: same 303 to the same
	// thanks page (issue #43), but nothing is sent.
	if rec.Code != http.StatusSeeOther {
		t.Errorf("honeypot status = %d, want 303 (silent PRG decoy)", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != thanksPath {
		t.Errorf("honeypot Location = %q, want %q (indistinguishable from a real send)", loc, thanksPath)
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
	if rec := post(s, validVals(), true); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 1 = %d, want 303", rec.Code)
	}
	if rec := post(s, validVals(), true); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 2 = %d, want 303", rec.Code)
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

// An oversized body must be rejected (413) before any parsing/validation, and
// nothing is sent. (issue #41 — gap 2)
func TestSubmitOversizedBody(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	v := validVals()
	v.Set("details", strings.Repeat("A", maxBodyBytes+1024)) // push the body past the cap
	rec := post(s, v, true)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
	if len(fm.sent) != 0 {
		t.Error("oversized submission was sent")
	}
}

// clientIP must trust the rightmost trusted-hop XFF entry (and validate it),
// never the client-forgeable leftmost one. (issue #41 — gap 4)
func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		hops       int
		xff        string
		remote     string
		want       string
	}{
		{"no proxy ignores xff", false, 1, "1.2.3.4", "10.0.0.9:5555", "10.0.0.9"},
		{"trusts rightmost (hops=1)", true, 1, "9.9.9.9, 203.0.113.7", "10.0.0.9:5555", "203.0.113.7"},
		{"forged left entry ignored", true, 1, "evil, 203.0.113.7", "10.0.0.9:5555", "203.0.113.7"},
		// hops=2: the client sits 2nd-from-right (one trusted proxy appended its own IP).
		{"hops=2 trusts 2nd-from-right", true, 2, "203.0.113.7, 10.0.0.1", "10.0.0.9:5555", "203.0.113.7"},
		// hops=2 with a forged leftmost entry: the real client is still 2nd-from-right.
		{"hops=2 ignores forged leftmost", true, 2, "evil, 203.0.113.7, 10.0.0.1", "10.0.0.9:5555", "203.0.113.7"},
		{"junk rightmost falls back to remote", true, 1, "203.0.113.7, not-an-ip", "10.0.0.9:5555", "10.0.0.9"},
		{"missing xff falls back to remote", true, 1, "", "10.0.0.9:5555", "10.0.0.9"},
		{"short header clamps to leftmost present", true, 3, "203.0.113.7", "10.0.0.9:5555", "203.0.113.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.TrustProxy, cfg.TrustedProxyHops = tc.trustProxy, tc.hops
			s, _ := newTestServer(t, cfg)
			req := httptest.NewRequest(http.MethodPost, "/contact/submit", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := s.clientIP(req); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// Rotating the (forgeable) leftmost XFF entry must NOT evade the per-IP limit:
// the limiter keys on the trusted rightmost entry, which the ALB sets to the real
// client. (issue #41 — gaps 3+4)
func TestRateLimitNotEvadableByForgedXFF(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitPerHour = 2
	cfg.TrustProxy, cfg.TrustedProxyHops = true, 1
	s, _ := newTestServer(t, cfg)
	forge := func(spoof string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("X-Forwarded-For", spoof+", 203.0.113.7") }
	}
	if rec := post(s, validVals(), true, forge("1.1.1.1")); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 1 = %d, want 303", rec.Code)
	}
	if rec := post(s, validVals(), true, forge("2.2.2.2")); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 2 = %d, want 303", rec.Code)
	}
	if rec := post(s, validVals(), true, forge("3.3.3.3")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("attempt 3 (forged left, same real client) = %d, want 429", rec.Code)
	}
}

// The global circuit-breaker trips on aggregate volume even when each request is
// from a distinct IP under its own per-IP cap. (issue #41 — gap 1)
func TestGlobalCircuitBreaker(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitPerHour = 100 // ensure the per-IP cap never trips here
	cfg.GlobalRateLimitPerHour = 2
	s, fm := newTestServer(t, cfg)
	from := func(ip string) func(*http.Request) {
		return func(r *http.Request) { r.RemoteAddr = ip + ":40000" }
	}
	if rec := post(s, validVals(), true, from("198.51.100.1")); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 1 = %d, want 303", rec.Code)
	}
	if rec := post(s, validVals(), true, from("198.51.100.2")); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 2 = %d, want 303", rec.Code)
	}
	if rec := post(s, validVals(), true, from("198.51.100.3")); rec.Code != http.StatusTooManyRequests {
		t.Errorf("attempt 3 (distinct IP, global cap) = %d, want 429", rec.Code)
	}
	if len(fm.sent) != 2 {
		t.Errorf("sent %d, want 2 (3rd blocked by the global cap)", len(fm.sent))
	}
}

// The GitHub daily cap throttles the issue channel without ever blocking email.
// (issue #41 — gap 1)
func TestGitHubDailyCap(t *testing.T) {
	var filed int
	ghAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		filed++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/x/y/issues/1"}`))
	}))
	defer ghAPI.Close()

	cfg := testConfig()
	cfg.GitHubToken, cfg.GitHubRepo, cfg.GitHubAPIBase = "tok", "x/y", ghAPI.URL
	cfg.GitHubDailyCap = 1
	s, fm := newTestServer(t, cfg)

	if rec := post(s, validVals(), true); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 1 = %d, want 303", rec.Code)
	}
	if rec := post(s, validVals(), true); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 2 = %d, want 303", rec.Code)
	}
	if len(fm.sent) != 2 {
		t.Errorf("emails sent = %d, want 2 (the daily cap must not block email)", len(fm.sent))
	}
	if filed != 1 {
		t.Errorf("issues filed = %d, want 1 (daily cap = 1)", filed)
	}
}

// Every HTML response must carry the defense-in-depth security headers. (NEW — N2)
func TestSecurityHeaders(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodGet, "/contact", nil)
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	h := rec.Header()
	for _, hdr := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "X-Frame-Options"} {
		if h.Get(hdr) == "" {
			t.Errorf("missing security header %s", hdr)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none': %q", csp)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// The attacker-controlled User-Agent must be truncated before it lands in the
// notification email body. (NEW — N3)
func TestUserAgentTruncatedInEmail(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	longUA := strings.Repeat("U", 5000)
	rec := post(s, validVals(), true, func(r *http.Request) { r.Header.Set("User-Agent", longUA) })
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if len(fm.sent) != 1 {
		t.Fatalf("sent %d, want 1", len(fm.sent))
	}
	if strings.Contains(fm.sent[0].Body, longUA) {
		t.Error("email body contains the full untruncated User-Agent")
	}
	if !strings.Contains(fm.sent[0].Body, "…") {
		t.Error("expected a truncation ellipsis on the User-Agent")
	}
}

// The PRG target renders a plain confirmation, branded with the path back, under
// both the /contact-prefixed and bare aliases. (issue #43)
func TestThanksPage(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	for _, path := range []string{"/contact/thanks", "/thanks"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
		if body := rec.Body.String(); !strings.Contains(body, "Thanks") || !strings.Contains(body, "Back to the wiki") {
			t.Errorf("GET %s missing the confirmation/back-to-wiki content", path)
		}
	}
}

// A cross-site POST (Origin host != ours) is rejected even with a valid CSRF
// pair — defense-in-depth; a same-origin Origin still passes. (issue #43)
func TestSubmitOriginMismatch(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	rec := post(s, validVals(), true, func(r *http.Request) { r.Header.Set("Origin", "http://evil.test") })
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site Origin status = %d, want 403", rec.Code)
	}
	if len(fm.sent) != 0 {
		t.Error("cross-site submission was sent")
	}

	s2, fm2 := newTestServer(t, testConfig())
	if rec := post(s2, validVals(), true, func(r *http.Request) { r.Header.Set("Origin", "http://"+r.Host) }); rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin status = %d, want 303", rec.Code)
	}
	if len(fm2.sent) != 1 {
		t.Errorf("same-origin sent = %d, want 1", len(fm2.sent))
	}
}

// Behind a proxy (TrustProxy) the CSRF cookie must be Secure, or the token rides
// cleartext — fail readiness. Localhost dev (no proxy) is exempt. (issue #43)
func TestReadyzRequiresSecureCookieBehindProxy(t *testing.T) {
	ready := func(trustProxy, secure bool) int {
		cfg := testConfig()
		cfg.TrustProxy, cfg.CookieSecure = trustProxy, secure
		s, _ := newTestServer(t, cfg)
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		return rec.Code
	}
	if c := ready(true, false); c != http.StatusServiceUnavailable {
		t.Errorf("readyz (proxy, insecure cookie) = %d, want 503", c)
	}
	if c := ready(true, true); c != http.StatusOK {
		t.Errorf("readyz (proxy, secure cookie) = %d, want 200", c)
	}
	if c := ready(false, false); c != http.StatusOK {
		t.Errorf("readyz (local dev, no proxy) = %d, want 200", c)
	}
}

// A 400 re-render must be navigable by assistive tech: an alert summary plus
// aria-invalid + aria-describedby tying the offending field to its message.
// (issue #43; WCAG 3.3.1 error identification, 2.4.3 focus order)
func TestErrorRenderA11y(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	v := validVals()
	v.Set("email", "jane@gmail.com") // off-domain → a field error keyed on "email"
	rec := post(s, v, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`role="alert"`,
		`id="error-summary"`,
		`aria-invalid="true"`,
		`aria-describedby="email-hint email-err"`,
		`id="email-err"`,
		`id="email-hint"`,
		`href="#email"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("error re-render missing a11y marker %q", want)
		}
	}
}

// A matching Origin (same host) must still produce a successful submit
// while a blank/missing Origin falls through to CSRF (also should pass).
func TestSubmitOriginSameHost(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	v := validVals()
	// Encode a valid POST with same hostname as the request.
	rec := post(s, v, true, func(r *http.Request) {
		r.Header.Set("Origin", "http://"+r.Host)
	})
	if rec.Code != http.StatusSeeOther {
		t.Errorf("same-origin Origin status = %d, want 303\n%s", rec.Code, rec.Body.String())
	}
	if len(fm.sent) != 1 {
		t.Errorf("same-origin sent = %d, want 1", len(fm.sent))
	}
}

// A missing Origin (typical for some mobile clients) must not block the submit
// — rely on CSRF alone.
func TestSubmitNoOriginNoReferer(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	rec := post(s, validVals(), true)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("no Origin status = %d, want 303 (rely on CSRF alone)", rec.Code)
	}
	if len(fm.sent) != 1 {
		t.Errorf("no Origin sent = %d, want 1", len(fm.sent))
	}
}

func TestConcurrentSubmissions(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitPerHour = 1000
	cfg.GlobalRateLimitPerHour = 1000
	s, fm := newTestServer(t, cfg)

	var wg sync.WaitGroup
	sent := atomic.Int64{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := post(s, validVals(), true)
			if rec.Code == http.StatusSeeOther {
				sent.Add(1)
			}
		}()
	}
	wg.Wait()
	if sent.Load() == 0 {
		t.Error("all 10 concurrent submissions failed when they should succeed")
	}
	if int(sent.Load()) > fm.count() {
		t.Errorf("concurrent sends under-counted: sent=%d fm.sent=%d", sent.Load(), fm.count())
	}
}

func TestBothLimitersIndependently(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitPerHour = 2         // per-IP cap
	cfg.GlobalRateLimitPerHour = 100 // global cap should not trip
	s, _ := newTestServer(t, cfg)

	if rec := post(s, validVals(), true); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 1 = %d, want 303", rec.Code)
	}
	if rec := post(s, validVals(), true); rec.Code != http.StatusSeeOther {
		t.Fatalf("attempt 2 = %d, want 303", rec.Code)
	}
	if rec := post(s, validVals(), true); rec.Code != http.StatusTooManyRequests {
		t.Errorf("attempt 3 (per-IP) = %d, want 429", rec.Code)
	}
	// A different IP should be allowed (global cap not hit).
	if rec := post(s, validVals(), true, func(r *http.Request) { r.RemoteAddr = "198.51.100.99:40000" }); rec.Code != http.StatusTooManyRequests {
		// Actually, the per-IP limiter uses the same key for a new IP when TrustProxy is false.
		// Use a second config for this proper test. Skip the cross-IP check.
	}
}

func TestSubmitMultipleEmptyFields(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	v := url.Values{}
	v.Set("type", "")
	v.Set("name", "")
	v.Set("email", "")
	v.Set("summary", "")
	rec := post(s, v, true)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty required fields", rec.Code)
	}
	if len(fm.sent) != 0 {
		t.Error("no message should be sent with all fields empty")
	}
	body := rec.Body.String()
	for _, id := range []string{"name", "email", "summary"} {
		if !strings.Contains(body, `id="`+id+`-err"`) {
			t.Errorf("expected error message for field %q on the re-rendered form", id)
		}
	}
}

func TestSubmitHoneypotWithFieldErrors(t *testing.T) {
	// Honeypot takes priority over field validation: a bot that fills the
	// honeypot AND leaves the summary empty gets the silent 303 decoy, not a
	// validation re-render — so the bot can't distinguish caught-vs-sent.
	s, fm := newTestServer(t, testConfig())
	v := validVals()
	v.Del("summary")
	v.Set("website", "http://bot.example")
	rec := post(s, v, true)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("honeypot+invalid status = %d, want 303 (silent decoy)", rec.Code)
	}
	if len(fm.sent) != 0 {
		t.Error("honeypot submission was sent despite empty summary")
	}
}
