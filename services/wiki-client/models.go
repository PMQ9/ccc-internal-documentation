package wikiclient

import "time"

// The data models mirror BookStack's REST entities. Only the fields the client
// reads or writes are modeled; BookStack returns more, which json.Unmarshal
// ignores. The JSON tags are the wire contract — verify them against the live API
// (a mismatch is silent: a wrong tag just unmarshals to the zero value).

// Book is a top-level container of chapters and pages.
//
// Name uses omitempty deliberately: it is required on create (the caller always supplies
// it), and on a PUT update omitempty lets a partial update — say, only the description —
// avoid sending name:"" and clearing/rejecting it. An empty name is never a legal value,
// so dropping it from the wire is always correct. Same reasoning on Chapter and Page.
type Book struct {
	ID          int64     `json:"id,omitempty"`
	Name        string    `json:"name,omitempty"`
	Slug        string    `json:"slug,omitempty"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// Page is a single wiki page. On create, supply BookID (or ChapterID) and one of
// Markdown/HTML; BookStack prefers Markdown when both are sent. Every create/update
// produces a page_revisions row, so writes are reversible (same as a UI edit).
type Page struct {
	ID            int64     `json:"id,omitempty"`
	BookID        int64     `json:"book_id,omitempty"`
	ChapterID     int64     `json:"chapter_id,omitempty"`
	Name          string    `json:"name,omitempty"`
	Slug          string    `json:"slug,omitempty"`
	Markdown      string    `json:"markdown,omitempty"`
	HTML          string    `json:"html,omitempty"`
	Priority      int       `json:"priority,omitempty"`
	RevisionCount int64     `json:"revision_count,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// Chapter groups pages within a book. (Model only in this scaffold — no methods
// yet; added when a consumer needs them.)
type Chapter struct {
	ID          int64     `json:"id,omitempty"`
	BookID      int64     `json:"book_id,omitempty"`
	Name        string    `json:"name,omitempty"`
	Slug        string    `json:"slug,omitempty"`
	Description string    `json:"description,omitempty"`
	Priority    int       `json:"priority,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// Attachment is a file (or link) attached to a page. (Model only in this scaffold.)
type Attachment struct {
	ID         int64  `json:"id,omitempty"`
	Name       string `json:"name"`
	UploadedTo int64  `json:"uploaded_to,omitempty"` // page id
	// No omitempty: false is the meaningful "this is an uploaded file, not a link"
	// value, and omitempty would drop it from the request body.
	External bool `json:"external"`
	Order    int  `json:"order,omitempty"`
}

// Image is a gallery or drawio image. (Model only in this scaffold.)
type Image struct {
	ID         int64  `json:"id,omitempty"`
	Name       string `json:"name"`
	URL        string `json:"url,omitempty"`
	Path       string `json:"path,omitempty"`
	Type       string `json:"type,omitempty"`        // "gallery" | "drawio"
	UploadedTo int64  `json:"uploaded_to,omitempty"` // page id
}

// listEnvelope is BookStack's collection wrapper: {"data":[...],"total":N}.
type listEnvelope[T any] struct {
	Data  []T   `json:"data"`
	Total int64 `json:"total"`
}
