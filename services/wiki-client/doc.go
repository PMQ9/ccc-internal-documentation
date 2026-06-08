// Package wikiclient is the shared client core for CCC's headless BookStack agent
// API (issue #27). It wraps BookStack's built-in REST API — auth, retries, typed
// data models, and typed errors — built ONCE so the CLI (#28) and the MCP server
// (#29) both import it rather than each re-implementing transport. We wrap and
// gate the native API; we do not reinvent it.
//
// It is a library, not a binary: there is no main. A consumer constructs a Client
// from environment configuration and calls the entity methods:
//
//	cfg, err := wikiclient.Load() // reads WIKI_BASE_URL, WIKI_API_TOKEN, ...
//	if err != nil {
//		// missing/invalid config
//	}
//	c, err := wikiclient.New(cfg)
//	if err != nil {
//		// ...
//	}
//	page, err := c.CreatePage(ctx, wikiclient.Page{BookID: 3, Name: "Runbook", Markdown: "# ..."})
//
// Auth is BookStack's token scheme: the Authorization header carries
// "Token <token_id>:<secret>". The token is read from the environment, never from
// a flag, and is never logged or placed in an error message.
//
// Scope: read/create/update for books, chapters, and pages, plus attachment and image
// upload (the multipart path) — the sanctioned Agent-author surface. The ccc-wiki CLI
// (#28, services/wiki-cli/) is the first consumer; the MCP server (#29) is the next.
//
// See docs/runbooks/agent-api.md and docs/architecture.md ("Headless agent API").
package wikiclient
