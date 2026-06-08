package wikiclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListChapters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"Ch A","book_id":3},{"id":2,"name":"Ch B","book_id":3}],"total":2}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	chs, err := c.ListChapters(context.Background())
	if err != nil {
		t.Fatalf("ListChapters: %v", err)
	}
	if len(chs) != 2 || chs[0].Name != "Ch A" || chs[1].BookID != 3 {
		t.Errorf("got %+v, want 2 chapters in book 3", chs)
	}
}

func TestGetChapter(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":7,"name":"Intro","book_id":3,"slug":"intro"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	ch, err := c.GetChapter(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetChapter: %v", err)
	}
	if gotPath != "/api/chapters/7" {
		t.Errorf("path = %q, want /api/chapters/7", gotPath)
	}
	if ch.ID != 7 || ch.Slug != "intro" {
		t.Errorf("chapter = %+v, want id=7 slug=intro", ch)
	}
}

func TestCreateChapter(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"id":9,"name":"New Ch","book_id":3,"slug":"new-ch"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	ch, err := c.CreateChapter(context.Background(), Chapter{BookID: 3, Name: "New Ch"})
	if err != nil {
		t.Fatalf("CreateChapter: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/chapters" {
		t.Errorf("got %s %s, want POST /api/chapters", gotMethod, gotPath)
	}
	if !strings.Contains(string(gotBody), `"book_id":3`) {
		t.Errorf("body missing book_id: %s", gotBody)
	}
	if ch.ID != 9 || ch.Slug != "new-ch" {
		t.Errorf("chapter = %+v, want id=9 slug=new-ch", ch)
	}
}

func TestUpdateChapter(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`{"id":9,"name":"Renamed"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	ch, err := c.UpdateChapter(context.Background(), 9, Chapter{Name: "Renamed"})
	if err != nil {
		t.Fatalf("UpdateChapter: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/chapters/9" {
		t.Errorf("got %s %s, want PUT /api/chapters/9", gotMethod, gotPath)
	}
	if ch.Name != "Renamed" {
		t.Errorf("name = %q, want Renamed", ch.Name)
	}
}
