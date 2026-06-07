package wikiclient

import (
	"context"
	"fmt"
	"net/http"
)

// ListBooks returns books visible to the token's user (a single page of results;
// BookStack paginates via count/offset — added when a consumer needs it).
func (c *Client) ListBooks(ctx context.Context) ([]Book, error) {
	var env listEnvelope[Book]
	if err := c.do(ctx, http.MethodGet, "/api/books", nil, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// GetBook fetches a single book by id.
func (c *Client) GetBook(ctx context.Context, id int64) (Book, error) {
	var b Book
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/books/%d", id), nil, &b)
	return b, err
}

// CreateBook creates a book and returns it as stored (with the server-assigned
// id and slug).
func (c *Client) CreateBook(ctx context.Context, in Book) (Book, error) {
	var b Book
	err := c.do(ctx, http.MethodPost, "/api/books", in, &b)
	return b, err
}
