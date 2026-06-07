package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateIssue(t *testing.T) {
	var gotPath, gotAuth, gotBody, gotVer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVer = r.Header.Get("X-GitHub-Api-Version")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"html_url":"https://github.com/o/r/issues/1"}`))
	}))
	defer srv.Close()

	gh := newGitHubClient(&Config{GitHubToken: "tok", GitHubRepo: "o/r", GitHubAPIBase: srv.URL})
	url, err := gh.CreateIssue(context.Background(), issue{Title: "t", Body: "b", Labels: []string{"bug"}})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if url != "https://github.com/o/r/issues/1" {
		t.Errorf("html_url = %q", url)
	}
	if gotPath != "/repos/o/r/issues" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotVer != "2022-11-28" {
		t.Errorf("api version header = %q", gotVer)
	}
	if !strings.Contains(gotBody, `"bug"`) {
		t.Errorf("body missing label: %s", gotBody)
	}
}

func TestCreateIssueError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed"}`))
	}))
	defer srv.Close()

	gh := newGitHubClient(&Config{GitHubToken: "tok", GitHubRepo: "o/r", GitHubAPIBase: srv.URL})
	if _, err := gh.CreateIssue(context.Background(), issue{Title: "t"}); err == nil {
		t.Error("expected error on 422, got nil")
	}
}
