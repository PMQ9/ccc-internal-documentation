package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

// fakeTimeout is a net.Error that reports a timeout — what http.Client.Do surfaces
// (wrapped in *url.Error) when the per-request timeout fires.
type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, codeOK},
		{"usage", usagef("bad flag"), codeUsage},
		{"auth401", &wikiclient.APIError{StatusCode: 401}, codeAuth},
		{"forbidden403", &wikiclient.APIError{StatusCode: 403}, codeForbidden},
		{"notfound404", &wikiclient.APIError{StatusCode: 404}, codeForbidden},
		{"server500", &wikiclient.APIError{StatusCode: 500}, codeServer},
		{"server503", &wikiclient.APIError{StatusCode: 503}, codeServer},
		{"client422", &wikiclient.APIError{StatusCode: 422}, codeError},
		// Transport timeouts (post-retry) are infra, not caller error -> codeServer.
		{"ctx-deadline", fmt.Errorf("wikiclient: GET /api/books: %w", context.DeadlineExceeded), codeServer},
		{"net-timeout", fmt.Errorf("wikiclient: POST /api/pages: %w", fakeTimeout{}), codeServer},
		{"other", errors.New("boom"), codeError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.err); got != tc.want {
				t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
