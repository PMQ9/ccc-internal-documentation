package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunHelpAndVersion(t *testing.T) {
	for _, argv := range [][]string{{"help"}, {}, {"-h"}} {
		var out, errb bytes.Buffer
		if code := run(argv, &out, &errb, strings.NewReader(""), envFrom(nil)); code != codeOK {
			t.Errorf("argv %v: code = %d, want %d", argv, code, codeOK)
		}
		if !strings.Contains(out.String(), "usage:") {
			t.Errorf("argv %v: help text missing 'usage:'", argv)
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb, strings.NewReader(""), envFrom(nil)); code != codeOK {
		t.Errorf("version: code = %d", code)
	}
	if strings.TrimSpace(out.String()) != version {
		t.Errorf("version output = %q, want %q", strings.TrimSpace(out.String()), version)
	}
}

func TestRunUnknownAndMissing(t *testing.T) {
	cases := [][]string{
		{"widget", "frob"},  // unknown resource
		{"book", "explode"}, // unknown action
		{"book"},            // missing action
	}
	for _, argv := range cases {
		var out, errb bytes.Buffer
		if code := run(argv, &out, &errb, strings.NewReader(""), envFrom(nil)); code != codeUsage {
			t.Errorf("argv %v: code = %d, want %d", argv, code, codeUsage)
		}
	}
}

// TestRunRejectsTokenFlag locks down R4: there is no --token flag in either position.
func TestRunRejectsTokenFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--token", "id:sec", "book", "list"}, &out, &errb, strings.NewReader(""), envFrom(nil)); code != codeUsage {
		t.Errorf("--token before resource: code = %d, want %d", code, codeUsage)
	}
	env := envFrom(map[string]string{"WIKI_BASE_URL": "http://x", "WIKI_API_TOKEN": "e:e"})
	if code := run([]string{"book", "list", "--token", "id:sec"}, &out, &errb, strings.NewReader(""), env); code != codeUsage {
		t.Errorf("--token after subcommand: code = %d, want %d", code, codeUsage)
	}
}

// TestRunStdinBodyPath drives the `--markdown-file -` stdin path through run() end to
// end (run() now takes stdin), proving a piped body reaches the request.
func TestRunStdinBodyPath(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":7,"name":"From Stdin","book_id":1}`))
	}))
	defer srv.Close()

	env := envFrom(map[string]string{
		"WIKI_BASE_URL":   srv.URL,
		"WIKI_API_TOKEN":  "tid:sec",
		"CCC_WIKI_CONFIG": writeTempConfig(t, ""),
	})
	var out, errb bytes.Buffer
	code := run(
		[]string{"--json", "page", "create", "--book", "1", "--name", "From Stdin", "--markdown-file", "-"},
		&out, &errb, strings.NewReader("# piped body\n"), env,
	)
	if code != codeOK {
		t.Fatalf("run code = %d, want %d (stderr: %s)", code, codeOK, errb.String())
	}
	if !strings.Contains(gotBody, "piped body") {
		t.Errorf("request body missing piped stdin content: %s", gotBody)
	}
}

// TestTokenNeverInOutput is the CLI's fitness function (the analog of the core's
// TestTokenNeverLogged): across success, --json, error, and usage paths, the token and
// its secret half must appear in NEITHER stdout NOR stderr.
func TestTokenNeverInOutput(t *testing.T) {
	const tokenID = "tokABC123"
	const tokenSecret = "secretXYZ789"
	const token = tokenID + ":" + tokenSecret

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/books") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"B"}],"total":1}`))
		case r.URL.Path == "/api/pages" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":403,"message":"denied"}}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	// An empty, explicit, 0600 config file keeps resolveConfig from consulting $HOME.
	env := envFrom(map[string]string{
		"WIKI_BASE_URL":    srv.URL,
		"WIKI_API_TOKEN":   token,
		"WIKI_MAX_RETRIES": "0",
		"CCC_WIKI_CONFIG":  writeTempConfig(t, ""),
	})

	invocations := [][]string{
		{"book", "list"},           // success, human
		{"--json", "book", "list"}, // success, json
		{"page", "create", "--book", "1", "--name", "X", "--markdown", "# y"},            // 403, human
		{"--json", "page", "create", "--book", "1", "--name", "X", "--html", "<p>y</p>"}, // 403, json
		{"page", "get"}, // usage error
	}
	for _, argv := range invocations {
		var out, errb bytes.Buffer
		_ = run(argv, &out, &errb, strings.NewReader(""), env)
		combined := out.String() + errb.String()
		for _, secret := range []string{token, tokenSecret} {
			if strings.Contains(combined, secret) {
				t.Errorf("argv %v leaked the token/secret\nSTDOUT: %s\nSTDERR: %s", argv, out.String(), errb.String())
			}
		}
	}
}
