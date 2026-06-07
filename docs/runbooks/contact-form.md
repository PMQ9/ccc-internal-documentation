# Runbook — contact-form intake (issue #15)

A "Contact" page where staff/faculty send requests, bug reports, and
feedback. Each submission becomes **one email** to the CCC mailbox and, optionally,
**one GitHub issue**. This runbook is how to set it up, operate it, and (later)
take it to AWS.

- **Code:** [`services/contact/`](../../services/contact/) (Go, standard-library only).
- **Form URL:** the value of `CONTACT_URL` (local default `http://localhost:8081/contact`;
  connor-server e.g. `http://10.76.88.214:8081/contact`). A "Contact"
  link is injected into the wiki header by [`apply-brand.sh`](../../deploy/local/apply-brand.sh).
- **Config:** non-secret knobs in [`config/config.md`](../../config/config.md) §1b;
  secrets in [`config/.env.example`](../../config/.env.example) §5 / `deploy/local/.env`.
- **Look + return path:** the page is a styled **look-alike** of the wiki (same black
  masthead, gold-flat rule, CCC lockup, "CCC Wiki" name, favicon) — not a
  BookStack-native page (cross-origin + `<form>` sanitization make native embedding
  infeasible; issue #38). Its logo and a "Back to the wiki" link point at
  `CONTACT_WIKI_URL` (defaults to `APP_URL`).
- **Light/dark parity (issue #39):** the page follows the wiki's theme. The wiki
  writes its choice to a host-scoped `ccc-color-scheme` cookie (cookies span ports;
  `localStorage` doesn't), which this service reads **server-side** to render the
  initial `html.dark-mode`/`ccc-light` class (no flash). It also has its own masthead
  toggle that writes the same cookie back, so toggling here is reflected on the wiki.
  No cookie ⇒ follow the OS. Non-`HttpOnly` and separate from the `ccc_csrf` cookie.

## How it works

```
Staff (on VPN, logged into the wiki)
  → header "Contact" link → GET  /contact      (branded form + CSRF cookie)
  → POST /contact/submit
       guards: VPN + login-gated link · @vanderbilt.edu · CSRF · honeypot · body cap · rate limits · fixed recipient
       ├─ email  → To = CONTACT_RECIPIENT, Reply-To = submitter   (transport below)
       └─ issue  → GitHub (best-effort; never blocks the email)
  → success page
```

**Email contract.** Subject `[CCC Wiki] <Type> - <summary>`; header
`X-CCC-Contact-Type: bug|request|feedback|other`; `Reply-To` = the submitter (so
"Reply" reaches the person); plain-text body with Type / From / Submitted /
Page / Summary / Details.

**GitHub contract.** Title = the subject; body = the same fields, each wrapped
verbatim in a Markdown code fence (so a submission can't inject `@mentions`,
`#refs`, images, or markup into the tracker); label by type: `bug→bug`,
`request→enhancement`, `feedback→feedback`, `other→question`.

## Abuse controls (issue #41)

The form is the most-attacked surface, so it is hardened beyond the basic guards:

| Control | What it does | Knob (default) |
|---|---|---|
| Body-size cap | Request body capped at 64 KiB **before** parsing, so an oversized POST can't buffer megabytes in RAM (returns **413**). | fixed |
| Per-IP rate limit | Submissions/hour from one source IP (returns **429**). | `CONTACT_RATE_LIMIT_PER_HOUR` (20) |
| Global circuit-breaker | Aggregate submissions/hour across **all** IPs — caps total volume even when no single IP is over its limit; trips fail-safe (**429**), auto-resets. | `CONTACT_GLOBAL_RATE_LIMIT_PER_HOUR` (100) |
| GitHub daily cap | Max issues filed/24h; over the cap the **email still sends**, only the issue is skipped. | `CONTACT_GITHUB_DAILY_CAP` (50) |
| Trusted-proxy IP | Behind the ALB the client IP is read from `X-Forwarded-For` counting from the **right**, so a client can't forge it to evade limits or spoof the audit IP; the limiter map self-evicts so spoofed keys can't exhaust memory. | `CONTACT_TRUST_PROXY` (false) · `CONTACT_TRUSTED_PROXY_HOPS` (1) |
| Security headers | Every response carries a CSP, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, and `frame-ancestors 'none'` / `X-Frame-Options: DENY` (anti-clickjacking). | fixed |

Not yet done (tracked separately): an optional CAPTCHA for bot pressure and a CSP
nonce to drop `'unsafe-inline'` — see the issue-#41 follow-up.

## Choosing a transport

`MAIL_TRANSPORT` selects how mail is sent. The hard constraint: you cannot send
*as* `@vanderbilt.edu` without VUIT, and you cannot send *as* a free-webmail /
Proton address through a relay (their **DMARC** policy makes the relay fail
authentication — Brevo, SendGrid, etc. will refuse the sender). So pick a sender
the transport is actually allowed to authenticate.

| `MAIL_TRANSPORT` | Sender (`From`) | Setup | Cost / IT | Deliverability |
|---|---|---|---|---|
| **`agentmail`** (recommended) | `…@agentmail.to` inbox | API key | Free, no IT | Good — AgentMail authenticates `agentmail.to` (SPF/DKIM/DMARC) |
| `smtp` — Gmail App Password | your Gmail | 2FA + app password | Free, no IT | Good — Google signs its own outbound |
| `smtp` — own domain + Brevo | `you@yourdomain` | buy domain + DNS records | ~$10/yr, no IT | Good — you authenticate the domain |
| `smtp` — AWS SES | SES-verified identity | AWS account + verify | ~free, no IT | Good (Phase 1; see below) |
| `graph` — Microsoft 365 | a real M365 mailbox | Entra app registration | needs VUIT/admin | Best (true `@vanderbilt.edu`) |

Dead ends (documented so they aren't retried): **Proton free** can't relay (no
SMTP; Bridge is paid) and has no OAuth send API; **Proton/any free-webmail via a
relay** is refused on DMARC; **self-hosting an MTA on connor-server** is blocked
by campus outbound port 25 + IP reputation.

In every case `To` = the CCC mailbox and `Reply-To` = the submitter, so even if
`From` isn't a Vanderbilt address, replies go to the right person and the Outlook
rule files by subject/header (not `From`).

## Set it up (connor-server, recommended = AgentMail)

1. **Get an AgentMail API key** at <https://console.agentmail.to> for the inbox
   (`ccc-3278@agentmail.to`). Optionally set the inbox **display name** (e.g.
   "CCC Wiki Contact") there — it becomes the `From` display name.
2. **Edit the live `.env`** on connor-server (hand-maintained, never overwritten
   by a deploy — same as `APP_KEY`):
   ```
   MAIL_TRANSPORT=agentmail
   AGENTMAIL_INBOX=ccc-3278@agentmail.to
   AGENTMAIL_API_KEY=am_...            # from the console; never commit
   CONTACT_RECIPIENT=cccadmin@vanderbilt.edu
   CONTACT_URL=http://10.76.88.214:8081/contact
   CONTACT_BIND=8081
   ```
   (Optional GitHub issues: `CONTACT_GITHUB_REPO=PMQ9/ccc-internal-documentation`
   and `CONTACT_INTAKE_GITHUB_TOKEN=github_pat_...` — a fine-grained PAT scoped to
   that repo, `Issues: read and write` only.)
3. **Deploy:** `make deploy` (or push to `main` with auto-deploy on). `dev-up.sh`
   brings the `contact` service up (compose profile) and `apply-brand.sh` injects
   the header link from `CONTACT_URL`.
4. **Verify:** open `CONTACT_URL`, submit one of each type; confirm it lands in
   `cccadmin` and that "Reply" addresses the submitter. `GET /contact/readyz`
   returns 200 only when a transport is fully configured.

**Switch transport** anytime by changing `.env` (e.g. to a Gmail App Password:
`MAIL_TRANSPORT=smtp`, `MAIL_HOST=smtp.gmail.com`, `MAIL_PORT=587`,
`MAIL_ENCRYPTION=starttls`, `MAIL_USERNAME`=the Gmail, `MAIL_PASSWORD`=the app
password, `MAIL_FROM_ADDRESS`=the same Gmail) and redeploy.

## File submissions into the Contact_form folder (Outlook)

In the `cccadmin` mailbox, create a rule:

- **Condition:** subject contains `[CCC Wiki]` (or, more precisely, message header
  `X-CCC-Contact-Type` exists).
- **Action:** move to `Inbox / CCC_Wiki / Contact_form`.

If mail lands in **Junk** at first (likely for an unfamiliar `@agentmail.to` or
Gmail sender into Microsoft 365), add the sender to **Safe Senders** once; the
rule then keeps it in `Contact_form`.

## Rotation

| Secret | Rotate by |
|---|---|
| `AGENTMAIL_API_KEY` | New key in the AgentMail console → update `.env` → redeploy. |
| Gmail App Password | Revoke + recreate in the Google account → update `.env` → redeploy. |
| `CONTACT_INTAKE_GITHUB_TOKEN` | Regenerate the fine-grained PAT → update `.env` → redeploy. |

Secrets live only in `.env` (gitignored; `make secrets`/gitleaks guards commits)
locally, and in AWS Secrets Manager in prod (below).

## AWS (Phase 1) — remaining work

The connor-server path above is the live deployment. The AWS footprint is
`validate`-clean but **not applied** (VUIT-gated), and the contact service's AWS
wiring is intentionally **not yet committed to Terraform** — it must be added and
verified with `terraform validate` / `terraform test` / `trivy` / `checkov`
during the AWS phase (committing unvalidated IaC would risk the shared CI). When
the AWS phase is applied, add (in `terraform/`):

1. **Secrets** (`secrets.tf`): `…/contact/agentmail_api_key` (or SMTP creds) and
   `…/contact/github_token`; grant the EC2 role `GetSecretValue` on them (`iam.tf`).
2. **Runtime** (`user-data.sh.tftpl` + `compute.tf`): add the `contact` container
   to the generated `compose.yaml`, fetch the secrets, and publish its port; set
   `CONTACT_TRUST_PROXY=true` and `CONTACT_SECURE_COOKIE=true` (behind the ALB).
3. **Routing** (`edge.tf` + `network.tf`): a listener rule `path /contact*` → a new
   target group on the contact port, an app-SG ingress for that port from the ALB,
   and attach the ASG to the new target group **only once the container is
   actually running** (an empty target group + ELB health checks would churn the
   single instance).

Then the form is reachable at `https://<wiki-domain>/contact`, VPN-gated like the
rest. **SES option:** if you prefer SES over AgentMail in prod, that's issue #22
(provision the identity + SMTP creds); the service just needs `MAIL_TRANSPORT=smtp`
+ the SES host/creds. Sending *as* `@vanderbilt.edu` from SES additionally needs
VUIT to authorize SES in Vanderbilt SPF/DKIM/DMARC.

## Troubleshooting

| Symptom | Check |
|---|---|
| Submit returns 503 | Transport not fully configured. `GET /contact/readyz`; fill `.env` (key/inbox or SMTP creds + `CONTACT_RECIPIENT`). |
| Submit returns 502 | Transport rejected the send. `docker compose logs contact` — bad API key / app password / blocked auth. |
| Submit returns 400 | Validation: non-`@vanderbilt.edu` sender, missing required field. |
| Submit returns 403 | CSRF cookie/field mismatch — reload the form (cookies enabled?). |
| Submit returns 413 | Body over the 64 KiB cap — shorten the submission. |
| Submit returns 429 | Rate limited (per-IP or the global circuit-breaker) — wait and retry; tune `CONTACT_RATE_LIMIT_PER_HOUR` / `CONTACT_GLOBAL_RATE_LIMIT_PER_HOUR`. |
| Mail not arriving | Check Junk + add the sender to Safe Senders; confirm `CONTACT_RECIPIENT`; check AgentMail/relay dashboards. |
| No header link in the wiki | `CONTACT_URL` unset in `.env`, or re-run `make apply-theme`. |
| GitHub issue not filed | Non-fatal by design — `docker compose logs contact`; verify PAT scope + `CONTACT_GITHUB_REPO`. |
