# Runbook: Headless agent API (BookStack REST)

**Implements:** the headless agent API in [docs/architecture.md](../architecture.md#headless-agent-api-the-load-bearing-bits) and issue [#27](https://github.com/PMQ9/ccc-internal-documentation/issues/27)
**Owner:** CCC maintainer (PMQ9)
**Last reviewed:** 2026-06-07

Sanctioned least-privilege headless writes ride BookStack's **built-in** REST API — no gateway
service. An agent (script, CI job, or LLM agent) authenticates with a token mapped to the
least-privilege **Agent author** role and can create/update content but cannot delete or reach
admin. This is the foundation for the CLI (#28) and the MCP server (#29), which share the Go client
core in [../../services/wiki-client/](../../services/wiki-client/). LAN-only (Phase 0) / on-VPN
(prod), deny-by-default.

## When to use this runbook

- Onboarding a new agent/script that needs to write to the wiki.
- Rotating or revoking (break-glass) an agent token.
- Confirming the API and the Agent author role are in place after a deploy.

## 1. Confirm the API is reachable

The REST API is on by default in v26.05 — no toggle. With a valid token:

```bash
curl -s -o /dev/null -w '%{http_code}\n' \
  -H "Authorization: Token <token_id>:<secret>" "$APP_URL/api/books"   # -> 200
```

A `401` means a bad/missing token; a `403` with a valid token means the user's role lacks
`access-api` (see step 2).

## 2. Confirm / (re)apply the Agent author role (config-as-code)

The role is **code**, not a UI artifact: [../../deploy/local/apply-agent-role.sh](../../deploy/local/apply-agent-role.sh)
creates it idempotently on **every** deploy, and `make apply-agent-role` applies it now without a
redeploy. It grants exactly: `access-api`; view/create/update on books, chapters, pages; image +
attachment create. It grants **no** delete, **no** shelf access, and **no** settings/users/roles
management.

- Confirm in the UI: Settings -> Roles -> "Agent author" exists with create/update (no delete).
- **Do not edit the role in the UI** — the repo is the source of truth, and a UI change is pruned
  on the next deploy. To change the grant, edit `apply-agent-role.sh` and redeploy.
- If a deploy logs `::warning::Agent author role not applied`, the script hit `MISSINGPERMS` (a
  permission slug was renamed by an upgrade); re-verify slugs against the running instance:
  ```bash
  docker compose exec -T bookstack php /app/www/artisan tinker
  >>> \BookStack\Permissions\Models\RolePermission::orderBy('name')->pluck('name');
  ```

## 3. Create an Agent-author user + issue a token (manual, per agent)

One token per agent, never shared, never committed:

1. Settings -> Users -> Add new user. Use a descriptive, non-personal name/email
   (e.g. "Agent: runbook-sync"). Assign **only** the "Agent author" role — no Admin/Editor.
2. On that user: API Tokens -> Create token. Give it a clear name and an **expiry** (not 2099).
   The secret is shown **once** — copy the Token ID + secret and hand them to the agent via a
   secret store (AWS Secrets Manager in prod; never a repo, never a log).
3. Smoke-test the token with the `GET /api/books` curl in step 1.

## 4. Rotate a token

Create a new token on the same user, update the agent's secret store, then delete the old token.
The new token is live before the old one is removed, so there's no downtime.

## 5. Revoke (break-glass)

- **Compromised token:** Settings -> Users -> _agent user_ -> API Tokens -> delete the token
  (effective immediately).
- **Kill all of an agent's access:** disable or delete the agent user.
- Tokens are local to BookStack, independent of SAML/SSO, so revocation works during an IdP outage.

## Sanctioned endpoints

This table is the canonical list (architecture.md summarizes it).

| Method | Path | Purpose | Agent author? |
|---|---|---|---|
| GET | `/api/books`, `/api/pages`, `/api/chapters` | read content | yes |
| POST / PUT | `/api/books`, `/api/chapters` | create/update container | yes |
| POST / PUT | `/api/pages` | create/update a page (produces a `page_revisions` row) | yes |
| POST | `/api/attachments`, `/api/image-gallery` | upload media (lands on the persistent volume) | yes |
| DELETE | any `/api/*` | delete content | **no — 403** |
| any | `/api/users`, `/api/roles`, settings | admin surfaces | **no — 403** |

## Clients

Two sanctioned clients share the Go core in [../../services/wiki-client/](../../services/wiki-client/):
the **`ccc-wiki` CLI** (#28) — see [../../services/wiki-cli/README.md](../../services/wiki-cli/README.md)
for commands, auth, and exit codes — and the MCP server (#29). They authenticate with an Agent-author
token exactly as above and add no capability the role doesn't grant (no delete/admin). The CLI takes
its token only from `WIKI_API_TOKEN` or a `0600` config file, never a flag.

## Access posture

LAN-only (Phase 0) / on-VPN (prod), deny-by-default — the same SG/VPN gate as the UI; `/api` is not
separately exposed. **Rate-limiting and per-request audit logging are deferred** (tracked
cybersecurity-checklist follow-up); the residual risk is that a leaked write-capable token can
vandalize content within the role's scope until revoked — mitigated by revision history + manual
revoke, not prevented.

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `401` with a token | Wrong format — the header must be `Authorization: Token <token_id>:<secret>`; or the token expired/was revoked. |
| `403` with a valid token | The role lacks the permission **by design** (e.g. delete) — do not widen the role to work around it; or the user is missing `access-api` (re-run step 2). |
| Role missing after deploy | `apply-agent-role.sh` failed — check the deploy log for `::warning::Agent author role not applied` and the `MISSINGPERMS` slug list (step 2). |
| Write returns `2xx` but no revision | Not expected — every page create/update records a `page_revisions` row; if it doesn't, file a bug (it breaks reversibility). |

## Notes

Append-only, dated.

- 2026-06-07 — Initial version (issue #27). Native BookStack REST + config-as-code Agent author
  role (`apply-agent-role.sh`); tokens issued manually per agent; rate-limiting + audit logging
  deferred to a tracked follow-up. Least-privilege verified on v26.05-ls265: create/update 2xx with
  `page_revisions` rows; delete and `/api/users` both 403.
- 2026-06-08 — Added the `ccc-wiki` CLI (#28) as a sanctioned client; usage runbook in
  [../../services/wiki-cli/README.md](../../services/wiki-cli/README.md).
