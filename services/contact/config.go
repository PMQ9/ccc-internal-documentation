package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the full runtime configuration, read once from the environment at
// startup (12-factor). Secrets (SMTP password, Graph client secret, GitHub PAT)
// arrive the same way: deploy/local/.env locally, AWS Secrets Manager in prod
// (see docs/runbooks/contact-form.md). Nothing is read from the environment
// deeper in the call tree — this struct is passed down.
type Config struct {
	// HTTP
	Listen string // bind address, e.g. ":8080"

	// Identity / recipient
	Recipient   string // fixed To: — never client-supplied (anti-relay), e.g. cccadmin@vanderbilt.edu
	FromAddress string // From: — a sender you can authenticate as, e.g. ccc.vanderbilt.admin@proton.me
	FromName    string // From: display name
	WikiName    string // shown in the form heading + the subject prefix
	WikiURL     string // base URL of the wiki this form belongs to; powers the masthead
	// logo "home" link + the "Back to the wiki" link. Empty = render the brand
	// non-interactively (no link). This is the REVERSE of CONTACT_URL (which links
	// the wiki header AT this form); see deploy/local/.env.example.

	// Approved-sender policy (the "whitelist"). With no list and no domain, any
	// well-formed address is accepted (still behind VPN + login-gated link).
	AllowedDomain  string   // required submitter domain, e.g. "vanderbilt.edu" ("" = any)
	AllowedSenders []string // optional exact-address allowlist (overrides AllowedDomain)

	// Mail transport: "agentmail" (REST via an agentmail.to inbox — recommended,
	// sender-authenticated, no IT), "smtp" (Brevo/Gmail/SES/Proton-Bridge — the
	// default), or "graph" (Microsoft 365 send-as, needs an app registration).
	Transport string

	// SMTP
	SMTPHost       string
	SMTPPort       int
	SMTPUsername   string
	SMTPPassword   string
	SMTPEncryption string // "starttls" (587) | "tls" (465) | "none" (test sink)

	// Microsoft Graph (optional alternative transport)
	GraphTenantID     string
	GraphClientID     string
	GraphClientSecret string
	GraphSenderUPN    string // mailbox to send as; defaults to FromAddress

	// AgentMail (https://agentmail.to) — REST email API for agents. No SMTP, no
	// DMARC setup; sends from the inbox below (From is fixed to it), API-key auth.
	AgentMailAPIKey  string
	AgentMailInbox   string // inbox address, e.g. ccc-3278@agentmail.to
	AgentMailAPIBase string // default https://api.agentmail.to; overridden in tests

	// GitHub issue intake (optional; empty token disables this channel entirely).
	GitHubToken   string
	GitHubRepo    string // "owner/repo"
	GitHubAPIBase string // default https://api.github.com; overridden in tests

	// Abuse controls + edge.
	RateLimitPerHour       int  // per source IP
	GlobalRateLimitPerHour int  // aggregate circuit-breaker across all IPs
	GitHubDailyCap         int  // max issues filed per 24h (email is unaffected)
	TrustProxy             bool // honor X-Forwarded-For (true behind the ALB/a proxy)
	TrustedProxyHops       int  // # of trusted proxies appending XFF (client IP = that-many from the right)
	CookieSecure           bool // set the CSRF cookie Secure flag (true under HTTPS)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		return strings.EqualFold(v, "true") || v == "1"
	}
	return def
}

