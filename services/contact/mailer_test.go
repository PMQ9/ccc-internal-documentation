package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRFC822(t *testing.T) {
	m := &Message{
		FromName: "CCC Wiki Contact",
		FromAddr: "from@proton.test",
		To:       "dest@example.org",
		ReplyTo:  "jane@vanderbilt.edu",
		Subject:  "[CCC Wiki] Bug report — Café",
		Body:     "line1\nline2",
		Headers:  map[string]string{"X-CCC-Contact-Type": "bug"},
	}
	raw := string(m.rfc822(time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)))

	for _, want := range []string{
		"To: dest@example.org\r\n",
		"Reply-To: jane@vanderbilt.edu\r\n",
		"X-CCC-Contact-Type: bug\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("rfc822 missing %q in:\n%s", want, raw)
		}
	}
	// Non-ASCII subject must be MIME-encoded, never emitted raw (also blocks injection).
	if strings.Contains(raw, "Café") {
		t.Error("subject should be MIME-encoded, found raw non-ASCII")
	}
	if !strings.Contains(raw, "Subject: =?utf-8?q?") {
		t.Error("subject not Q-encoded")
	}
	// Body is CRLF-normalized and separated from headers by a blank line.
	if !strings.Contains(raw, "\r\n\r\nline1\r\nline2") {
		t.Error("body not CRLF-normalized after header break")
	}
}

func TestRFC822HeaderInjection(t *testing.T) {
	// Defense in depth: a CRLF in a header-bound field is stripped, so it can't
	// start a NEW header line (the text is harmlessly folded into the value).
	m := &Message{
		FromAddr: "from@proton.test",
		To:       "dest@example.org\r\nBcc: attacker@evil.test",
		Subject:  "hi",
		Body:     "b",
	}
	raw := string(m.rfc822(time.Date(2026, 6, 6, 14, 30, 0, 0, time.UTC)))
	if strings.Contains(raw, "\nBcc:") {
		t.Errorf("CRLF injection created a new header line:\n%s", raw)
	}
}

func TestNewMailer(t *testing.T) {
	if m, err := newMailer(&Config{Transport: "smtp", SMTPPort: 587, SMTPEncryption: "starttls"}); err != nil {
		t.Fatalf("smtp: %v", err)
	} else if _, ok := m.(*smtpMailer); !ok {
		t.Errorf("smtp transport gave %T, want *smtpMailer", m)
	}
	if m, err := newMailer(&Config{Transport: "graph", FromAddress: "x@y.z"}); err != nil {
		t.Fatalf("graph: %v", err)
	} else if _, ok := m.(*graphMailer); !ok {
		t.Errorf("graph transport gave %T, want *graphMailer", m)
	}
	if m, err := newMailer(&Config{Transport: "agentmail", AgentMailInbox: "ccc-3278@agentmail.to", AgentMailAPIKey: "am_x", AgentMailAPIBase: "https://api.agentmail.to"}); err != nil {
		t.Fatalf("agentmail: %v", err)
	} else if _, ok := m.(*agentMailer); !ok {
		t.Errorf("agentmail transport gave %T, want *agentMailer", m)
	}
	if _, err := newMailer(&Config{Transport: "carrier-pigeon"}); err == nil {
		t.Error("unknown transport should error")
	}
}

func TestAgentMailSend(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path // server-decoded (%40 -> @)
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message_id":"m1","thread_id":"t1"}`))
	}))
	defer srv.Close()

	m, err := newMailer(&Config{
		Transport: "agentmail", AgentMailAPIKey: "am_key",
		AgentMailInbox: "ccc-3278@agentmail.to", AgentMailAPIBase: srv.URL,
	})
	if err != nil {
		t.Fatalf("newMailer: %v", err)
	}
	err = m.Send(context.Background(), &Message{
		To: "dest@example.org", ReplyTo: "jane@vanderbilt.edu", Subject: "s", Body: "b",
		Headers: map[string]string{"X-CCC-Contact-Type": "bug"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/v0/inboxes/ccc-3278@agentmail.to/messages/send" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer am_key" {
		t.Errorf("auth = %q", gotAuth)
	}
	for _, want := range []string{`"to":"dest@example.org"`, `"reply_to":"jane@vanderbilt.edu"`, `"X-CCC-Contact-Type":"bug"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %s in: %s", want, gotBody)
		}
	}
}

func TestStripCRLF(t *testing.T) {
	if got := stripCRLF("a\r\nb\nc\rd"); got != "abcd" {
		t.Errorf("stripCRLF = %q", got)
	}
}

// A black-holing relay (accepts the TCP connection but never sends the SMTP
// greeting) must not hang the request goroutine: the connection deadline pinned
// from the send context makes Send fail fast. Without it, smtp.NewClient blocks
// reading the greeting until the server's WriteTimeout. (issue #43 — finding #1)
func TestSMTPSendHonorsContextDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		time.Sleep(2 * time.Second) // hold the connection open, say nothing
		_ = c.Close()
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	m := &smtpMailer{host: host, port: port, encryption: "none"}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- m.Send(ctx, &Message{FromAddr: "f@x.test", To: "t@x.test", Subject: "s", Body: "b"})
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Send should error against a black-holing relay")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("Send took %v — deadline not honored (should fail ~300ms)", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send hung past the context deadline — connection deadline not set")
	}
}
