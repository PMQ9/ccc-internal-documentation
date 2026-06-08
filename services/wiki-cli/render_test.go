package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

func TestRenderJSONSingle(t *testing.T) {
	var b bytes.Buffer
	if err := renderResult(&b, true, wikiclient.Book{ID: 3, Name: "B", Slug: "b"}); err != nil {
		t.Fatal(err)
	}
	var got wikiclient.Book
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, b.String())
	}
	if got.ID != 3 || got.Name != "B" {
		t.Errorf("round-trip = %+v", got)
	}
}

func TestRenderJSONListIsTopLevelArray(t *testing.T) {
	var b bytes.Buffer
	if err := renderResult(&b, true, []wikiclient.Book{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}); err != nil {
		t.Fatal(err)
	}
	var arr []wikiclient.Book
	if err := json.Unmarshal(b.Bytes(), &arr); err != nil {
		t.Fatalf("want a top-level JSON array: %v\n%s", err, b.String())
	}
	if len(arr) != 2 {
		t.Errorf("len = %d, want 2", len(arr))
	}
}

func TestRenderJSONNilListIsArray(t *testing.T) {
	var b bytes.Buffer
	var nilBooks []wikiclient.Book // nil slice would marshal to `null` without normalization
	if err := renderResult(&b, true, nilBooks); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != "[]" {
		t.Errorf("nil list --json = %q, want []", got)
	}
}

func TestRenderHumanEmptyListNotError(t *testing.T) {
	var b bytes.Buffer
	if err := renderResult(&b, false, []wikiclient.Page{}); err != nil {
		t.Fatalf("empty list should not error: %v", err)
	}
	if !strings.Contains(b.String(), "(no pages)") {
		t.Errorf("empty list human output = %q", b.String())
	}
}

func TestRenderHumanSingle(t *testing.T) {
	var b bytes.Buffer
	if err := renderResult(&b, false, wikiclient.Page{ID: 7, Name: "P", Slug: "p", BookID: 3, RevisionCount: 2}); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !strings.Contains(s, "page 7") || !strings.Contains(s, "revisions: 2") {
		t.Errorf("human single = %q", s)
	}
}
