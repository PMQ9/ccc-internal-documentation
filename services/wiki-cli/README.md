# ccc-wiki — headless CLI for the wiki

`ccc-wiki` is a command-line client for the CCC BookStack wiki
([issue #28](https://github.com/PMQ9/ccc-internal-documentation/issues/28)). It is a **thin client
over the shared Go core** in [`../wiki-client/`](../wiki-client/) — it reuses that core's auth,
retries, typed models, and typed errors rather than re-implementing transport. Built for humans *and*
CI: scriptable subcommands, a `--json` mode, and exit codes that map to the failure class.

Standard-library only (no third-party deps): while in-repo it imports the core via a local `replace`
directive, so it inherits zero transitive dependencies and the CVE surface stays near zero — same
stance as [`../wiki-client/`](../wiki-client/) and [`../contact/`](../contact/).

## Build

```bash
make wiki-cli-build           # -> services/wiki-cli/bin/ccc-wiki (static, via the pinned go image)
# or, with a local Go toolchain:
cd services/wiki-cli && go build -o ccc-wiki .
```

## Auth (env or config file — never a flag)

The token is read from the environment or a config file, **never a `--token` flag** — a flag would
leak the secret into the process table, shell history, and CI logs. The token is never printed or logged.

| Source | Var / key | Notes |
|---|---|---|
| env (recommended for CI) | `WIKI_BASE_URL` | wiki base URL, e.g. `http://10.76.88.214` (no `/api` suffix). Also `--base-url`. |
| env | `WIKI_API_TOKEN` | BookStack token `<id>:<secret>`. **Secret.** |
| env (optional) | `WIKI_HTTP_TIMEOUT`, `WIKI_MAX_RETRIES` | per-request timeout / retry budget. Also `--timeout`, `--max-retries`. |
| config file | `WIKI_BASE_URL`, `WIKI_API_TOKEN` | `KEY=VALUE` lines. Path: `--config`, else `$CCC_WIKI_CONFIG`, else `~/.config/ccc-wiki/config`. |

Resolution order: **base-url** = `--base-url` > env > file; **token** = env > file. A config file that
holds a token **must be mode `0600`** (no group/other access) or the CLI refuses to use it — like ssh
`StrictModes`. Tokens are issued per agent against the least-privilege **Agent author** role; see
[`docs/runbooks/agent-api.md`](../../docs/runbooks/agent-api.md).

## Commands

`ccc-wiki [global flags] <resource> <action> [action flags]`. The surface is exactly the
sanctioned-endpoints set of the Agent author role — read/create/update plus media upload. There is
**no delete or admin command** (the role never grants them).

| Command | Flags |
|---|---|
| `book list` | — |
| `book get` | `--id N` |
| `book create` | `--name S` [`--description S`] |
| `book update` | `--id N` [`--name S`] [`--description S`] |
| `chapter list` / `get --id N` | — / `--id N` |
| `chapter create` | `--book N --name S` [`--description S`] |
| `chapter update` | `--id N` [`--name S`] [`--description S`] |
| `page list` / `get --id N` | — / `--id N` |
| `page create` | `--book N`\|`--chapter N --name S` (`--markdown S`\|`--markdown-file P`\|`--html S`\|`--html-file P`) |
| `page update` | `--id N` [`--name S`] [`--book N`] [`--chapter N`] [body flags] |
| `attachment upload` | `--page N --name S --file P` |
| `image upload` | `--page N --name S --file P` [`--type gallery`\|`drawio`] |

A `--*-file` value of `-` reads the body from **stdin**. Global flags: `--json`, `--base-url`,
`--timeout`, `--max-retries`, `--config`, plus `help` and `version`.

## Output and exit codes

Default output is a concise human summary; `--json` emits the resource as JSON (a single resource is
an object; a list is a top-level array). Errors always go to **stderr** (so `--json` stdout stays
parseable) and never contain the token.

| Exit | Meaning |
|---|---|
| `0` | success |
| `2` | usage/config error (bad flags, missing/malformed token, loose config-file perms) |
| `3` | auth — `401` (token missing/invalid) |
| `4` | forbidden/not-found — `403` (role lacks the permission **by design** — do not widen the role) or `404` |
| `5` | server — `5xx` after the retry budget, or a timeout |
| `1` | any other error |

## Examples

```bash
export WIKI_BASE_URL=http://10.76.88.214
export WIKI_API_TOKEN='<id>:<secret>'

# Create a page from a Markdown file in a repo, machine-readable:
ccc-wiki --json page create --book 3 --name "Deploy runbook" --markdown-file ./runbook.md

# Push a runbook straight from a pipeline (body from stdin):
generate-notes | ccc-wiki page create --book 3 --name "Release notes" --markdown-file -

# Attach a file to a page:
ccc-wiki attachment upload --page 42 --name diagram.txt --file ./diagram.txt
```

## Security posture

Token via env or a `0600` config file, never a flag, never printed. No delete/admin commands (surface
== the sanctioned endpoints). No insecure/skip-TLS escape hatch — HTTP vs HTTPS is driven solely by
`WIKI_BASE_URL`. Access is LAN-only (Phase 0) / on-VPN (prod), deny-by-default, inherited from #27
(see [`docs/runbooks/agent-api.md`](../../docs/runbooks/agent-api.md)). File inputs are read at the
operator's own privilege; upload bodies are buffered in memory for retry, so very large files are a
footgun (set `WIKI_MAX_RETRIES=0` to avoid replay).

## Develop / test

```bash
make wiki-cli-test     # gofmt + vet + go test (no network/deps, pinned go image)
```

The unit tests are network-free (`httptest`) and include `TestTokenNeverInOutput` — the fitness
function that proves the token never reaches stdout or stderr on any path. The end-to-end test
([`tests/integration/bats/12_cli.bats`](../../tests/integration/bats/12_cli.bats)) builds the binary
and drives it against a live stack: create + update a page (which produces a `page_revisions` row) and
verify the auth/not-found exit codes.
