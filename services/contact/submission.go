package main

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// kind is the category of a contact submission. The set is closed: adding a
// value here and to the maps below keeps the email subject, the
// X-CCC-Contact-Type header, and the GitHub label from ever disagreeing
// (stringly-typed status fields are a bug factory — this is the antidote).
type kind string

const (
	kindBug      kind = "bug"
	kindRequest  kind = "request"
	kindFeedback kind = "feedback"
	kindOther    kind = "other"
)

// ordered list for the form's <select> (maps need a stable display order).
var kindOrder = []kind{kindBug, kindRequest, kindFeedback, kindOther}

var kindLabels = map[kind]string{
	kindBug:      "Bug report",
	kindRequest:  "Request",
	kindFeedback: "Feedback",
	kindOther:    "Other",
}

// gitHubLabel maps a kind to the repo issue label — the "issue label map" from
// issue #15. A kind with no entry files the issue with no label.
var gitHubLabel = map[kind]string{
	kindBug:      "bug",
	kindRequest:  "enhancement",
	kindFeedback: "feedback",
	kindOther:    "question",
}

func parseKind(s string) (kind, bool) {
	k := kind(strings.ToLower(strings.TrimSpace(s)))
	if _, ok := kindLabels[k]; ok {
		return k, true
	}
	return "", false
}

// kindOption is one <select> choice for the form template.
type kindOption struct {
	Value string
	Label string
}

func kindOptions() []kindOption {
	out := make([]kindOption, 0, len(kindOrder))
	for _, k := range kindOrder {
		out = append(out, kindOption{Value: string(k), Label: kindLabels[k]})
	}
	return out
}

// Field length caps. Generous for humans, bounded against abuse / oversized POSTs.
const (
	maxName    = 200
	maxSummary = 200
	maxDetails = 8000
	maxPage    = 500
)

// formInput is the raw, untrusted form payload.
type formInput struct {
	Kind    string
	Name    string
	Email   string
	Page    string
	Summary string
	Details string
}

// fieldError is one validation failure, surfaced back on the re-rendered form.
type fieldError struct {
	Field   string
	Message string
}

// Submission is one validated contact entry. Construct only via newSubmission,
// which enforces the invariants (valid kind, allowed well-formed email, non-empty
// summary); downstream code trusts these fields.
type Submission struct {
	Kind      kind
	Name      string
	Email     string // submitter; becomes Reply-To
	Page      string // optional URL/area the feedback is about
	Summary   string
	Details   string
	At        time.Time
	SourceIP  string
	UserAgent string
}

// newSubmission validates raw input against the config policy. It returns either
// a Submission or a non-empty slice of field errors (never both).
func newSubmission(f formInput, c *Config, now time.Time) (*Submission, []fieldError) {
	var errs []fieldError
	add := func(field, msg string) { errs = append(errs, fieldError{field, msg}) }

	k, ok := parseKind(f.Kind)
	if !ok {
		add("type", "choose a valid type")
	}

	name := strings.TrimSpace(f.Name)
	switch {
	case name == "":
		add("name", "your name is required")
	case len(name) > maxName:
		add("name", "name is too long")
	}

	email := strings.TrimSpace(strings.ToLower(f.Email))
	switch {
	case email == "":
		add("email", "your email is required")
	case !validEmail(email):
		add("email", "enter a valid email address")
	case !c.senderAllowed(email):
		add("email", c.senderRejectMessage())
	}

	summary := strings.TrimSpace(f.Summary)
	switch {
	case summary == "":
		add("summary", "a short summary is required")
	case len(summary) > maxSummary:
		add("summary", "summary is too long")
	}

	details := strings.TrimSpace(f.Details)
	if len(details) > maxDetails {
		add("details", "details are too long")
	}

	page := strings.TrimSpace(f.Page)
	if len(page) > maxPage {
		add("page", "page/area is too long")
	}

	if len(errs) > 0 {
		return nil, errs
	}
	return &Submission{
		Kind:    k,
		Name:    name,
		Email:   email,
		Page:    page,
		Summary: summary,
		Details: details,
		At:      now,
	}, nil
}

