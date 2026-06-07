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
