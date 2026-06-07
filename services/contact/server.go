package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	texttemplate "text/template"
	"time"
)

//go:embed templates
var templatesFS embed.FS

const (
	csrfCookie    = "ccc_csrf"
	csrfField     = "_csrf"
	honeypotField = "website"          // a field humans never see; bots fill it
	themeCookie   = "ccc-color-scheme" // cross-origin theme bridge from the wiki (issue #39)

	// maxBodyBytes caps the submit request body before it is parsed, so an
	// oversized POST can't buffer megabytes in memory ahead of validation. 64 KiB
	// is ~6x the sum of every field cap (maxName+maxSummary+maxDetails+maxPage+email),
	// leaving generous room for encoding overhead. (issue #41)
	maxBodyBytes = 64 << 10
	// maxUserAgentRunes bounds the attacker-controlled User-Agent before it lands
	// verbatim in the notification email body. (issue #41)
	maxUserAgentRunes = 400

	// thanksPath is the GET target of the post-submit PRG redirect: a refresh or
	// Back lands here (a plain confirmation) instead of re-POSTing. (issue #43)
	thanksPath = "/contact/thanks"
)

// securityHeaders adds defense-in-depth response headers to every response.
// html/template already auto-escapes and the form is VPN-gated, so these are
// belt-and-suspenders: the CSP confines the page to its own (inline) assets, the
// data: favicon, and a same-origin form action; frame-ancestors/X-Frame block
// framing (clickjacking); nosniff stops MIME guessing; no-referrer keeps the form
// URL out of the wiki's request logs. 'unsafe-inline' is required while the theme
// script + page styles are inline blocks (a CSP nonce is a tracked follow-up).
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
		"script-src 'self' 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY") // legacy backstop for frame-ancestors
		next.ServeHTTP(w, r)
	})
}

// themeClass maps the wiki's host-scoped ccc-color-scheme cookie to the initial
// <html> class so the page paints in the chosen mode with no light-then-dark
// flash. The cookie is authoritative across origins; its absence (or any value
// other than dark/light) means "follow the OS", matching the wiki's guest
// behavior — so we add no class and let CSS resolve it. (Issue #39.)
//
//	dark  -> "dark-mode" (force dark)   light -> "ccc-light" (force light)
func themeClass(r *http.Request) string {
	if c, err := r.Cookie(themeCookie); err == nil {
		switch c.Value {
		case "dark":
			return "dark-mode"
		case "light":
			return "ccc-light"
		}
	}
	return ""
}

type server struct {
	cfg           *Config
	mailer        Mailer
	gh            *githubClient
	html          *htmltemplate.Template
	email         *texttemplate.Template
	limiter       *rateLimiter // per source IP
	globalLimiter *rateLimiter // aggregate circuit-breaker across all IPs
	ghLimiter     *rateLimiter // GitHub-issue channel, per 24h
	log           *slog.Logger
	now           func() time.Time // injectable for tests
}

