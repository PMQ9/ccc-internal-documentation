package main

import (
	"context"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

func cmdPageList(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("page list")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	pages, err := c.ListPages(ctx)
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, pages)
}

func cmdPageGet(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("page get")
	id := fs.Int64("id", 0, "page id (required)")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *id == 0 {
		return usagef("page get: --id is required")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	p, err := c.GetPage(ctx, *id)
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, p)
}

func cmdPageCreate(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("page create")
	book := fs.Int64("book", 0, "id of the book to create the page in")
	chapter := fs.Int64("chapter", 0, "id of the chapter to create the page in (instead of --book)")
	name := fs.String("name", "", "page name (required)")
	md := fs.String("markdown", "", "page body as Markdown")
	mdFile := fs.String("markdown-file", "", "read Markdown body from a file ('-' = stdin)")
	html := fs.String("html", "", "page body as HTML")
	htmlFile := fs.String("html-file", "", "read HTML body from a file ('-' = stdin)")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *name == "" {
		return usagef("page create: --name is required")
	}
	if *book == 0 && *chapter == 0 {
		return usagef("page create: one of --book or --chapter is required")
	}
	mdBody, mdSet, err := readSource(*md, *mdFile, cc.stdin)
	if err != nil {
		return err
	}
	htmlBody, htmlSet, err := readSource(*html, *htmlFile, cc.stdin)
	if err != nil {
		return err
	}
	if mdSet && htmlSet {
		return usagef("page create: provide either Markdown or HTML, not both")
	}
	if !mdSet && !htmlSet {
		return usagef("page create: a body is required (--markdown/--markdown-file or --html/--html-file)")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	p, err := c.CreatePage(ctx, wikiclient.Page{
		BookID: *book, ChapterID: *chapter, Name: *name, Markdown: mdBody, HTML: htmlBody,
	})
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, p)
}

func cmdPageUpdate(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("page update")
	id := fs.Int64("id", 0, "page id (required)")
	name := fs.String("name", "", "new page name")
	book := fs.Int64("book", 0, "move the page to this book")
	chapter := fs.Int64("chapter", 0, "move the page to this chapter")
	md := fs.String("markdown", "", "new page body as Markdown")
	mdFile := fs.String("markdown-file", "", "read new Markdown body from a file ('-' = stdin)")
	html := fs.String("html", "", "new page body as HTML")
	htmlFile := fs.String("html-file", "", "read new HTML body from a file ('-' = stdin)")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *id == 0 {
		return usagef("page update: --id is required")
	}
	mdBody, mdSet, err := readSource(*md, *mdFile, cc.stdin)
	if err != nil {
		return err
	}
	htmlBody, htmlSet, err := readSource(*html, *htmlFile, cc.stdin)
	if err != nil {
		return err
	}
	if mdSet && htmlSet {
		return usagef("page update: provide either Markdown or HTML, not both")
	}
	if *name == "" && *book == 0 && *chapter == 0 && !mdSet && !htmlSet {
		return usagef("page update: nothing to change (set --name, --book, --chapter, or a body)")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	// Name uses omitempty, so an empty --name is not sent; the update is partial.
	p, err := c.UpdatePage(ctx, *id, wikiclient.Page{
		BookID: *book, ChapterID: *chapter, Name: *name, Markdown: mdBody, HTML: htmlBody,
	})
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, p)
}
