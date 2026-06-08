package main

import (
	"strings"
	"testing"
)

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

func TestSenderRejectMessageAllowlist(t *testing.T) {
	c := &Config{AllowedSenders: []string{"a@vanderbilt.edu"}}
	msg := c.senderRejectMessage()
	if !strings.Contains(msg, "approved senders") {
		t.Errorf("reject message should mention approved senders: %q", msg)
	}
}

func TestSenderRejectMessageDomain(t *testing.T) {
	c := &Config{AllowedDomain: "vanderbilt.edu"}
	msg := c.senderRejectMessage()
	if !strings.Contains(msg, "vanderbilt.edu") {
		t.Errorf("reject message should mention the allowed domain: %q", msg)
	}
}

func TestValidateAgentMailTransport(t *testing.T) {
	c := &Config{Transport: "agentmail", SMTPEncryption: "starttls",
		RateLimitPerHour: 1, GlobalRateLimitPerHour: 1, GitHubDailyCap: 1, TrustedProxyHops: 1}
	if err := c.validate(); err != nil {
		t.Errorf("agentmail transport should be valid: %v", err)
	}
}

func TestMailConfiguredAgentMail(t *testing.T) {
	// AgentMail requires key + inbox but NOT a from address.
	c := &Config{Transport: "agentmail", Recipient: "a@b.org",
		AgentMailAPIKey: "am_x", AgentMailInbox: "ccc-3278@agentmail.to"}
	if !c.mailConfigured() {
		t.Error("agentmail with key+inbox should be configured")
	}
	c.AgentMailInbox = ""
	if c.mailConfigured() {
		t.Error("agentmail without inbox should NOT be configured")
	}
}

func TestMailConfiguredGraphDefaults(t *testing.T) {
	// Graph uses FromAddress when GraphSenderUPN is empty.
	c := &Config{Transport: "graph", Recipient: "a@b.org", FromAddress: "c@d.org",
		GraphTenantID: "t", GraphClientID: "i", GraphClientSecret: "s"}
	if !c.mailConfigured() {
		t.Error("graph with full creds + from should be configured")
	}
	// Without FromAddress, graph should not be configured.
	c.FromAddress = ""
	if c.mailConfigured() {
		t.Error("graph without from address should NOT be configured")
	}
}

func TestSenderAllowedCaseInsensitive(t *testing.T) {
	c := &Config{AllowedSenders: []string{"A@Vanderbilt.EDU"}}
	if !c.senderAllowed("a@vanderbilt.edu") {
		t.Error("allowlist should be case-insensitive")
	}
	if !c.senderAllowed("A@VANDERBILT.EDU") {
		t.Error("allowlist should handle different casing")
	}
}

func TestSenderAllowedEmptyAllowlist(t *testing.T) {
	c := &Config{AllowedSenders: []string{}}
	if !c.senderAllowed("a@vanderbilt.edu") {
		t.Error("empty allowlist with no domain should allow any")
	}
}

func TestLoadDefaults(t *testing.T) {
	// With no env set, Load should return defaults (and fail validation because
	// some are required). We just test it doesn't panic and returns something.
	t.Setenv("CONTACT_RECIPIENT", "test@example.org")
	t.Setenv("MAIL_FROM_ADDRESS", "from@example.org")
	t.Setenv("MAIL_HOST", "smtp.example.org")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with minimal valid env should succeed: %v", err)
	}
	if cfg.RateLimitPerHour != 20 {
		t.Errorf("default RateLimitPerHour = %d, want 20", cfg.RateLimitPerHour)
	}
	if cfg.Transport != "smtp" {
		t.Errorf("default Transport = %q, want smtp", cfg.Transport)
	}
}
