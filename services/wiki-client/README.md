# wiki-client — shared BookStack agent-API client core

The shared client core for CCC's headless wiki API
([issue #27](https://github.com/PMQ9/ccc-internal-documentation/issues/27)). It wraps
BookStack's built-in REST API once — auth, retries, typed models, typed errors — so the
CLI ([#28](https://github.com/PMQ9/ccc-internal-documentation/issues/28)) and the MCP server
([#29](https://github.com/PMQ9/ccc-internal-documentation/issues/29)) **import this package
rather than each re-implementing transport**. We wrap and gate the native API; we don't reinvent it.

This is a **library** (no `main`). It is a separate Go module so the two future binaries depend on
it without dragging in each other's deps. Standard library only — no third-party dependencies (same
stance as `services/contact`), so consumers inherit zero transitive deps and the CVE surface stays
near zero.

## Configuration (environment)

| Env var | Default | Meaning |
|---|---|---|
| `WIKI_BASE_URL` | _(required)_ | Wiki base URL, e.g. `http://10.76.88.214` or `http://localhost:8080` (no `/api` suffix). |
| `WIKI_API_TOKEN` | _(required)_ | BookStack token, form `<token_id>:<secret>`. **Secret — never logged or printed.** |
| `WIKI_HTTP_TIMEOUT` | `15s` | Per-request timeout (Go duration). |
| `WIKI_MAX_RETRIES` | `3` | Retry budget on 5xx / transport errors (`0` disables). 4xx is never retried. |
| `WIKI_RETRY_BASE_DELAY` | `200ms` | Base for exponential backoff (full jitter, 5s cap). |

Tokens are issued/rotated/revoked manually per agent against an **Agent author** user — see
[`docs/runbooks/agent-api.md`](../../docs/runbooks/agent-api.md). The role is config-as-code
([`deploy/local/apply-agent-role.sh`](../../deploy/local/apply-agent-role.sh)); the token is not.

## Usage

```go
import "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"

cfg, err := wikiclient.Load()        // reads the env vars above
if err != nil { /* missing/invalid config */ }
c, err := wikiclient.New(cfg)
if err != nil { /* ... */ }

page, err := c.CreatePage(ctx, wikiclient.Page{BookID: 3, Name: "Runbook", Markdown: "# steps"})
// page.ID / page.Slug are populated from the server response (return-what-you-wrote).

var apiErr *wikiclient.APIError
if errors.As(err, &apiErr) && apiErr.StatusCode == 403 {
    // the Agent author role intentionally lacks this permission (e.g. delete) — don't retry
}
```

While the binaries (#28/#29) live in this repo (single repo, Phase 0), they import this module via a
local `replace` directive; once it's tagged, by version.

## Surface

Read/create/update for **books, chapters, and pages**, plus **attachment and image upload** (the
multipart path) — the sanctioned Agent-author surface. The `ccc-wiki` CLI
([#28](https://github.com/PMQ9/ccc-internal-documentation/issues/28), [`../wiki-cli/`](../wiki-cli/))
is the first consumer; the MCP server (#29) is the next.

## Tests

`go test ./...` (or `make wiki-client-test` from the repo root — gofmt + vet + test on the pinned
`golang:1.23-alpine`). The unit tests are network-free (`httptest`) and prove the transport contract:
auth header, retry-on-5xx, no-retry-on-4xx, error-envelope parsing, and that the token never reaches
logs or errors. The end-to-end test against a real BookStack stack is a tracked follow-up.
