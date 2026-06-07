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
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ready"))
}

func (s *server) handleForm(w http.ResponseWriter, r *http.Request) {
	s.renderForm(w, http.StatusOK, s.newFormView(w, r, map[string]string{}, map[string]string{}, ""))
}

func (s *server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)

	// Cap the body before parsing so an oversized POST can't buffer megabytes in
	// memory ahead of any validation (issue #41). MaxBytesReader makes ParseForm
	// fail with *http.MaxBytesError once the cap is hit.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			s.log.Warn("contact submission too large", "ip", ip, "limit", maxBodyBytes)
			s.renderForm(w, http.StatusRequestEntityTooLarge,
				s.newFormView(w, r, map[string]string{}, map[string]string{}, "That submission was too large. Please shorten it and try again."))
			return
		}
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

	if !s.limiter.allow(ip, s.now()) {
		s.log.Warn("contact rate limited (per-ip)", "ip", ip)
		s.renderForm(w, http.StatusTooManyRequests, s.newFormView(w, r, values, map[string]string{},
			"Too many submissions from your connection. Please wait a few minutes and try again."))
		return
	}

	// Aggregate circuit-breaker: even if no single IP is over its cap, a flood
	// spread across many IPs is throttled before it can reach the mailbox. Trips
	// fail-safe (protects the inbox) and auto-resets as the sliding window clears.
	if !s.globalLimiter.allow("global", s.now()) {
		s.log.Warn("contact rate limited (global circuit-breaker)", "ip", ip)
		s.renderForm(w, http.StatusTooManyRequests, s.newFormView(w, r, values, map[string]string{},
			"We're receiving an unusually high number of submissions right now. Please try again shortly."))
		return
	}

	if !s.verifyCSRF(r) {
		s.renderForm(w, http.StatusForbidden, s.newFormView(w, r, values, map[string]string{},
			"Your session expired. Please review and submit again."))
		return
	}

	// Honeypot: a hidden field real users never fill. Pretend success so bots
	// don't learn they were caught; send nothing.
	if strings.TrimSpace(r.PostFormValue(honeypotField)) != "" {
		s.log.Warn("contact honeypot tripped", "ip", ip)
		s.renderSuccess(w, s.newSuccessView(r))
		return
	}

	sub, ferrs := newSubmission(in, s.cfg, s.now())
	if len(ferrs) > 0 {
		errs := make(map[string]string, len(ferrs))
		for _, e := range ferrs {
			errs[e.Field] = e.Message
		}
		s.renderForm(w, http.StatusBadRequest, s.newFormView(w, r, values, errs, ""))
		return
	}

	if !s.cfg.mailConfigured() {
		s.log.Error("contact submit but mail not configured")
		s.renderForm(w, http.StatusServiceUnavailable, s.newFormView(w, r, values, map[string]string{},
			"Email delivery isn't set up yet. Please email "+s.cfg.Recipient+" directly for now."))
		return
	}

	sub.SourceIP = ip // already a validated IP or RemoteAddr host (see clientIP)
	sub.UserAgent = truncateRunes(r.UserAgent(), maxUserAgentRunes)

	// Email is the gating channel — its failure fails the submission.
	sendCtx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := s.mailer.Send(sendCtx, s.buildMessage(sub)); err != nil {
		s.log.Error("contact email send failed", "err", err, "ip", ip, "kind", string(sub.Kind))
		s.renderForm(w, http.StatusBadGateway, s.newFormView(w, r, values, map[string]string{},
			"Sorry — we couldn't send your message right now. Please try again in a few minutes."))
		return
	}
	s.log.Info("contact email sent", "kind", string(sub.Kind), "from", sub.Email)

	// GitHub issue is best-effort: a GitHub outage must never block feedback.
	// Filed for internal tracking only — its URL is never surfaced to the
	// submitter (issue #36), so we don't thread it into the success view. A
	// tighter daily cap protects the tracker from a flood that the hourly caps
	// would still let through over a day; the email above is unaffected (issue #41).
	if s.cfg.githubConfigured() {
		if !s.ghLimiter.allow("github", s.now()) {
			s.log.Warn("contact github daily cap reached; issue not filed (email still sent)", "ip", ip)
		} else {
			ghCtx, ghCancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer ghCancel()
			if u, err := s.gh.CreateIssue(ghCtx, s.buildIssue(sub)); err != nil {
				s.log.Error("contact github issue failed (non-fatal)", "err", err)
			} else {
				s.log.Info("contact github issue filed", "url", u)
			}
		}
	}

	s.renderSuccess(w, s.newSuccessView(r))
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

// newSuccessView builds the success view with the brand fields filled in. Both
// success paths (honeypot decoy + real send) go through here so neither can ship
// the masthead without the "home"/"back to the wiki" links.
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
