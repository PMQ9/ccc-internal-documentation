package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Transport-edge coverage for the two outbound side channels (AgentMail REST +
// GitHub issues) and the RFC 5322 serializer: the failure and omit-empty paths
// the happy-path tests in mailer_test.go / github_test.go don't reach.

// A non-2xx from the AgentMail API must surface as an error that carries the
// status and the upstream body (so the operator log says *why* a send failed).
func TestAgentMailSendNon2xxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"relay exploded"}`))
	}))
	defer srv.Close()

	m := &agentMailer{apiKey: "am_x", inbox: "ccc-3278@agentmail.to", apiBase: srv.URL, httpc: &http.Client{Timeout: 2 * time.Second}}
	err := m.Send(context.Background(), &Message{To: "d@x.test", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("expected an error on a non-2xx AgentMail response")
	}
	if !strings.Contains(err.Error(), "relay exploded") {
		t.Errorf("error should carry the upstream body: %v", err)
	}
}

// A message with no Reply-To and no extra headers must NOT emit empty/absent keys
// into the JSON payload (the optional fields are conditionally added).
func TestAgentMailSendOmitsEmptyOptionalFields(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := &agentMailer{apiKey: "am_x", inbox: "ccc-3278@agentmail.to", apiBase: srv.URL, httpc: &http.Client{Timeout: 2 * time.Second}}
	if err := m.Send(context.Background(), &Message{To: "d@x.test", Subject: "s", Body: "b"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if strings.Contains(body, "reply_to") {
		t.Errorf("payload should omit reply_to when ReplyTo is empty: %s", body)
	}
	if strings.Contains(body, "headers") {
		t.Errorf("payload should omit headers when none are set: %s", body)
	}
}

// A GitHub outage (unreachable endpoint) must return an error — the caller treats
// it as non-fatal, but it must still know the issue wasn't filed.
func TestGitHubCreateIssueTransportError(t *testing.T) {
	gh := newGitHubClient(&Config{GitHubToken: "tok", GitHubRepo: "o/r", GitHubAPIBase: "http://127.0.0.1:1"})
	if _, err := gh.CreateIssue(context.Background(), issue{Title: "t"}); err == nil {
		t.Error("expected a transport error against an unreachable GitHub endpoint")
	}
}

// An issue with no labels must not serialize a "labels" key (omitempty), so a
// kind with no mapped label files cleanly.
func TestGitHubCreateIssueNoLabels(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/o/r/issues/9"}`))
	}))
	defer srv.Close()

	gh := newGitHubClient(&Config{GitHubToken: "tok", GitHubRepo: "o/r", GitHubAPIBase: srv.URL})
	if _, err := gh.CreateIssue(context.Background(), issue{Title: "t", Body: "b"}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if strings.Contains(body, "labels") {
		t.Errorf("body should omit labels when none given: %s", body)
	}
}

// A 2xx whose body isn't the expected JSON must still succeed (the URL parse is
// best-effort) — GitHub having filed the issue is what matters, not the echo.
func TestGitHubCreateIssueToleratesNonJSONSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`Created`)) // not JSON
	}))
	defer srv.Close()

	gh := newGitHubClient(&Config{GitHubToken: "tok", GitHubRepo: "o/r", GitHubAPIBase: srv.URL})
	url, err := gh.CreateIssue(context.Background(), issue{Title: "t"})
	if err != nil {
		t.Fatalf("a 2xx with a non-JSON body must not error: %v", err)
	}
	if url != "" {
		t.Errorf("html_url = %q, want empty (no JSON to parse)", url)
	}
}

// rfc822 must emit a Date and a Message-ID derived from the sender (the bits a
// relay/Outlook threading needs), in addition to the body/subject checks elsewhere.
func TestRFC822DateAndMessageID(t *testing.T) {
	m := &Message{FromAddr: "from@proton.test", To: "to@x.test", Subject: "s", Body: "b"}
	raw := string(m.rfc822(time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)))
	if !strings.Contains(raw, "Date: ") {
		t.Errorf("rfc822 missing Date header:\n%s", raw)
	}
	if !strings.Contains(raw, "Message-ID: <") || !strings.Contains(raw, "from@proton.test>") {
		t.Errorf("rfc822 Message-ID missing/not sender-derived:\n%s", raw)
	}
}

// Multiple extra headers must serialize in deterministic (sorted) order, so the
// wire output is stable and assertable.
func TestRFC822SortsHeaders(t *testing.T) {
	m := &Message{
		FromAddr: "f@x.test", To: "t@x.test", Subject: "s", Body: "b",
		Headers: map[string]string{"X-Zeta": "1", "X-Alpha": "2"},
	}
	raw := string(m.rfc822(time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)))
	ai, zi := strings.Index(raw, "X-Alpha:"), strings.Index(raw, "X-Zeta:")
	if ai < 0 || zi < 0 {
		t.Fatalf("both headers must be present:\n%s", raw)
	}
	if ai > zi {
		t.Errorf("headers not sorted: X-Alpha at %d after X-Zeta at %d", ai, zi)
	}
}