func newServer(cfg *Config, log *slog.Logger) (*server, error) {
	html, err := htmltemplate.ParseFS(templatesFS, "templates/layout.html", "templates/form.html", "templates/success.html")
	if err != nil {
		return nil, fmt.Errorf("parse html templates: %w", err)
	}
	email, err := texttemplate.ParseFS(templatesFS, "templates/email.txt.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse email template: %w", err)
	}
	mailer, err := newMailer(cfg)
	if err != nil {
		return nil, err
	}
	return &server{
		cfg:           cfg,
		mailer:        mailer,
		gh:            newGitHubClient(cfg),
		html:          html,
		email:         email,
		limiter:       newRateLimiter(cfg.RateLimitPerHour, time.Hour),
		globalLimiter: newRateLimiter(cfg.GlobalRateLimitPerHour, time.Hour),
		ghLimiter:     newRateLimiter(cfg.GitHubDailyCap, 24*time.Hour),
		log:           log,
		now:           time.Now,
	}, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	// The contact UI lives under /contact* so the prod ALB can route it on one
	// path rule to the same VPN-gated domain; the bare aliases serve the local
	// direct-port case and container health checks.
	mux.HandleFunc("GET /contact", s.handleForm)
	mux.HandleFunc("POST /contact/submit", s.handleSubmit)
	mux.HandleFunc("GET /contact/healthz", s.handleHealthz)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /contact/readyz", s.handleReadyz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /contact/thanks", s.handleThanks)
	mux.HandleFunc("GET /thanks", s.handleThanks)
	mux.HandleFunc("GET /{$}", s.handleRoot)
	return securityHeaders(mux)
}

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/contact", http.StatusSeeOther)
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if !s.cfg.mailConfigured() {
		http.Error(w, "mail not configured", http.StatusServiceUnavailable)
		return
	}
	// Behind a proxy (TrustProxy is the "we sit behind the ALB, TLS terminates
	// upstream" signal) the CSRF cookie MUST be Secure — otherwise the token rides
	// cleartext and a network adversary can pair cookie+body. Fail readiness rather
	// than serve an insecure cookie where the connection should be HTTPS. Not
	// enforced on localhost dev (TrustProxy false, plain HTTP), where a Secure
	// cookie would simply never be sent back. (issue #43)
	if s.cfg.TrustProxy && !s.cfg.CookieSecure {
		http.Error(w, "insecure cookie: set CONTACT_SECURE_COOKIE=true behind a proxy", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ready"))
}

func (s *server) handleForm(w http.ResponseWriter, r *http.Request) {
	s.renderForm(w, http.StatusOK, s.newFormView(w, r, map[string]string{}, map[string]string{}, ""))
}

// handleThanks renders the post-submit confirmation. It is the GET target of the
// PRG redirect from a successful submit (and the honeypot decoy), so a refresh or
// Back never re-POSTs — no duplicate email/issue. It holds no per-submission state
// (none is needed); a direct GET just shows a generic thank-you. (issue #43)
func (s *server) handleThanks(w http.ResponseWriter, r *http.Request) {
	s.renderSuccess(w, s.newSuccessView(r))
}

func (s *server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)

	// One wide, structured event per request — the single per-submission record
	// operators count for a RED-style view (grep '"msg":"contact request"' | jq
	// .outcome | sort | uniq -c). Std-lib slog only; full Prometheus would break
	// the no-dependencies rule. The GitHub side-channel keeps its own detail lines.
	// (issue #43 — observability)
	outcome := "sent"
	github := "disabled"
	var sendMS int64
	var sendErr error
	defer func() {
		lvl := slog.LevelInfo
		switch outcome {
		case "rate_limited_ip", "rate_limited_global", "csrf_fail", "origin_mismatch", "invalid", "too_large", "unreadable":
			lvl = slog.LevelWarn
		case "send_failed", "mail_unconfigured":
			lvl = slog.LevelError
		}
		attrs := []any{"outcome", outcome, "ip", ip, "send_ms", sendMS, "github", github}
		if sendErr != nil {
			attrs = append(attrs, "err", sendErr)
		}
		s.log.Log(context.Background(), lvl, "contact request", attrs...)
	}()

	// Cap the body before parsing so an oversized POST can't buffer megabytes in
	// memory ahead of any validation (issue #41). MaxBytesReader makes ParseForm
	// fail with *http.MaxBytesError once the cap is hit.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			outcome = "too_large"
			s.renderForm(w, http.StatusRequestEntityTooLarge,
				s.newFormView(w, r, map[string]string{}, map[string]string{}, "That submission was too large. Please shorten it and try again."))
			return
		}
		outcome = "unreadable"
		s.renderForm(w, http.StatusBadRequest,
			s.newFormView(w, r, map[string]string{}, map[string]string{}, "Could not read the form. Please try again."))
		return
	}

	in := formInput{
		Kind:    r.PostFormValue("type"),
		Name:    r.PostFormValue("name"),
		Email:   r.PostFormValue("email"),
		Page:    r.PostFormValue("page"),
		Summary: r.PostFormValue("summary"),
		Details: r.PostFormValue("details"),
	}
	values := map[string]string{
		"type": in.Kind, "name": in.Name, "email": in.Email,
		"page": in.Page, "summary": in.Summary, "details": in.Details,
	}

	// Per-IP limiter caps SUBMIT ATTEMPTS (every parseable POST from this source),
	// not just deliveries — so a single client hammering invalid/junk posts is
	// still throttled. It is charged early, ahead of validation, for exactly that
	// reason. The aggregate breaker (which protects mailbox volume) is charged
	// later, only on would-be-sends — see below. (issue #43)
	if !s.limiter.allow(ip, s.now()) {
		outcome = "rate_limited_ip"
		s.renderForm(w, http.StatusTooManyRequests, s.newFormView(w, r, values, map[string]string{},
			"Too many submissions from your connection. Please wait a few minutes and try again."))
		return
	}

	// Reject an obvious cross-site POST before more work — defense-in-depth
	// alongside the double-submit CSRF token (see sameOrigin). (issue #43)
	if !s.sameOrigin(r) {
		outcome = "origin_mismatch"
		s.renderForm(w, http.StatusForbidden, s.newFormView(w, r, values, map[string]string{},
			"Your session expired. Please review and submit again."))
		return
	}

	if !s.verifyCSRF(r) {
		outcome = "csrf_fail"
		s.renderForm(w, http.StatusForbidden, s.newFormView(w, r, values, map[string]string{},
			"Your session expired. Please review and submit again."))
		return
	}

	// Honeypot: a hidden field real users never fill. Redirect to the same thanks
	// page a real send uses (PRG), so a bot can't tell it was caught; send nothing.
	if strings.TrimSpace(r.PostFormValue(honeypotField)) != "" {
		outcome = "honeypot"
		http.Redirect(w, r, thanksPath, http.StatusSeeOther)
		return
	}

	sub, ferrs := newSubmission(in, s.cfg, s.now())
	if len(ferrs) > 0 {
		outcome = "invalid"
		errs := make(map[string]string, len(ferrs))
		for _, e := range ferrs {
			errs[e.Field] = e.Message
		}
		s.renderForm(w, http.StatusBadRequest, s.newFormView(w, r, values, errs, ""))
		return
	}

	if !s.cfg.mailConfigured() {
		outcome = "mail_unconfigured"
		s.renderForm(w, http.StatusServiceUnavailable, s.newFormView(w, r, values, map[string]string{},
			"Email delivery isn't set up yet. Please email "+s.cfg.Recipient+" directly for now."))
		return
	}

	// Aggregate circuit-breaker counts WOULD-BE-SENDS: charged here, only after a
	// submission has cleared CSRF + honeypot + validation, so a flood of junk that
	// never reaches the mailbox can't trip it and fail-safe-429 legitimate users.
	// Trips fail-safe (protects the inbox) and auto-resets as the window clears.
	// (issue #43 — was previously charged on raw attempts.)
	if !s.globalLimiter.allow("global", s.now()) {
		outcome = "rate_limited_global"
		s.renderForm(w, http.StatusTooManyRequests, s.newFormView(w, r, values, map[string]string{},
			"We're receiving an unusually high number of submissions right now. Please try again shortly."))
		return
	}

	sub.SourceIP = ip // already a validated IP or RemoteAddr host (see clientIP)
	sub.UserAgent = truncateRunes(r.UserAgent(), maxUserAgentRunes)

	// Decouple the send from the request context: a client disconnect or refresh
	// must NOT cancel an in-flight send the relay may already have accepted — that
	// turns one delivered email into a perceived failure and a retry (duplicate).
	// Email is the gating channel — its failure fails the submission. Delivery is
	// at-least-once; see the runbook. (issue #43)
	sendCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	t0 := time.Now()
	sendErr = s.mailer.Send(sendCtx, s.buildMessage(sub))
	sendMS = time.Since(t0).Milliseconds()
	if sendErr != nil {
		outcome = "send_failed"
		s.renderForm(w, http.StatusBadGateway, s.newFormView(w, r, values, map[string]string{},
			"Sorry — we couldn't send your message right now. Please try again in a few minutes."))
		return
	}

	// GitHub issue is best-effort: a GitHub outage must never block feedback.
	// Filed for internal tracking only — its URL is never surfaced to the
	// submitter (issue #36). A tighter daily cap protects the tracker from a flood
	// the hourly caps would still let through over a day; email is unaffected.
	if s.cfg.githubConfigured() {
		if !s.ghLimiter.allow("github", s.now()) {
			github = "capped"
			s.log.Warn("contact github daily cap reached; issue not filed (email still sent)", "ip", ip)
		} else {
			ghCtx, ghCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer ghCancel()
			if u, err := s.gh.CreateIssue(ghCtx, s.buildIssue(sub)); err != nil {
				github = "failed"
				s.log.Error("contact github issue failed (non-fatal)", "err", err)
			} else {
				github = "filed"
				s.log.Info("contact github issue filed", "url", u)
			}
		}
	}

	// PRG: redirect to a GET so a refresh/Back doesn't re-POST (no duplicate
	// email/issue). The thanks page reads the theme cookie + brand from the GET.
	http.Redirect(w, r, thanksPath, http.StatusSeeOther)
}

