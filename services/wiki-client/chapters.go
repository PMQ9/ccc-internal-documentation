package wikiclient

import (
	"context"
	"fmt"
	"net/http"
)

// ListChapters returns chapters visible to the token's user (a single page of results;
// BookStack paginates via count/offset — added when a consumer needs it).
func (c *Client) ListChapters(ctx context.Context) ([]Chapter, error) {
	var env listEnvelope[Chapter]
	if err := c.do(ctx, http.MethodGet, "/api/chapters", nil, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// GetChapter fetches a single chapter by id.
func (c *Client) GetChapter(ctx context.Context, id int64) (Chapter, error) {
	var ch Chapter
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/chapters/%d", id), nil, &ch)
	return ch, err
}

// CreateChapter creates a chapter in a book (supply BookID and Name) and returns it as
// stored (with the server-assigned id and slug).
func (c *Client) CreateChapter(ctx context.Context, in Chapter) (Chapter, error) {
	var ch Chapter
	err := c.do(ctx, http.MethodPost, "/api/chapters", in, &ch)
	return ch, err
}

// UpdateChapter updates a chapter by id (PUT — idempotent) and returns it as stored.
func (c *Client) UpdateChapter(ctx context.Context, id int64, in Chapter) (Chapter, error) {
	var ch Chapter
	err := c.do(ctx, http.MethodPut, fmt.Sprintf("/api/chapters/%d", id), in, &ch)
	return ch, err
}
