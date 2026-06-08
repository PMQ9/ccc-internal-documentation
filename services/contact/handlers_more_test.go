package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Unit coverage for the request-guard helpers (themeClass, sameOrigin, verifyCSRF)
// and a few handler invariants that the end-to-end server tests exercise only
// indirectly: the unreadable-body branch, the abuse-limiter charge ORDER, and the
// CSRF cookie's security attributes.

func TestThemeClassDirect(t *testing.T) {
	cases := map[string]string{
		"dark":   "dark-mode",
		"light":  "ccc-light",
		"":       "", // no cookie => follow OS
		"purple": "", // unknown value => follow OS
	}
	for cookie, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/contact", nil)
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: themeCookie, Value: cookie})
		}
		if got := themeClass(req); got != want {
			t.Errorf("themeClass(cookie=%q) = %q, want %q", cookie, got, want)
		}
	}
}

func TestSameOriginDirect(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	// httptest.NewRequest defaults the request Host to "example.com".
	cases := []struct {
		name            string
		origin, referer string
		want            bool
	}{
		{"matching Origin", "http://example.com", "", true},
		{"mismatched Origin", "http://evil.test", "", false},
		{"Referer fallback matches", "", "http://example.com/page", true},
		{"Referer fallback mismatches", "", "http://evil.test/page", false},
		{"no signal falls through to CSRF", "", "", true},
		{"malformed Origin rejected", "::::", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/contact/submit", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			if got := s.sameOrigin(req); got != tc.want {
				t.Errorf("sameOrigin = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestVerifyCSRFDirect(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	mk := func(cookie, posted string) *http.Request {
		v := url.Values{}
		if posted != "" {
			v.Set(csrfField, posted)
		}
		req := httptest.NewRequest(http.MethodPost, "/contact/submit", strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: csrfCookie, Value: cookie})
		}
		return req
	}
	cases := []struct {
		name           string
		cookie, posted string
		want           bool
	}{
		{"matching pair passes", "tok-abc", "tok-abc", true},
		{"missing cookie fails", "", "tok-abc", false},
		{"missing posted fails", "tok-abc", "", false},
		{"mismatch fails", "tok-abc", "tok-xyz", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.verifyCSRF(mk(tc.cookie, tc.posted)); got != tc.want {
				t.Errorf("verifyCSRF = %v, want %v", got, tc.want)
			}
		})
	}
}

// A body that ParseForm can't decode (invalid percent-escape) is "unreadable":
// a clean 400, no mail sent — distinct from the 413 oversized-body branch.
func TestSubmitUnreadableBody(t *testing.T) {
	s, fm := newTestServer(t, testConfig())
	req := httptest.NewRequest(http.MethodPost, "/contact/submit", strings.NewReader("name=%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an unparseable body", rec.Code)
	}
	if len(fm.sent) != 0 {
		t.Error("nothing should be sent for an unreadable body")
	}
}

// The global circuit-breaker is charged only on a would-be-SEND (after CSRF +
// honeypot + validation). A flood of INVALID posts must therefore NOT consume the
// global budget — otherwise junk could fail-safe-429 a legitimate user. (issue #43)
func TestGlobalLimiterNotChargedByInvalidSubmissions(t *testing.T) {
	cfg := testConfig()
	cfg.RateLimitPerHour = 100     // keep the per-IP cap out of the way
	cfg.GlobalRateLimitPerHour = 1 // a single would-be-send is allowed
	s, fm := newTestServer(t, cfg)

	// Three invalid (off-domain) posts: each passes CSRF, fails validation (400),
	// and must NOT touch the global limiter.
	for i := 0; i < 3; i++ {
		v := validVals()
		v.Set("email", "nope@gmail.com")
		if rec := post(s, v, true); rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid attempt %d = %d, want 400", i+1, rec.Code)
		}
	}
	// The first VALID submission must still go through — proof the budget wasn't
	// burned by the junk above.
	if rec := post(s, validVals(), true); rec.Code != http.StatusSeeOther {
		t.Errorf("valid submit after junk = %d, want 303 (global budget intact)", rec.Code)
	}
	if len(fm.sent) != 1 {
		t.Errorf("sent = %d, want 1 (only the valid submission)", len(fm.sent))
	}
}

// The CSRF cookie must be HttpOnly + SameSite=Strict always, and Secure exactly
// when CookieSecure is set (behind the HTTPS-terminating ALB).
func TestCSRFCookieAttributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		cfg := testConfig()
		cfg.CookieSecure = secure
		s, _ := newTestServer(t, cfg)
		req := httptest.NewRequest(http.MethodGet, "/contact", nil)
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)

		var c *http.Cookie
		for _, ck := range rec.Result().Cookies() {
			if ck.Name == csrfCookie {
				c = ck
			}
		}
		if c == nil {
			t.Fatalf("secure=%v: no CSRF cookie set", secure)
		}
		if !c.HttpOnly {
			t.Errorf("secure=%v: CSRF cookie must be HttpOnly", secure)
		}
		if c.SameSite != http.SameSiteStrictMode {
			t.Errorf("secure=%v: CSRF cookie SameSite = %v, want Strict", secure, c.SameSite)
		}
		if c.Secure != secure {
			t.Errorf("secure=%v: CSRF cookie Secure = %v, want %v", secure, c.Secure, secure)
		}
	}
}

// Every closed-set kind maps to exactly one GitHub label on the built issue.
func TestBuildIssueLabelPerKind(t *testing.T) {
	s, _ := newTestServer(t, testConfig())
	for _, k := range kindOrder {
		sub := &Submission{Kind: k, Name: "J", Email: "j@v.edu", Summary: "s"}
		got := s.buildIssue(sub).Labels
		want := gitHubLabel[k]
		if len(got) != 1 || got[0] != want {
			t.Errorf("buildIssue(kind=%q).Labels = %v, want [%q]", k, got, want)
		}
	}
}

func TestMailConfiguredUnknownTransport(t *testing.T) {
	c := &Config{Transport: "carrier-pigeon", Recipient: "a@b.org"}
	if c.mailConfigured() {
		t.Error("an unknown transport must never report mail-configured")
	}
}