// ---- CSRF (stateless double-submit cookie) --------------------------------

func (s *server) issueCSRF(w http.ResponseWriter) string {
	tok, err := randomToken(16)
	if err != nil {
		// Fail closed: an empty token can't match on submit, prompting a reload.
		s.log.Error("csrf token generation failed", "err", err)
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	return tok
}

func (s *server) verifyCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookie)
	if err != nil || c.Value == "" {
		return false
	}
	posted := r.PostFormValue(csrfField)
	if posted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(posted)) == 1
}

// sameOrigin is a defense-in-depth cross-site check: when the browser tells us
// where the POST came from (Origin, else Referer), a host that doesn't match
// ours is a forgery and we reject it. Absent headers fall through — the
// double-submit CSRF token stays the primary gate, so this never blocks a
// same-origin submit from a client that omits the header. (issue #43)
func (s *server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true // no signal — rely on the CSRF token
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// clientIP resolves the submitter's IP for rate-limiting and audit. Behind a
// trusted proxy (the ALB), X-Forwarded-For is "client, proxy1, proxy2, …" where
// each hop appends the address it received from. The rightmost entries are added
// by infrastructure we control, so we count TrustedProxyHops in from the RIGHT to
// find the address the outermost trusted proxy actually saw. Trusting the LEFTmost
// entry (the old behavior) let a client forge it — evading the per-IP limit,
// exploding the limiter map with junk keys, and spoofing the audit IP in emails.
// The chosen value must parse as an IP or we fall back to RemoteAddr, so garbage
// never becomes a rate-limit key. (issue #41)
func (s *server) clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if !s.cfg.TrustProxy {
		return remote
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remote
	}
	parts := strings.Split(xff, ",")
	// hops counts from the right: 1 -> the last entry (what the proxy nearest us
	// recorded). Clamp so a header shorter than the hop count yields its leftmost
	// present entry rather than indexing out of range.
	idx := len(parts) - s.cfg.TrustedProxyHops
	if idx < 0 {
		idx = 0
	}
	if cand := strings.TrimSpace(parts[idx]); net.ParseIP(cand) != nil {
		return cand
	}
	return remote
}

