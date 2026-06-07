package main

import (
	"strings"
	"testing"
	"time"
)

func fieldErr(errs []fieldError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

func TestParseKind(t *testing.T) {
	for _, in := range []string{"bug", "Bug", " request ", "feedback", "other"} {
		if _, ok := parseKind(in); !ok {
			t.Errorf("parseKind(%q) = not ok, want ok", in)
		}
	}
	if _, ok := parseKind("nope"); ok {
		t.Error("parseKind(nope) = ok, want not ok")
	}
}

func TestNewSubmissionValid(t *testing.T) {
	c := &Config{AllowedDomain: "vanderbilt.edu"}
	now := time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)
	sub, errs := newSubmission(formInput{
		Kind: "bug", Name: "Jane", Email: "Jane@Vanderbilt.edu", Summary: "Login 500",
	}, c, now)
	if len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %+v", errs)
	}
	if sub.Email != "jane@vanderbilt.edu" {
		t.Errorf("email = %q, want lowercased", sub.Email)
	}
	if sub.Kind != kindBug {
		t.Errorf("kind = %q, want bug", sub.Kind)
	}
}

func TestNewSubmissionErrors(t *testing.T) {
	c := &Config{AllowedDomain: "vanderbilt.edu"}
	now := time.Now()
	cases := map[string]formInput{
		"type":    {Kind: "x", Name: "J", Email: "j@vanderbilt.edu", Summary: "s"},
		"name":    {Kind: "bug", Name: "  ", Email: "j@vanderbilt.edu", Summary: "s"},
		"email":   {Kind: "bug", Name: "J", Email: "not-an-email", Summary: "s"},
		"summary": {Kind: "bug", Name: "J", Email: "j@vanderbilt.edu", Summary: ""},
	}
	for field, in := range cases {
		_, errs := newSubmission(in, c, now)
		if !fieldErr(errs, field) {
			t.Errorf("input %+v: expected error on field %q, got %+v", in, field, errs)
		}
	}
}

func TestNewSubmissionDomainReject(t *testing.T) {
	c := &Config{AllowedDomain: "vanderbilt.edu"}
	_, errs := newSubmission(formInput{
		Kind: "bug", Name: "J", Email: "j@gmail.com", Summary: "s",
	}, c, time.Now())
	if !fieldErr(errs, "email") {
		t.Errorf("expected email domain rejection, got %+v", errs)
	}
}

func TestValidEmail(t *testing.T) {
	good := []string{"a@b.com", "first.last@sub.domain.edu"}
	bad := []string{"", "no-at", "a@b@c.com", "Name <a@b.com>", "a@b.com\r\nBcc: x@y.com", "a@b.com, c@d.com"}
	for _, s := range good {
		if !validEmail(s) {
			t.Errorf("validEmail(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validEmail(s) {
			t.Errorf("validEmail(%q) = true, want false", s)
		}
	}
}

func TestSubject(t *testing.T) {
	s := &Submission{Kind: kindBug, Summary: "Login 500"}
	if got, want := s.subject("CCC Wiki"), "[CCC Wiki] Bug report - Login 500"; got != want {
		t.Errorf("subject = %q, want %q", got, want)
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("hello", 10); got != "hello" {
		t.Errorf("no truncation = %q", got)
	}
	if got := truncateRunes("hello", 3); got != "he…" {
		t.Errorf("truncated = %q, want he…", got)
	}
}

func TestGitHubLabelMap(t *testing.T) {
	want := map[kind]string{kindBug: "bug", kindRequest: "enhancement", kindFeedback: "feedback", kindOther: "question"}
	for k, v := range want {
		if gitHubLabel[k] != v {
			t.Errorf("gitHubLabel[%q] = %q, want %q", k, gitHubLabel[k], v)
		}
	}
}

func TestIssueBody(t *testing.T) {
	s := &Submission{Kind: kindBug, Name: "Jane", Email: "j@v.edu", Summary: "Sum", Details: "Steps",
		At: time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)}
	b := s.issueBody("CCC Wiki")
	for _, want := range []string{"**Type:** Bug report", "Jane", "Sum", "Steps"} {
		if !strings.Contains(b, want) {
			t.Errorf("issueBody missing %q in:\n%s", want, b)
		}
	}
}

// mdFence must preserve content verbatim while wrapping it in a backtick fence,
// so a submission can't smuggle @mentions / #refs / images / HTML into the
// GitHub tracker as live Markdown (issue #41).
func TestMdFenceNeutralizesMarkdown(t *testing.T) {
	for _, in := range []string{
		"@maintainer please look",
		"see #1 and #42",
		"![x](https://attacker.example/p.png)",
		"<img src=x onerror=alert(1)>",
	} {
		out := mdFence(in)
		if !strings.Contains(out, in) {
			t.Errorf("mdFence dropped content for %q: %q", in, out)
		}
		if !strings.HasPrefix(out, "```") || !strings.HasSuffix(out, "```") {
			t.Errorf("mdFence did not wrap %q in a fence: %q", in, out)
		}
	}
}

// A payload containing its own fence must get a longer fence, or it could break
// out of the block and reach live Markdown again.
func TestMdFenceOutgrowsInternalBackticks(t *testing.T) {
	in := "evil\n```\n@everyone\n```"
	out := mdFence(in)
	if !strings.HasPrefix(out, "````") { // 4 backticks > the internal run of 3
		t.Errorf("fence not longer than the internal backtick run: %q", out)
	}
	if !strings.Contains(out, in) {
		t.Errorf("content not preserved: %q", out)
	}
}

func TestMdFenceEmpty(t *testing.T) {
	if got := mdFence(""); got != "_(none provided)_" {
		t.Errorf("mdFence(\"\") = %q, want the placeholder", got)
	}
}

// The whole issue body must fence every submitter-controlled field, and carry the
// note that fields are unrendered.
func TestIssueBodyFencesUntrustedFields(t *testing.T) {
	sub := &Submission{
		Kind: kindBug, Name: "@everyone", Email: "a@vanderbilt.edu",
		Page: "#1", Summary: "ping @team", Details: "![x](http://evil/p.png)",
		At: time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC),
	}
	body := sub.issueBody("CCC Wiki")
	if !strings.Contains(body, "```\n@everyone <a@vanderbilt.edu>\n```") {
		t.Errorf("From line not fenced verbatim:\n%s", body)
	}
	if !strings.Contains(body, "verbatim and unrendered") {
		t.Error("issue body missing the verbatim-content note")
	}
}
