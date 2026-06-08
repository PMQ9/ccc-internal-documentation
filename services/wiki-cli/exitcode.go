package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

// Exit codes are part of the CLI's contract (a CI job branches on them). They map the
// failure CLASS, not the raw HTTP status, so scripts switch on a small stable set.
const (
	codeOK        = 0 // success
	codeError     = 1 // any other / unexpected error
	codeUsage     = 2 // bad flags/args, or a config problem (missing/loose-perms token)
	codeAuth      = 3 // 401 — the token is missing/invalid
	codeForbidden = 4 // 403 (role lacks the permission by design) or 404 (not found)
	codeServer    = 5 // 5xx after the retry budget, or a transport timeout
)

// usageError marks an error as a usage/config problem (exit 2): a bad flag, a missing
// required argument, an unresolved/loose-perms token, or a config-validation failure.
// It is distinct from an *APIError, which carries a server status.
type usageError struct{ err error }

func (u *usageError) Error() string { return u.err.Error() }
func (u *usageError) Unwrap() error { return u.err }

// usagef builds a usageError from a format string. The caller must never interpolate the
// token into the message (none of the call sites do).
func usagef(format string, a ...any) error { return &usageError{fmt.Errorf(format, a...)} }

// exitCode maps a (possibly nil) error to the CLI's exit-code contract.
func exitCode(err error) int {
	if err == nil {
		return codeOK
	}
	var ue *usageError
	if errors.As(err, &ue) {
		return codeUsage
	}
	var apiErr *wikiclient.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized:
			return codeAuth
		case apiErr.StatusCode == http.StatusForbidden, apiErr.StatusCode == http.StatusNotFound:
			return codeForbidden
		case apiErr.StatusCode >= 500:
			return codeServer
		default:
			// Other 4xx (400/409/422/429): a caller/server-state error the Agent-author
			// role shouldn't normally produce — treat as unexpected (1), not retryable.
			return codeError
		}
	}
	// A post-retry transport timeout is an infra problem, not a caller error: map it to
	// codeServer so a CI job can treat 5 as "retry later" (matches the documented contract).
	// Other transport failures (e.g. connection refused — often a misconfig) stay codeError.
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return codeServer
	}
	return codeError
}

// writeError renders an error to stderr — never stdout, so --json stdout stays parseable,
// and never with the token (neither *APIError nor our usageError messages carry it). In
// --json mode it emits a problem object so CI can parse failures too.
func writeError(stderr io.Writer, asJSON bool, err error) {
	if !asJSON {
		fmt.Fprintln(stderr, "ccc-wiki: "+err.Error())
		return
	}
	obj := map[string]any{"message": err.Error()}
	var apiErr *wikiclient.APIError
	if errors.As(err, &apiErr) {
		obj = map[string]any{"status": apiErr.StatusCode, "code": apiErr.Code, "message": apiErr.Message}
	}
	b, _ := json.Marshal(map[string]any{"error": obj})
	fmt.Fprintln(stderr, string(b))
}