// ---- views + rendering ----------------------------------------------------

type formView struct {
	WikiName      string
	WikiURL       string
	ThemeClass    string // initial <html> class from the theme cookie (issue #39)
	CSRFToken     string
	Honeypot      string
	Kinds         []kindOption
	Values        map[string]string
	Errors        map[string]string
	GeneralError  string
	Ready         bool
	Recipient     string
	AllowedDomain string
}

type successView struct {
	WikiName   string
	WikiURL    string
	ThemeClass string // initial <html> class from the theme cookie (issue #39)
	Recipient  string
}

func (s *server) newFormView(w http.ResponseWriter, r *http.Request, values, errs map[string]string, general string) formView {
	return formView{
		WikiName:      s.cfg.WikiName,
		WikiURL:       s.cfg.WikiURL,
		ThemeClass:    themeClass(r),
		CSRFToken:     s.issueCSRF(w),
		Honeypot:      honeypotField,
		Kinds:         kindOptions(),
		Values:        values,
		Errors:        errs,
		GeneralError:  general,
		Ready:         s.cfg.mailConfigured(),
		Recipient:     s.cfg.Recipient,
		AllowedDomain: s.cfg.AllowedDomain,
	}
}

func (s *server) renderForm(w http.ResponseWriter, status int, v formView) {
	s.renderHTML(w, status, "form.html", v)
}

// newSuccessView builds the success view with the brand fields filled in. The
// single thanks page (the PRG target both the honeypot decoy and a real send
// redirect to) renders through here, so it can't ship the masthead without the
// "home"/"back to the wiki" links.
func (s *server) newSuccessView(r *http.Request) successView {
	return successView{
		WikiName:   s.cfg.WikiName,
		WikiURL:    s.cfg.WikiURL,
		ThemeClass: themeClass(r),
		Recipient:  s.cfg.Recipient,
	}
}

func (s *server) renderSuccess(w http.ResponseWriter, v successView) {
	s.renderHTML(w, http.StatusOK, "success.html", v)
}

func (s *server) renderHTML(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.html.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("template render failed", "template", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *server) buildMessage(sub *Submission) *Message {
	var body bytes.Buffer
	if err := s.email.ExecuteTemplate(&body, "email.txt.tmpl", sub.emailView(s.cfg.WikiName)); err != nil {
		s.log.Error("render email body failed", "err", err)
	}
	return &Message{
		FromName: s.cfg.FromName,
		FromAddr: s.cfg.FromAddress,
		To:       s.cfg.Recipient,
		ReplyTo:  sub.Email,
		Subject:  sub.subject(s.cfg.WikiName),
		Body:     body.String(),
		Headers:  map[string]string{"X-CCC-Contact-Type": string(sub.Kind)},
	}
}

func (s *server) buildIssue(sub *Submission) issue {
	in := issue{Title: sub.subject(s.cfg.WikiName), Body: sub.issueBody(s.cfg.WikiName)}
	if lbl := gitHubLabel[sub.Kind]; lbl != "" {
		in.Labels = []string{lbl}
	}
	return in
}
