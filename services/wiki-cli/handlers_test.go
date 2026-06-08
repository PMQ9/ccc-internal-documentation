package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

// testCC builds a cmdContext whose client is a real wiki-client pointed at an httptest
// server (so handler tests exercise the real wire format) with the given stdin.
func testCC(t *testing.T, serverURL string, asJSON bool, stdin string) (*cmdContext, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	cl, err := wikiclient.New(wikiclient.Config{BaseURL: serverURL, Token: "tid:sec", MaxRetries: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	g := newGlobalFlags()
	g.json = asJSON
	var out, errb bytes.Buffer
	cc := &cmdContext{
		g: g, stdout: &out, stderr: &errb, stdin: strings.NewReader(stdin),
		getenv: envFrom(nil), cached: cl, resolved: true,
	}
	return cc, &out, &errb
}

func TestPageCreateBodyFromStdin(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":5,"name":"From Stdin","book_id":1}`))
	}))
	defer srv.Close()

	cc, out, _ := testCC(t, srv.URL, true, "# from stdin\n")
	if err := cmdPageCreate(context.Background(), cc, []string{"--book", "1", "--name", "From Stdin", "--markdown-file", "-"}); err != nil {
		t.Fatalf("cmdPageCreate: %v", err)
	}
	if !strings.Contains(gotBody, "from stdin") {
		t.Errorf("request body missing stdin content: %s", gotBody)
	}
	if !strings.Contains(out.String(), `"id": 5`) {
		t.Errorf("json output missing id: %s", out.String())
	}
}

func TestPageCreateBothBodiesRejected(t *testing.T) {
	cc, _, _ := testCC(t, "http://unused", false, "")
	err := cmdPageCreate(context.Background(), cc, []string{"--book", "1", "--name", "X", "--markdown", "a", "--html", "b"})
	if err == nil {
		t.Fatal("expected a mutual-exclusion error for both body forms")
	}
	if exitCode(err) != codeUsage {
		t.Errorf("exit = %d, want %d", exitCode(err), codeUsage)
	}
}

func TestPageCreateMissingBodyRejected(t *testing.T) {
	cc, _, _ := testCC(t, "http://unused", false, "")
	err := cmdPageCreate(context.Background(), cc, []string{"--book", "1", "--name", "X"})
	if exitCode(err) != codeUsage {
		t.Errorf("missing body exit = %d, want %d (%v)", exitCode(err), codeUsage, err)
	}
}

func TestPageCreateMissingNameRejected(t *testing.T) {
	cc, _, _ := testCC(t, "http://unused", false, "")
	err := cmdPageCreate(context.Background(), cc, []string{"--book", "1", "--markdown", "x"})
	if exitCode(err) != codeUsage {
		t.Errorf("missing name exit = %d, want %d", exitCode(err), codeUsage)
	}
}

func TestAttachmentUploadMultipart(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(fp, []byte("file content here"), 0o600); err != nil {
		t.Fatal(err)
	}
	var gotName, gotFile, gotUploadedTo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotName = r.FormValue("name")
		gotUploadedTo = r.FormValue("uploaded_to")
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		gotFile = string(b)
		_, _ = w.Write([]byte(`{"id":9,"name":"notes.txt","uploaded_to":3}`))
	}))
	defer srv.Close()

	cc, out, _ := testCC(t, srv.URL, true, "")
	if err := cmdAttachmentUpload(context.Background(), cc, []string{"--page", "3", "--name", "notes.txt", "--file", fp}); err != nil {
		t.Fatalf("cmdAttachmentUpload: %v", err)
	}
	if gotName != "notes.txt" || gotUploadedTo != "3" || gotFile != "file content here" {
		t.Errorf("multipart got name=%q uploaded_to=%q file=%q", gotName, gotUploadedTo, gotFile)
	}
	if !strings.Contains(out.String(), `"id": 9`) {
		t.Errorf("json output missing id: %s", out.String())
	}
}

func TestPageUpdateMovesBook(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":5,"name":"P","book_id":9}`))
	}))
	defer srv.Close()

	cc, _, _ := testCC(t, srv.URL, true, "")
	if err := cmdPageUpdate(context.Background(), cc, []string{"--id", "5", "--book", "9"}); err != nil {
		t.Fatalf("cmdPageUpdate: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/api/pages/5" {
		t.Errorf("got %s %s, want PUT /api/pages/5", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"book_id":9`) {
		t.Errorf("update body missing the book_id move: %s", gotBody)
	}
}

func TestPageCreateBookAndChapterRejected(t *testing.T) {
	cc, _, _ := testCC(t, "http://unused", false, "")
	err := cmdPageCreate(context.Background(), cc, []string{"--book", "1", "--chapter", "2", "--name", "X", "--markdown", "y"})
	if exitCode(err) != codeUsage {
		t.Errorf("both --book and --chapter (create): exit %d, want %d (%v)", exitCode(err), codeUsage, err)
	}
}

func TestPageUpdateBookAndChapterRejected(t *testing.T) {
	cc, _, _ := testCC(t, "http://unused", false, "")
	err := cmdPageUpdate(context.Background(), cc, []string{"--id", "5", "--book", "1", "--chapter", "2"})
	if exitCode(err) != codeUsage {
		t.Errorf("both --book and --chapter (update): exit %d, want %d (%v)", exitCode(err), codeUsage, err)
	}
}

func TestPageCreateEmptyBodyFileRejected(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(fp, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	cc, _, _ := testCC(t, "http://unused", false, "")
	err := cmdPageCreate(context.Background(), cc, []string{"--book", "1", "--name", "X", "--markdown-file", fp})
	if exitCode(err) != codeUsage {
		t.Errorf("empty body file: exit %d, want %d (%v)", exitCode(err), codeUsage, err)
	}
}

func TestImageUploadBadTypeRejected(t *testing.T) {
	fp := filepath.Join(t.TempDir(), "x.png")
	if err := os.WriteFile(fp, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cc, _, _ := testCC(t, "http://unused", false, "")
	err := cmdImageUpload(context.Background(), cc, []string{"--page", "1", "--name", "x", "--file", fp, "--type", "bogus"})
	if exitCode(err) != codeUsage {
		t.Errorf("bad --type exit = %d, want %d (%v)", exitCode(err), codeUsage, err)
	}
}

func TestBookListHumanEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"total":0}`))
	}))
	defer srv.Close()

	cc, out, _ := testCC(t, srv.URL, false, "")
	if err := cmdBookList(context.Background(), cc, nil); err != nil {
		t.Fatalf("cmdBookList: %v", err)
	}
	if !strings.Contains(out.String(), "(no books)") {
		t.Errorf("human empty list = %q", out.String())
	}
}
