package main

import (
	"context"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

func cmdBookList(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("book list")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	books, err := c.ListBooks(ctx)
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, books)
}

func cmdBookGet(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("book get")
	id := fs.Int64("id", 0, "book id (required)")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *id == 0 {
		return usagef("book get: --id is required")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	b, err := c.GetBook(ctx, *id)
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, b)
}

func cmdBookCreate(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("book create")
	name := fs.String("name", "", "book name (required)")
	desc := fs.String("description", "", "book description")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *name == "" {
		return usagef("book create: --name is required")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	b, err := c.CreateBook(ctx, wikiclient.Book{Name: *name, Description: *desc})
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, b)
}

func cmdBookUpdate(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("book update")
	id := fs.Int64("id", 0, "book id (required)")
	name := fs.String("name", "", "new book name")
	desc := fs.String("description", "", "new book description")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *id == 0 {
		return usagef("book update: --id is required")
	}
	if *name == "" && *desc == "" {
		return usagef("book update: nothing to change (set --name and/or --description)")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	// Name uses omitempty in the model, so an empty --name is not sent (partial update).
	b, err := c.UpdateBook(ctx, *id, wikiclient.Book{Name: *name, Description: *desc})
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, b)
}
