package wikiclient

import "fmt"

// APIError is a non-2xx response from BookStack, mapped to a typed value so callers
// switch on it (via errors.As) instead of string-matching a message. It carries
// both the HTTP status and BookStack's own error envelope ({"error":{"code","message"}}).
//
// Its Error() string is built only from method, path, status, and the server's
// message — never from the token or the full request URL — so logging an APIError
// cannot leak the credential.
type APIError struct {
	StatusCode int    // HTTP status, e.g. 404, 422, 503
	Code       int    // BookStack envelope "error.code" (0 if absent/unparseable)
	Message    string // BookStack envelope "error.message", else a status-derived fallback
	Method     string // request method, for diagnostics ("POST")
	Path       string // request path WITHOUT host or query ("/api/pages")
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bookstack %s %s: %d (code %d): %s", e.Method, e.Path, e.StatusCode, e.Code, e.Message)
}

// Retryable reports whether the request might succeed if retried. Only 5xx is
// retryable; a 4xx is a deterministic caller error (bad input, missing auth, or a
// permission the least-privilege role intentionally lacks) that a retry won't fix.
func (e *APIError) Retryable() bool {
	return e.StatusCode >= 500
}
