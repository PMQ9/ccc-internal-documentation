# contact service

A small, self-contained HTTP service that serves the CCC Wiki **Contact**
form and, on submit, **emails the destination mailbox** and
(optionally) **files a GitHub issue**. It owns no database — one submission is
one email plus one issue.

Why it exists: BookStack is an upstream image with no form handler, and it
sanitizes page HTML — so the form is served here and linked from the wiki header
(see [docs/runbooks/contact-form.md](../../docs/runbooks/contact-form.md) and
[docs/architecture.md](../../docs/architecture.md)). Standard-library only: no
third-party dependencies, so the image is tiny and the CVE surface minimal.

## Endpoints

| Method + path | Purpose |
|---|---|
| `GET /contact` | the branded form (sets a CSRF cookie) |
| `POST /contact/submit` | validate → email → GitHub issue → success page |
| `GET /contact/healthz`, `GET /healthz` | liveness (always 200 if up) |
| `GET /contact/readyz`, `GET /readyz` | readiness (200 only if mail is configured) |
| `GET /` | redirects to `/contact` |

## Configuration (environment)

| Var | Default | Notes |
|---|---|---|
| `CONTACT_LISTEN` | `:8080` | bind address |
| `CONTACT_RECIPIENT` | — | fixed `To:` (never client-supplied), e.g. `cccadmin@vanderbilt.edu` |
| `MAIL_FROM_ADDRESS` | — | `From:` — a sender you can authenticate as, e.g. a Gmail (`cccwiki.contact@gmail.com`); for Gmail it must equal `MAIL_USERNAME` |
| `MAIL_FROM_NAME` | `CCC Wiki Contact` | `From:` display name |
| `CONTACT_WIKI_NAME` | `CCC Wiki` | shown on the form + in the subject prefix |
| `CONTACT_WIKI_URL` | _(empty)_ | wiki base URL; powers the masthead logo "home" link + the "Back to the wiki" link. Empty = brand renders non-interactively. The reverse of `CONTACT_URL` (which links the wiki header _at_ this form). Defaults to `APP_URL` in compose. |
| `CONTACT_ALLOWED_EMAIL_DOMAIN` | _(empty = any)_ | required submitter domain, e.g. `vanderbilt.edu` |
| `CONTACT_ALLOWED_SENDERS` | _(empty)_ | optional comma-list of exact addresses (overrides the domain) |
| `MAIL_TRANSPORT` | `smtp` | `agentmail` (recommended), `smtp` (Gmail/Brevo/SES), or `graph` (M365 send-as) |
| `AGENTMAIL_INBOX` / `AGENTMAIL_API_KEY` | — | AgentMail transport: sending inbox + API key (`AGENTMAIL_API_BASE` defaults to the public API; From is fixed to the inbox) |
| `MAIL_HOST` / `MAIL_PORT` | — / `587` | SMTP relay host/port |
| `MAIL_USERNAME` / `MAIL_PASSWORD` | — | SMTP credentials (empty username = no AUTH, for a local sink) |
| `MAIL_ENCRYPTION` | `starttls` | `starttls` (587) \| `tls` (465) \| `none` (test sink only) |
| `MS_TENANT_ID` / `MS_CLIENT_ID` / `MS_CLIENT_SECRET` / `MS_SENDER_UPN` | — | Graph transport only |
| `CONTACT_INTAKE_GITHUB_TOKEN` | _(empty = off)_ | fine-grained PAT, `issues:write` only |
| `CONTACT_GITHUB_REPO` | — | `owner/repo` |
| `CONTACT_GITHUB_API_BASE` | `https://api.github.com` | overridden in tests |
| `CONTACT_RATE_LIMIT_PER_HOUR` | `20` | per source IP — submit attempts (every parseable POST, valid or not) |
| `CONTACT_TRUST_PROXY` | `false` | `true` behind the ALB/a proxy (honor `X-Forwarded-For`) |
| `CONTACT_SECURE_COOKIE` | `false` | `true` under HTTPS (sets the CSRF cookie `Secure`); required when `CONTACT_TRUST_PROXY=true` (else `/readyz` fails) |

Mail/GitHub config is checked lazily: the service starts even when unconfigured
(serving the form, returning 503 on submit), so it never crash-loops the compose
project before credentials are filled in.

## Security model

Layered and proportionate for an internal VPN-only tool: reachable only on the
VPN, link shown only to logged-in wiki users, `@vanderbilt.edu` (or an explicit
allowlist) required, a synchronizer **CSRF** token + **honeypot** + per-IP
**rate-limit**, and a **fixed recipient** (never an open relay). It does *not*
cryptographically prove the submitter's wiki identity — that would need SAML/
BookStack integration (a documented future hardening).

## Develop / test

```bash
go test ./...        # unit tests (no network, no Docker)
go run .             # needs the env vars above; or run via the compose stack
```

Run as part of the stack: `COMPOSE_PROFILES=contact` brings it up alongside
BookStack (see `deploy/local/compose.yaml`). The integration test
(`tests/integration/bats/08_contact.bats`) exercises it end-to-end against a
[mailpit](https://github.com/axllent/mailpit) SMTP sink.
