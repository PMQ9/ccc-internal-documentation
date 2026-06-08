package wikiclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPartialUpdateOmitsEmptyName proves the omitempty on Name lets a partial update
// (only the body) avoid sending name:"" — which BookStack would reject as an empty
// required field. This is the wire behavior the CLI's `page update --markdown ...`
// relies on (and mirrors the API contract exercised by 09_agent_role.bats AGENT-003).
func TestPartialUpdateOmitsEmptyName(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":1,"name":"Existing","markdown":"# v2"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL, 0)
	if _, err := c.UpdatePage(context.Background(), 1, Page{Markdown: "# v2"}); err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if strings.Contains(gotBody, `"name"`) {
		t.Errorf("partial update sent a name field, want it omitted: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"markdown":"# v2"`) {
		t.Errorf("update body missing markdown: %s", gotBody)
	}
}
