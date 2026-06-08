package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envFrom returns a getenv func backed by a map (nil map -> all empty).
func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// writeTempConfig writes a 0600 config file in a temp dir and returns its path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveConfigPrecedenceBaseURL(t *testing.T) {
	cf := writeTempConfig(t, "WIKI_BASE_URL=http://from-file\nWIKI_API_TOKEN=fid:fsec\n")
	env := map[string]string{"CCC_WIKI_CONFIG": cf}

	// file only
	cfg, err := resolveConfig(newGlobalFlags(), envFrom(env))
	if err != nil {
		t.Fatalf("resolveConfig (file): %v", err)
	}
	if cfg.BaseURL != "http://from-file" {
		t.Errorf("base = %q, want from-file", cfg.BaseURL)
	}

	// env overrides file
	env["WIKI_BASE_URL"] = "http://from-env"
	if cfg, _ = resolveConfig(newGlobalFlags(), envFrom(env)); cfg.BaseURL != "http://from-env" {
		t.Errorf("base = %q, want from-env", cfg.BaseURL)
	}

	// flag overrides env
	g := newGlobalFlags()
	g.seen["base-url"] = true
	g.baseURL = "http://from-flag"
	if cfg, _ = resolveConfig(g, envFrom(env)); cfg.BaseURL != "http://from-flag" {
		t.Errorf("base = %q, want from-flag", cfg.BaseURL)
	}
}

func TestTokenEnvOverridesFile(t *testing.T) {
	cf := writeTempConfig(t, "WIKI_API_TOKEN=fid:fsec\n")
	env := map[string]string{"CCC_WIKI_CONFIG": cf, "WIKI_BASE_URL": "http://x", "WIKI_API_TOKEN": "eid:esec"}
	cfg, err := resolveConfig(newGlobalFlags(), envFrom(env))
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if cfg.Token != "eid:esec" {
		t.Errorf("token did not take the env value over the file")
	}
}

func TestConfigFilePermsRefused(t *testing.T) {
	p := writeTempConfig(t, "WIKI_BASE_URL=http://x\nWIKI_API_TOKEN=id:sec\n")
	if err := os.Chmod(p, 0o644); err != nil { // loosen perms regardless of umask
		t.Fatal(err)
	}
	_, err := resolveConfig(newGlobalFlags(), envFrom(map[string]string{"CCC_WIKI_CONFIG": p}))
	if err == nil {
		t.Fatal("expected a perms error for a 0644 token file")
	}
	if !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("error should suggest chmod 600, got: %v", err)
	}
	if exitCode(err) != codeUsage {
		t.Errorf("perms error exit = %d, want %d", exitCode(err), codeUsage)
	}
	// The error must not echo the token value.
	if strings.Contains(err.Error(), "id:sec") {
		t.Errorf("perms error leaked the token: %v", err)
	}
}

func TestConfigBaseURLOnlyFileNeedsNoPerms(t *testing.T) {
	// A file with NO token is not a secret, so loose perms are tolerated.
	p := writeTempConfig(t, "WIKI_BASE_URL=http://x\n")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CCC_WIKI_CONFIG": p, "WIKI_API_TOKEN": "id:sec"}
	if _, err := resolveConfig(newGlobalFlags(), envFrom(env)); err != nil {
		t.Errorf("base-url-only file should not trip the perms check: %v", err)
	}
}

func TestMissingExplicitConfigErrors(t *testing.T) {
	env := map[string]string{"CCC_WIKI_CONFIG": filepath.Join(t.TempDir(), "nope")}
	if _, err := resolveConfig(newGlobalFlags(), envFrom(env)); err == nil {
		t.Fatal("expected an error for a missing explicit config file")
	}
}

func TestExplicitEmptyConfigErrors(t *testing.T) {
	g := newGlobalFlags()
	g.seen["config"] = true
	g.configPath = "" // `--config ""`
	env := map[string]string{"WIKI_BASE_URL": "http://x", "WIKI_API_TOKEN": "id:sec"}
	_, err := resolveConfig(g, envFrom(env))
	if err == nil {
		t.Fatal("expected an error for an explicit empty --config (not a silent fall-through)")
	}
	if exitCode(err) != codeUsage {
		t.Errorf("explicit empty --config exit = %d, want %d", exitCode(err), codeUsage)
	}
}

func TestResolveConfigMissingTokenErrorHasNoSecret(t *testing.T) {
	env := map[string]string{"WIKI_BASE_URL": "http://x"} // no token anywhere
	_, err := resolveConfig(newGlobalFlags(), envFrom(env))
	if err == nil {
		t.Fatal("expected an error when no token is configured")
	}
	if !strings.Contains(err.Error(), "WIKI_API_TOKEN") {
		t.Errorf("error should name WIKI_API_TOKEN: %v", err)
	}
}

func TestParseConfigFile(t *testing.T) {
	v := parseConfigFile("# comment\n\nWIKI_BASE_URL = http://x \n WIKI_API_TOKEN=id:sec=tail\nno-equals-line\n")
	if v["WIKI_BASE_URL"] != "http://x" {
		t.Errorf("base = %q, want trimmed http://x", v["WIKI_BASE_URL"])
	}
	if v["WIKI_API_TOKEN"] != "id:sec=tail" {
		t.Errorf("token = %q, want value after the first '=' kept", v["WIKI_API_TOKEN"])
	}
	if _, ok := v["no-equals-line"]; ok {
		t.Error("a line without '=' should be skipped")
	}
}
