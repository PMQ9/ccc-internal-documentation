package main

import (
	"encoding/json"
	"fmt"
	"io"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

// renderResult writes a command result to out. In --json mode it marshals the value
// verbatim (a single resource is a JSON object; a list is a top-level JSON array) using
// the wiki-client models' JSON tags as the wire contract. In human mode it prints a
// concise one-line-per-item summary. No result type carries the token, so neither mode
// can leak it.
func renderResult(out io.Writer, asJSON bool, v any) error {
	if asJSON {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(b))
		return err
	}

	switch r := v.(type) {
	case wikiclient.Book:
		fmt.Fprintf(out, "book %d  %s  (slug: %s)\n", r.ID, r.Name, r.Slug)
	case wikiclient.Chapter:
		fmt.Fprintf(out, "chapter %d  %s  (slug: %s, book: %d)\n", r.ID, r.Name, r.Slug, r.BookID)
	case wikiclient.Page:
		fmt.Fprintf(out, "page %d  %s  (slug: %s, book: %d, revisions: %d)\n", r.ID, r.Name, r.Slug, r.BookID, r.RevisionCount)
	case wikiclient.Attachment:
		fmt.Fprintf(out, "attachment %d  %s  (page: %d)\n", r.ID, r.Name, r.UploadedTo)
	case wikiclient.Image:
		fmt.Fprintf(out, "image %d  %s  (%s)\n", r.ID, r.Name, r.URL)
	case []wikiclient.Book:
		if len(r) == 0 {
			fmt.Fprintln(out, "(no books)")
			return nil
		}
		for _, b := range r {
			fmt.Fprintf(out, "%d\t%s\t%s\n", b.ID, b.Name, b.Slug)
		}
	case []wikiclient.Chapter:
		if len(r) == 0 {
			fmt.Fprintln(out, "(no chapters)")
			return nil
		}
		for _, ch := range r {
			fmt.Fprintf(out, "%d\t%s\t%s\tbook:%d\n", ch.ID, ch.Name, ch.Slug, ch.BookID)
		}
	case []wikiclient.Page:
		if len(r) == 0 {
			fmt.Fprintln(out, "(no pages)")
			return nil
		}
		for _, p := range r {
			fmt.Fprintf(out, "%d\t%s\t%s\tbook:%d\n", p.ID, p.Name, p.Slug, p.BookID)
		}
	default:
		// A programming error (a new result type without a render arm) — surface it
		// rather than printing nothing.
		return fmt.Errorf("renderResult: no human renderer for %T", v)
	}
	return nil
}
