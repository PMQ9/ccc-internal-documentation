package main

import "testing"

// The env* readers are the seam between 12-factor config and the typed Config.
// They are pure-logic, so they get cheap, exhaustive table tests here rather than
// being exercised only indirectly through Load().

func TestEnvHelper(t *testing.T) {
	if got := env("CCC_TEST_UNSET_VAR", "fallback"); got != "fallback" {
		t.Errorf("env(unset) = %q, want fallback", got)
	}
	t.Setenv("CCC_TEST_SET_VAR", "value")
	if got := env("CCC_TEST_SET_VAR", "fallback"); got != "value" {
		t.Errorf("env(set) = %q, want value", got)
	}
	t.Setenv("CCC_TEST_EMPTY_VAR", "")
	if got := env("CCC_TEST_EMPTY_VAR", "fallback"); got != "fallback" {
		t.Errorf("env(set-empty) = %q, want fallback (empty treated as unset)", got)
	}
}

func TestEnvIntHelper(t *testing.T) {
	if got := envInt("CCC_TEST_INT_UNSET", 42); got != 42 {
		t.Errorf("envInt(unset) = %d, want 42", got)
	}
	t.Setenv("CCC_TEST_INT", "7")
	if got := envInt("CCC_TEST_INT", 42); got != 7 {
		t.Errorf("envInt(set) = %d, want 7", got)
	}
	t.Setenv("CCC_TEST_INT_BAD", "not-a-number")
	if got := envInt("CCC_TEST_INT_BAD", 42); got != 42 {
		t.Errorf("envInt(invalid) = %d, want the default 42", got)
	}
}

func TestEnvBoolHelper(t *testing.T) {
	cases := map[string]struct {
		val  string
		want bool
	}{
		"true":  {"true", true},
		"TRUE":  {"TRUE", true},
		"one":   {"1", true},
		"false": {"false", false},
		"junk":  {"yes", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CCC_TEST_BOOL", tc.val)
			if got := envBool("CCC_TEST_BOOL", false); got != tc.want {
				t.Errorf("envBool(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
	if got := envBool("CCC_TEST_BOOL_UNSET", true); got != true {
		t.Errorf("envBool(unset) = %v, want the default true", got)
	}
}

// CONTACT_ALLOWED_SENDERS is a comma list that must be trimmed, lowercased, and
// have empty entries dropped — the parsing the senderAllowed allowlist depends on.
func TestLoadParsesAllowedSenders(t *testing.T) {
	// Set the mail basics explicitly so this stays a test of sender PARSING even if
	// validate() later grows a presence check on these (it would otherwise fail here
	// for an unrelated reason).
	t.Setenv("CONTACT_RECIPIENT", "dest@example.org")
	t.Setenv("MAIL_FROM_ADDRESS", "from@example.org")
	t.Setenv("MAIL_HOST", "smtp.example.org")
	t.Setenv("CONTACT_ALLOWED_SENDERS", "  A@X.com , b@Y.com ,, ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"a@x.com", "b@y.com"}
	if len(cfg.AllowedSenders) != len(want) {
		t.Fatalf("AllowedSenders = %v, want %v", cfg.AllowedSenders, want)
	}
	for i, w := range want {
		if cfg.AllowedSenders[i] != w {
			t.Errorf("AllowedSenders[%d] = %q, want %q", i, cfg.AllowedSenders[i], w)
		}
	}
}

func TestLoadRejectsBadTransport(t *testing.T) {
	t.Setenv("MAIL_TRANSPORT", "carrier-pigeon")
	if _, err := Load(); err == nil {
		t.Error("Load must reject an unknown MAIL_TRANSPORT")
	}
}

func TestLoadTrustProxyHops(t *testing.T) {
	t.Setenv("CONTACT_TRUST_PROXY", "true")
	t.Setenv("CONTACT_TRUSTED_PROXY_HOPS", "2")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.TrustProxy {
		t.Error("CONTACT_TRUST_PROXY=true should set TrustProxy")
	}
	if cfg.TrustedProxyHops != 2 {
		t.Errorf("TrustedProxyHops = %d, want 2", cfg.TrustedProxyHops)
	}
}
