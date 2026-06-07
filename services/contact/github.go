package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// githubClient files issues via the GitHub REST API. Failure is non-fatal to a
// submission: email is the gating channel, so a GitHub outage must never block
// feedback (the caller logs and continues).
type githubClient struct {
	token   string
	repo    string // owner/repo
	apiBase string // e.g. https://api.github.com (overridden in tests)
	httpc   *http.Client
}

func newGitHubClient(c *Config) *githubClient {
	return &githubClient{
		token:   c.GitHubToken,
		repo:    c.GitHubRepo,
		apiBase: strings.TrimRight(c.GitHubAPIBase, "/"),
		httpc:   &http.Client{Timeout: 15 * time.Second},
	}
}

type issue struct {
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels,omitempty"`
}

// CreateIssue files an issue and returns its html_url.
func (g *githubClient) CreateIssue(ctx context.Context, in issue) (string, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/issues", g.apiBase, g.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.httpc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("github create issue: %s: %s", resp.Status, string(body))
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal(body, &out)
	return out.HTMLURL, nil
}
