package main

import "testing"

func TestValidate(t *testing.T) {
	// A baseline valid config; cases below flip one field at a time to invalid.
	base := func() *Config {
		return &Config{Transport: "smtp", SMTPEncryption: "starttls", RateLimitPerHour: 1,
			GlobalRateLimitPerHour: 1, GitHubDailyCap: 1, TrustedProxyHops: 1}
	}
	if err := base().validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []*Config{
		{Transport: "bogus", SMTPEncryption: "starttls", RateLimitPerHour: 1, GlobalRateLimitPerHour: 1, GitHubDailyCap: 1, TrustedProxyHops: 1},
		{Transport: "smtp", SMTPEncryption: "rot13", RateLimitPerHour: 1, GlobalRateLimitPerHour: 1, GitHubDailyCap: 1, TrustedProxyHops: 1},
		{Transport: "smtp", SMTPEncryption: "starttls", RateLimitPerHour: 0, GlobalRateLimitPerHour: 1, GitHubDailyCap: 1, TrustedProxyHops: 1},
		{Transport: "smtp", SMTPEncryption: "starttls", RateLimitPerHour: 1, GlobalRateLimitPerHour: 0, GitHubDailyCap: 1, TrustedProxyHops: 1},
		{Transport: "smtp", SMTPEncryption: "starttls", RateLimitPerHour: 1, GlobalRateLimitPerHour: 1, GitHubDailyCap: 0, TrustedProxyHops: 1},
		{Transport: "smtp", SMTPEncryption: "starttls", RateLimitPerHour: 1, GlobalRateLimitPerHour: 1, GitHubDailyCap: 1, TrustedProxyHops: 0},
	}
	for i, c := range bad {
		if err := c.validate(); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

func TestMailConfigured(t *testing.T) {
	smtp := &Config{Transport: "smtp", Recipient: "a@b.org", FromAddress: "c@d.org", SMTPHost: "h"}
	if !smtp.mailConfigured() {
		t.Error("smtp with host/recipient/from should be configured")
	}
	smtp.SMTPHost = ""
	if smtp.mailConfigured() {
		t.Error("smtp without host should not be configured")
	}
	graph := &Config{Transport: "graph", Recipient: "a@b.org", FromAddress: "c@d.org",
		GraphTenantID: "t", GraphClientID: "i", GraphClientSecret: "s"}
	if !graph.mailConfigured() {
		t.Error("graph with full creds should be configured")
	}
	graph.GraphClientSecret = ""
	if graph.mailConfigured() {
		t.Error("graph missing secret should not be configured")
	}
	// AgentMail needs an API key + inbox + recipient, but NOT a from address.
	am := &Config{Transport: "agentmail", Recipient: "a@b.org",
		AgentMailAPIKey: "am_x", AgentMailInbox: "ccc-3278@agentmail.to"}
	if !am.mailConfigured() {
		t.Error("agentmail with key+inbox+recipient should be configured")
	}
	am.AgentMailAPIKey = ""
	if am.mailConfigured() {
		t.Error("agentmail missing api key should not be configured")
	}
}

func TestSenderAllowed(t *testing.T) {
	open := &Config{}
	if !open.senderAllowed("anyone@anywhere.com") {
		t.Error("no policy should allow any well-formed sender")
	}
	dom := &Config{AllowedDomain: "vanderbilt.edu"}
	if !dom.senderAllowed("Jane@Vanderbilt.edu") {
		t.Error("matching domain (case-insensitive) should pass")
	}
	if dom.senderAllowed("jane@gmail.com") {
		t.Error("non-matching domain should fail")
	}
	list := &Config{AllowedSenders: []string{"a@x.com"}, AllowedDomain: "vanderbilt.edu"}
	if !list.senderAllowed("A@X.com") {
		t.Error("allowlisted address should pass")
	}
	if list.senderAllowed("b@vanderbilt.edu") {
		t.Error("allowlist must override domain (non-listed should fail)")
	}
}

func TestRandomToken(t *testing.T) {
	a, err := randomToken(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 {
		t.Errorf("randomToken(16) len=%d want 32", len(a))
	}
	b, _ := randomToken(16)
	if a == b {
		t.Error("two tokens should differ")
	}
}