// validEmail accepts only a bare, single-address mailbox (no display name, no
// group, exactly one @). Rejecting display-name forms also blocks header
// injection via the email field.
func validEmail(s string) bool {
	if strings.ContainsAny(s, "\r\n") || strings.Count(s, "@") != 1 {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// truncateRunes shortens to at most n runes (UTF-8 safe), adding an ellipsis.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// subject is the email title + GitHub issue title: a stable, filterable prefix
// drives the Outlook rule into the Contact_form folder.
func (s *Submission) subject(wikiName string) string {
	// ASCII separator (not an em dash) so a plain-ASCII summary yields a plain,
	// human-readable, grep-able Subject header rather than a MIME-encoded word.
	return fmt.Sprintf("[%s] %s - %s", wikiName, kindLabels[s.Kind], truncateRunes(s.Summary, 120))
}

// emailView is the data the plain-text email template renders.
type emailView struct {
	KindLabel string
	Name      string
	Email     string
	At        string
	Page      string
	Summary   string
	Details   string
	WikiName  string
	SourceIP  string
	UserAgent string
}

func (s *Submission) emailView(wikiName string) emailView {
	details := s.Details
	if details == "" {
		details = "(none provided)"
	}
	return emailView{
		KindLabel: kindLabels[s.Kind],
		Name:      s.Name,
		Email:     s.Email,
		At:        s.At.Format("2006-01-02 15:04 MST"),
		Page:      s.Page,
		Summary:   s.Summary,
		Details:   details,
		WikiName:  wikiName,
		SourceIP:  s.SourceIP,
		UserAgent: s.UserAgent,
	}
}

// mdFence renders s as a GitHub-flavored-Markdown fenced code block, picking a
// backtick fence strictly longer than the longest run of backticks inside s.
// Inside a code fence nothing is interpreted — no @mentions notify users, no
// #refs cross-link, no ![images] load (tracking/exfil pixels), no HTML renders —
// so this neutralizes the whole class of Markdown-injection abuse from untrusted
// submission fields rather than escaping triggers one by one (issue #41).
func mdFence(s string) string {
	if s == "" {
		return "_(none provided)_"
	}
	longest, run := 0, 0
	for _, c := range s {
		if c == '`' {
			if run++; run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, longest+1))
	// Own-line fence markers + a trailing newline keep them intact even when s
	// starts/ends with backticks or whitespace.
	return fence + "\n" + s + "\n" + fence
}

// issueBody renders the GitHub issue body (Markdown) for the same submission.
// Every submitter-controlled field is wrapped with mdFence so a submission can
// never inject @mentions, #refs, images, or markup into the tracker; only the
// closed-set Type and the server-generated timestamp render as prose (issue #41).
func (s *Submission) issueBody(wikiName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Type:** %s  \n", kindLabels[s.Kind])
	fmt.Fprintf(&b, "**Submitted:** %s\n\n", s.At.Format("2006-01-02 15:04 MST"))
	b.WriteString("**From** (name / email):\n")
	b.WriteString(mdFence(s.Name + " <" + s.Email + ">"))
	if s.Page != "" {
		b.WriteString("\n\n**Page/Area:**\n")
		b.WriteString(mdFence(s.Page))
	}
	b.WriteString("\n\n**Summary:**\n")
	b.WriteString(mdFence(s.Summary))
	b.WriteString("\n\n**Details:**\n")
	b.WriteString(mdFence(s.Details))
	fmt.Fprintf(&b, "\n\n---\n_Filed automatically by the %s contact form. All submitter-supplied fields above are shown verbatim and unrendered. Reply to the email notification to reach the submitter._\n", wikiName)
	return b.String()
}
