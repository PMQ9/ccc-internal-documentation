package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
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
	honeypotField = "website" // a field humans never see; bots fill it
)

type server struct {
	cfg     *Config
	mailer  Mailer
	gh      *githubClient
	html    *htmltemplate.Template
	email   *texttemplate.Template
	limiter *rateLimiter
	log     *slog.Logger
	now     func() time.Time // injectable for tests
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
		cfg:     cfg,
		mailer:  mailer,
		gh:      newGitHubClient(cfg),
		html:    html,
		email:   email,
		limiter: newRateLimiter(cfg.RateLimitPerHour, time.Hour),
		log:     log,
		now:     time.Now,
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
	return mux
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

func (s *server) handleForm(w http.ResponseWriter, _ *http.Request) {
	s.renderForm(w, http.StatusOK, s.newFormView(w, map[string]string{}, map[string]string{}, ""))
}

func (s *server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)

	if err := r.ParseForm(); err != nil {
		s.renderForm(w, http.StatusBadRequest,
			s.newFormView(w, map[string]string{}, map[string]string{}, "Could not read the form. Please try again."))
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
		s.log.Warn("contact rate limited", "ip", ip)
		s.renderForm(w, http.StatusTooManyRequests, s.newFormView(w, values, map[string]string{},
			"Too many submissions from your connection. Please wait a few minutes and try again."))
		return
	}

	if !s.verifyCSRF(r) {
		s.renderForm(w, http.StatusForbidden, s.newFormView(w, values, map[string]string{},
			"Your session expired. Please review and submit again."))
		return
	}

	// Honeypot: a hidden field real users never fill. Pretend success so bots
	// don't learn they were caught; send nothing.
	if strings.TrimSpace(r.PostFormValue(honeypotField)) != "" {
		s.log.Warn("contact honeypot tripped", "ip", ip)
		s.renderSuccess(w, s.newSuccessView(""))
		return
	}

	sub, ferrs := newSubmission(in, s.cfg, s.now())
	if len(ferrs) > 0 {
		errs := make(map[string]string, len(ferrs))
		for _, e := range ferrs {
			errs[e.Field] = e.Message
		}
		s.renderForm(w, http.StatusBadRequest, s.newFormView(w, values, errs, ""))
		return
	}

	if !s.cfg.mailConfigured() {
		s.log.Error("contact submit but mail not configured")
		s.renderForm(w, http.StatusServiceUnavailable, s.newFormView(w, values, map[string]string{},
			"Email delivery isn't set up yet. Please email "+s.cfg.Recipient+" directly for now."))
		return
	}

	sub.SourceIP = ip
	sub.UserAgent = r.UserAgent()

	// Email is the gating channel — its failure fails the submission.
	sendCtx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := s.mailer.Send(sendCtx, s.buildMessage(sub)); err != nil {
		s.log.Error("contact email send failed", "err", err, "ip", ip, "kind", string(sub.Kind))
		s.renderForm(w, http.StatusBadGateway, s.newFormView(w, values, map[string]string{},
			"Sorry — we couldn't send your message right now. Please try again in a few minutes."))
		return
	}
	s.log.Info("contact email sent", "kind", string(sub.Kind), "from", sub.Email)

	// GitHub issue is best-effort: a GitHub outage must never block feedback.
	issueURL := ""
	if s.cfg.githubConfigured() {
		ghCtx, ghCancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer ghCancel()
		if u, err := s.gh.CreateIssue(ghCtx, s.buildIssue(sub)); err != nil {
			s.log.Error("contact github issue failed (non-fatal)", "err", err)
		} else {
			issueURL = u
			s.log.Info("contact github issue filed", "url", u)
		}
	}

	s.renderSuccess(w, s.newSuccessView(issueURL))
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

func (s *server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- views + rendering ----------------------------------------------------

type formView struct {
	WikiName      string
	WikiURL       string
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
	WikiName  string
	WikiURL   string
	Recipient string
	IssueURL  string
}

func (s *server) newFormView(w http.ResponseWriter, values, errs map[string]string, general string) formView {
	return formView{
		WikiName:      s.cfg.WikiName,
		WikiURL:       s.cfg.WikiURL,
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
func (s *server) newSuccessView(issueURL string) successView {
	return successView{
		WikiName:  s.cfg.WikiName,
		WikiURL:   s.cfg.WikiURL,
		Recipient: s.cfg.Recipient,
		IssueURL:  issueURL,
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
