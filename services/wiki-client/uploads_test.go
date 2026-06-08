package wikiclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestUploadAttachmentMultipart proves the attachment upload sends a well-formed
// multipart body (fields + file part), the right method/path, and the auth header, and
// that the file content round-trips through doUpload -> doRaw.
func TestUploadAttachmentMultipart(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotCT, gotName, gotUploadedTo, gotFileName, gotFileBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotName = r.FormValue("name")
		gotUploadedTo = r.FormValue("uploaded_to")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile(file): %v", err)
		}
		defer f.Close()
		gotFileName = hdr.Filename
		b, _ := io.ReadAll(f)
		gotFileBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":12,"name":"notes.txt","uploaded_to":5}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	a, err := c.UploadAttachment(context.Background(), 5, "notes.txt", "notes.txt", strings.NewReader("hello attachment"))
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/attachments" {
		t.Errorf("got %s %s, want POST /api/attachments", gotMethod, gotPath)
	}
	if gotAuth != "Token "+testToken {
		t.Errorf("Authorization = %q, want Token %s", gotAuth, testToken)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data; boundary=") {
		t.Errorf("Content-Type = %q, want multipart/form-data; boundary=...", gotCT)
	}
	if gotName != "notes.txt" || gotUploadedTo != "5" {
		t.Errorf("fields name=%q uploaded_to=%q, want notes.txt / 5", gotName, gotUploadedTo)
	}
	if gotFileName != "notes.txt" || gotFileBody != "hello attachment" {
		t.Errorf("file %q=%q, want notes.txt=hello attachment", gotFileName, gotFileBody)
	}
	if a.ID != 12 {
		t.Errorf("attachment id = %d, want 12", a.ID)
	}
}

// TestUploadImageMultipart proves the image upload sends the type field + the "image"
// file part to /api/image-gallery.
func TestUploadImageMultipart(t *testing.T) {
	var gotPath, gotType, gotFileName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotType = r.FormValue("type")
		_, hdr, err := r.FormFile("image")
		if err != nil {
			t.Fatalf("FormFile(image): %v", err)
		}
		gotFileName = hdr.Filename
		_, _ = w.Write([]byte(`{"id":3,"name":"diagram.png","url":"http://x/img.png","type":"gallery"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	img, err := c.UploadImage(context.Background(), 5, "diagram.png", "gallery", "diagram.png", strings.NewReader("PNGDATA"))
	if err != nil {
		t.Fatalf("UploadImage: %v", err)
	}
	if gotPath != "/api/image-gallery" {
		t.Errorf("path = %q, want /api/image-gallery", gotPath)
	}
	if gotType != "gallery" {
		t.Errorf("type field = %q, want gallery", gotType)
	}
	if gotFileName != "diagram.png" {
		t.Errorf("file name = %q, want diagram.png", gotFileName)
	}
	if img.URL == "" {
		t.Error("image url empty, want populated from the response")
	}
}

// TestUploadRetriesOn5xxReplaysBody is the core multipart-retry correctness test: the
// first two attempts 503, and the third must parse the SAME file bytes intact — proving
// the buffered body (and its boundary) replays across retries.
func TestUploadRetriesOn5xxReplaysBody(t *testing.T) {
	var calls atomic.Int32
	var lastBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm on replay: %v", err)
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile on replay: %v", err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		lastBody = string(b)
		_, _ = w.Write([]byte(`{"id":1,"name":"r.txt"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 3)
	_, err := c.UploadAttachment(context.Background(), 1, "r.txt", "r.txt", strings.NewReader("retry-replay-body"))
	if err != nil {
		t.Fatalf("UploadAttachment: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3 (two 503s then success)", calls.Load())
	}
	if lastBody != "retry-replay-body" {
		t.Errorf("replayed file body = %q, want retry-replay-body", lastBody)
	}
}

// TestUploadNoRetryOn4xx — a 422 is terminal on the multipart path too, mapped to *APIError.
func TestUploadNoRetryOn4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":422,"message":"The uploaded_to field is required."}}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 3)
	_, err := c.UploadAttachment(context.Background(), 0, "x", "x.txt", strings.NewReader("x"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 422 {
		t.Fatalf("err = %v, want *APIError 422", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (4xx not retried)", calls.Load())
	}
}

// TestUploadTokenNeverLogged — the no-secret-in-logs invariant holds on the multipart
// path as well (the upload routes through the same doRaw/logRetry).
func TestUploadTokenNeverLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	c, err := New(Config{
		BaseURL: srv.URL, Token: testToken,
		HTTPTimeout: 2 * time.Second, MaxRetries: 2, RetryBaseDelay: time.Millisecond,
	}, WithLogger(logger))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.UploadImage(context.Background(), 1, "n", "gallery", "n.png", strings.NewReader("x"))
	if err == nil {
		t.Fatal("expected an error after exhausting retries")
	}
	for _, secret := range []string{testToken, testSecret} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("token leaked into logs: %q", buf.String())
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("token leaked into error: %q", err.Error())
		}
	}
}
