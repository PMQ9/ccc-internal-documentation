package main

import (
	"errors"
	"testing"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

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