// Load reads configuration from the environment and validates the parts that
// must be coherent to even start serving. Mail/GitHub readiness is checked
// lazily (see mailConfigured/githubConfigured) so the container can run in any
// stack — including before credentials are filled in — and report 503 on submit
// rather than crash-looping the whole compose project.
func Load() (*Config, error) {
	c := &Config{
		Listen:                 env("CONTACT_LISTEN", ":8080"),
		Recipient:              os.Getenv("CONTACT_RECIPIENT"),
		FromAddress:            os.Getenv("MAIL_FROM_ADDRESS"),
		FromName:               env("MAIL_FROM_NAME", "CCC Wiki Contact"),
		WikiName:               env("CONTACT_WIKI_NAME", "CCC Wiki"),
		WikiURL:                strings.TrimRight(env("CONTACT_WIKI_URL", ""), "/"),
		AllowedDomain:          os.Getenv("CONTACT_ALLOWED_EMAIL_DOMAIN"),
		Transport:              env("MAIL_TRANSPORT", "smtp"),
		SMTPHost:               os.Getenv("MAIL_HOST"),
		SMTPPort:               envInt("MAIL_PORT", 587),
		SMTPUsername:           os.Getenv("MAIL_USERNAME"),
		SMTPPassword:           os.Getenv("MAIL_PASSWORD"),
		SMTPEncryption:         strings.ToLower(env("MAIL_ENCRYPTION", "starttls")),
		GraphTenantID:          os.Getenv("MS_TENANT_ID"),
		GraphClientID:          os.Getenv("MS_CLIENT_ID"),
		GraphClientSecret:      os.Getenv("MS_CLIENT_SECRET"),
		GraphSenderUPN:         os.Getenv("MS_SENDER_UPN"),
		AgentMailAPIKey:        os.Getenv("AGENTMAIL_API_KEY"),
		AgentMailInbox:         os.Getenv("AGENTMAIL_INBOX"),
		AgentMailAPIBase:       env("AGENTMAIL_API_BASE", "https://api.agentmail.to"),
		GitHubToken:            os.Getenv("CONTACT_INTAKE_GITHUB_TOKEN"),
		GitHubRepo:             os.Getenv("CONTACT_GITHUB_REPO"),
		GitHubAPIBase:          env("CONTACT_GITHUB_API_BASE", "https://api.github.com"),
		RateLimitPerHour:       envInt("CONTACT_RATE_LIMIT_PER_HOUR", 20),
		GlobalRateLimitPerHour: envInt("CONTACT_GLOBAL_RATE_LIMIT_PER_HOUR", 100),
		GitHubDailyCap:         envInt("CONTACT_GITHUB_DAILY_CAP", 50),
		TrustProxy:             envBool("CONTACT_TRUST_PROXY", false),
		TrustedProxyHops:       envInt("CONTACT_TRUSTED_PROXY_HOPS", 1),
		CookieSecure:           envBool("CONTACT_SECURE_COOKIE", false),
	}
	for _, a := range strings.Split(os.Getenv("CONTACT_ALLOWED_SENDERS"), ",") {
		if a = strings.TrimSpace(strings.ToLower(a)); a != "" {
			c.AllowedSenders = append(c.AllowedSenders, a)
		}
	}
	return c, c.validate()
}

func (c *Config) validate() error {
	switch c.Transport {
	case "smtp", "graph", "agentmail":
	default:
		return fmt.Errorf("MAIL_TRANSPORT must be 'smtp', 'graph', or 'agentmail', got %q", c.Transport)
	}
	switch c.SMTPEncryption {
	case "starttls", "tls", "none":
	default:
		return fmt.Errorf("MAIL_ENCRYPTION must be 'starttls', 'tls', or 'none', got %q", c.SMTPEncryption)
	}
	if c.RateLimitPerHour < 1 {
		return fmt.Errorf("CONTACT_RATE_LIMIT_PER_HOUR must be >= 1, got %d", c.RateLimitPerHour)
	}
	if c.GlobalRateLimitPerHour < 1 {
		return fmt.Errorf("CONTACT_GLOBAL_RATE_LIMIT_PER_HOUR must be >= 1, got %d", c.GlobalRateLimitPerHour)
	}
	if c.GitHubDailyCap < 1 {
		return fmt.Errorf("CONTACT_GITHUB_DAILY_CAP must be >= 1, got %d", c.GitHubDailyCap)
	}
	if c.TrustedProxyHops < 1 {
		return fmt.Errorf("CONTACT_TRUSTED_PROXY_HOPS must be >= 1, got %d", c.TrustedProxyHops)
	}
	return nil
}

// mailConfigured reports whether the selected transport has everything it needs
// to actually deliver. Gates /readyz and the submit handler.
func (c *Config) mailConfigured() bool {
	if c.Recipient == "" {
		return false
	}
	switch c.Transport {
	case "smtp":
		return c.FromAddress != "" && c.SMTPHost != ""
	case "graph":
		return c.FromAddress != "" && c.GraphTenantID != "" && c.GraphClientID != "" && c.GraphClientSecret != ""
	case "agentmail":
		// From is fixed to the inbox, so FromAddress isn't required here.
		return c.AgentMailAPIKey != "" && c.AgentMailInbox != ""
	}
	return false
}

// githubConfigured reports whether the (optional) GitHub-issue channel is wired.
func (c *Config) githubConfigured() bool {
	return c.GitHubToken != "" && c.GitHubRepo != ""
}

// senderAllowed enforces the approved-sender policy. An explicit allowlist wins;
// otherwise a domain suffix (if set); otherwise any well-formed address.
func (c *Config) senderAllowed(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(c.AllowedSenders) > 0 {
		for _, a := range c.AllowedSenders {
			if a == email {
				return true
			}
		}
		return false
	}
	if c.AllowedDomain == "" {
		return true
	}
	return strings.HasSuffix(email, "@"+strings.ToLower(c.AllowedDomain))
}

// senderRejectMessage is the user-facing reason an address was refused.
func (c *Config) senderRejectMessage() string {
	if len(c.AllowedSenders) > 0 {
		return "this address is not on the approved senders list"
	}
	return fmt.Sprintf("please use your @%s email address", c.AllowedDomain)
}

// randomToken returns n bytes of cryptographically-random data, hex-encoded.
// Used for the CSRF synchronizer token.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out), nil
}
