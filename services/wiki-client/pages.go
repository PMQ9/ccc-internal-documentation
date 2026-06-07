package wikiclient

import (
	"context"
	"fmt"
	"net/http"
)

// ListPages returns pages visible to the token's user (a single page of results).
func (c *Client) ListPages(ctx context.Context) ([]Page, error) {
	var env listEnvelope[Page]
	if err := c.do(ctx, http.MethodGet, "/api/pages", nil, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// GetPage fetches a single page by id (its body included).
func (c *Client) GetPage(ctx context.Context, id int64) (Page, error) {
	var p Page
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/pages/%d", id), nil, &p)
	return p, err
}

// CreatePage creates a page. Supply BookID (or ChapterID) and one of Markdown/HTML.
// The returned Page carries the server-assigned id/slug; the create produces a
// page_revisions row, same as a UI edit.
func (c *Client) CreatePage(ctx context.Context, in Page) (Page, error) {
	var p Page
	err := c.do(ctx, http.MethodPost, "/api/pages", in, &p)
	return p, err
}

// UpdatePage updates a page by id (PUT — idempotent at the protocol level). Each
// update produces a new page_revisions row, so edits are reversible.
func (c *Client) UpdatePage(ctx context.Context, id int64, in Page) (Page, error) {
	var p Page
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("/api/pages/%d", id), in, &p)
	return p, err
}
