package main

import (
	"context"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

func cmdChapterList(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("chapter list")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	chs, err := c.ListChapters(ctx)
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, chs)
}

func cmdChapterGet(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("chapter get")
	id := fs.Int64("id", 0, "chapter id (required)")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *id == 0 {
		return usagef("chapter get: --id is required")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	ch, err := c.GetChapter(ctx, *id)
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, ch)
}

func cmdChapterCreate(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("chapter create")
	book := fs.Int64("book", 0, "id of the book to create the chapter in (required)")
	name := fs.String("name", "", "chapter name (required)")
	desc := fs.String("description", "", "chapter description")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *book == 0 {
		return usagef("chapter create: --book is required")
	}
	if *name == "" {
		return usagef("chapter create: --name is required")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	ch, err := c.CreateChapter(ctx, wikiclient.Chapter{BookID: *book, Name: *name, Description: *desc})
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, ch)
}

func cmdChapterUpdate(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("chapter update")
	id := fs.Int64("id", 0, "chapter id (required)")
	name := fs.String("name", "", "new chapter name")
	desc := fs.String("description", "", "new chapter description")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *id == 0 {
		return usagef("chapter update: --id is required")
	}
	if *name == "" && *desc == "" {
		return usagef("chapter update: nothing to change (set --name and/or --description)")
	}
	c, err := cc.client()
	if err != nil {
		return err
	}
	ch, err := c.UpdateChapter(ctx, *id, wikiclient.Chapter{Name: *name, Description: *desc})
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, ch)
}
