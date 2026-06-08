package main

import (
	"context"
)

func cmdAttachmentUpload(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("attachment upload")
	page := fs.Int64("page", 0, "id of the page to attach to (required)")
	name := fs.String("name", "", "attachment display name (required)")
	file := fs.String("file", "", "path to the file to upload (required)")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *page == 0 {
		return usagef("attachment upload: --page is required")
	}
	if *name == "" {
		return usagef("attachment upload: --name is required")
	}
	f, fileName, err := openFileArg(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	c, err := cc.client()
	if err != nil {
		return err
	}
	a, err := c.UploadAttachment(ctx, *page, *name, fileName, f)
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, a)
}

func cmdImageUpload(ctx context.Context, cc *cmdContext, args []string) error {
	fs := cc.newFlagSet("image upload")
	page := fs.Int64("page", 0, "id of the page the image belongs to (required)")
	name := fs.String("name", "", "image display name (required)")
	file := fs.String("file", "", "path to the image to upload (required)")
	imgType := fs.String("type", "gallery", "image type: gallery or drawio")
	if err := cc.parse(fs, args); err != nil {
		return err
	}
	if *page == 0 {
		return usagef("image upload: --page is required")
	}
	if *name == "" {
		return usagef("image upload: --name is required")
	}
	if *imgType != "gallery" && *imgType != "drawio" {
		return usagef("image upload: --type must be gallery or drawio, got %q", *imgType)
	}
	f, fileName, err := openFileArg(*file)
	if err != nil {
		return err
	}
	defer f.Close()
	c, err := cc.client()
	if err != nil {
		return err
	}
	img, err := c.UploadImage(ctx, *page, *name, *imgType, fileName, f)
	if err != nil {
		return err
	}
	return renderResult(cc.stdout, cc.g.json, img)
}
