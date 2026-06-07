# CCC Wiki — Centralized configuration

The single control panel for every variable, secret, token, and knob in the
project. **Two files:**

| File | Holds | Committed? |
|---|---|---|
| [.env.example](.env.example) | Secrets + per-deployment values (template — `cp` to `.env`, fill in) | yes (template only) |
| `config.md` (this) | All non-secret tunables, **and how to apply everything** | yes |

> **Why a template, not a real `.env`?** `.env` and any `*.env` are gitignored —
> committing real secrets would leak them. The committed artifact is always the
> `*.example` template; the real `.env` stays local. Matches the repo's existing
> pattern (`deploy/local/.env.example`, `terraform.tfvars.example`).

Nothing reads these two files automatically — they are the **map**. Each value is
applied to the consumer that actually uses it, via one of four mechanisms:

| Mechanism | Used for | Rows |
|---|---|---|
| Local file (`.env`, `tfvars`, `backend.hcl`) | local stack + Terraform inputs | §1, §4, §5 + [.env.example](.env.example) |
| `gh variable set` | GitHub Actions repo Variables | §3 |
| `terraform apply` (automatic) | most AWS secrets + all SSM params | §6 + [.env.example](.env.example) §3 |
| `aws secretsmanager` / `aws ssm` (manual) | VUIT secrets + live config edits | [.env.example](.env.example) §3 + §6 |

---

## How to apply

### Local stack (connor-server / your laptop)

```bash
cp config/.env.example config/.env     # create your real env file (gitignored), then fill it in
cp config/.env deploy/local/.env       # the Docker stack reads deploy/local/.env (same keys)
cd deploy/local && ./dev-up.sh         # generates a local .env if absent
```

Only three secrets are mandatory: `APP_KEY` (generate once — command is in
[.env.example](.env.example), then never change it), `DB_ROOT_PASSWORD`,
`DB_PASSWORD`. Everything else has a working default.

### GitHub (Actions Variables & Secrets)

The values in §3 are **Variables**, not Secrets (none are sensitive). Use `gh`
or **Settings → Secrets and variables → Actions**:

```bash
gh variable set AWS_ROLE_ARN   --body "arn:aws:iam::<acct>:role/<role>"
gh variable set AWS_REGION     --body "us-east-1"
gh variable set TFSTATE_BUCKET --body "ccc-wiki-tfstate-<suffix>"
gh variable set DEPLOY_CONNOR_ENABLED --body "true"   # activate auto-deploy LAST, after the runner exists
```

**Secrets:** none required today — CI uses the auto-injected `github.token` and
authenticates to AWS via the OIDC role above. Future-feature credentials (SES,
contact-page PAT, BookStack API tokens) live in **AWS Secrets Manager**, not
GitHub, because the running app uses them — not CI.

### AWS (Secrets Manager, SSM, Terraform inputs)

Most secrets are generated for you: `terraform apply` creates `app-key`,
`breakglass-admins`, and the SSM parameters; RDS manages its own master password.

```bash
cd terraform
cp terraform.tfvars.example terraform.tfvars   # fill in VUIT-gated values (§4)
cp backend.hcl.example     backend.hcl          # fill in the state bucket (§5)
terraform init -backend-config=backend.hcl

# VUIT-supplied SAML secrets (Phase 2; placeholders until then):
aws secretsmanager put-secret-value --secret-id ccc-wiki/saml/idp_x509 --secret-string "$(cat idp-cert.b64)"
aws secretsmanager put-secret-value --secret-id ccc-wiki/saml/sp_key   --secret-string "$(cat sp-key.pem)"

# Live config edit without a redeploy (then roll the ASG so user-data re-reads it):
aws ssm put-parameter --name /ccc-wiki/auth_method --value saml2 --overwrite
```

Full procedures: [VUIT coordination](../docs/runbooks/vuit-coordination-checklist.md),
[SAML cert rotation](../docs/runbooks/saml-cert-rotation.md),
[break-glass admin](../docs/runbooks/break-glass-admin.md).

### Safety rules

- **Never commit a real `.env`** — only the `*.example` template (`.gitignore`
  enforces it; `make secrets` scans for leaks in CI).
- **Never restate the image pins** (`BOOKSTACK_TAG`, …) as an editable copy — they
  are lockstep-enforced from a canonical source (§0). Bump there, run `make pins`.
- **`APP_KEY` is generate-once** — changing it invalidates every session + all 2FA.
- **`vpn_ingress_cidrs` is the production access control** — empty = ALB fully
  closed (fails safe). Set it before `terraform apply`.

---

# Registry

Rule of thumb: a value belongs here if it is the same for everyone running the
same phase. If it is a credential, or differs per host/operator, it belongs in
`.env` ([.env.example](.env.example)), not here. Each row says where the value is
**consumed** and where you **change** it so it takes effect.

## 0) Version pins — REFERENCE ONLY, do not edit here

These are deliberately mirrored across several files and kept in lockstep by a
fitness function: [tests/lib/check_pins.sh](../tests/lib/check_pins.sh) (run via
`make pins`, enforced in CI). Adding an editable copy here would create a mirror
the gate does not know about, which would silently drift on the next bump — so
they are listed for visibility only. Bump at the **canonical** source, then run
`make pins`. Upgrade procedure: [docs/runbooks/bookstack-upgrade.md](../docs/runbooks/bookstack-upgrade.md).

| Pin | Current | Canonical source |
|---|---|---|
| `BOOKSTACK_TAG` | `v26.05-ls265` | [tests/integration/run.sh](../tests/integration/run.sh) |
| `MARIADB_TAG` | `11.4.12-r0-ls220` | [tests/integration/run.sh](../tests/integration/run.sh) |
| `docker_compose_version` | `2.32.4` | [terraform/variables.tf](../terraform/variables.tf) (prod boot) |
| TF / tflint / trivy / checkov / shellcheck / actionlint / gitleaks / lychee | (see file) | [Makefile](../Makefile) + `.github/workflows/*` |
| GitHub Action versions | by commit SHA | `.github/workflows/*` (Dependabot-updated) |

The image pins are also mirrored (by design) in [deploy/local/.env.example](../deploy/local/.env.example),
[terraform/variables.tf](../terraform/variables.tf), [tests/lib/render_user_data.sh](../tests/lib/render_user_data.sh),
and [.github/workflows/weekly.yml](../.github/workflows/weekly.yml).

## 1) Local stack behavior — change in `deploy/local/.env`

Read by [deploy/local/compose.yaml](../deploy/local/compose.yaml). Defaults shown are the compose fallbacks.

| Key | Default | Notes |
|---|---|---|
| `TZ` | `America/Chicago` | IANA timezone for both containers. |
| `APP_NAME` | `CCC Wiki` | Site name in header + browser title; also drives branding (§7). |
| `AUTH_METHOD` | `standard` | `standard` = local DB login, `saml2` = SSO smoke test. |
| `AUTH_AUTO_INITIATE` | `false` | Keep `false` so the `/login` break-glass form stays reachable. |
| `REVISION_LIMIT` | `false` | `false` = unlimited page history; an integer caps it. |
| `DB_DATABASE` | `bookstackapp` | BookStack database name. |
| `DB_USERNAME` | `bookstack` | BookStack database user. |
| `QUEUE_CONNECTION` | `database` | BookStack job-queue backend (hardcoded in compose.yaml; tunable). |
| `DB_HOST` | `db` | Local: the `db` container. Prod: the RDS address (set by Terraform). |
| `DB_PORT` | `3306` | MySQL/MariaDB port (hardcoded in compose.yaml). |
| `PUID` / `PGID` | `1000` / `1000` | Host uid/gid the container runs as; load-bearing for volume file ownership (hardcoded in compose.yaml). |

**SAML2 attribute mapping** (only when `AUTH_METHOD=saml2`; names mirror prod):

| Key | Default |
|---|---|
| `SAML2_NAME` | `Vanderbilt SSO` |
| `SAML2_EMAIL_ATTRIBUTE` | `mail` |
| `SAML2_DISPLAY_NAME_ATTRIBUTES` | `givenName\|sn` |
| `SAML2_EXTERNAL_ID_ATTRIBUTE` | `eduPersonPrincipalName` |
| `SAML2_USER_TO_GROUPS` | `false` (group→role sync OFF until the real released attribute is confirmed) |

## 1b) Contact-form service (issue #15) — change in `deploy/local/.env`

The `services/contact` container (run behind the `contact` compose profile;
`dev-up.sh` enables it) serves the wiki's **Contact** page, emails
`CONTACT_RECIPIENT`, and optionally files a GitHub issue. No-IT delivery uses
**AgentMail** (an email API for agents — recommended; sends from your
`agentmail.to` inbox with SPF/DKIM/DMARC handled, API-key auth, free tier).
Alternatives — SMTP (Gmail App Password / own-domain + Brevo / AWS SES) and M365
Graph — are in the [contact-form runbook](../docs/runbooks/contact-form.md). Secrets
(`MAIL_USERNAME`/`MAIL_PASSWORD`, `CONTACT_INTAKE_GITHUB_TOKEN`,
`MS_CLIENT_SECRET`) live in `.env` ([.env.example](.env.example) §5), not here.

| Key | Default | Notes |
|---|---|---|
| `CONTACT_URL` | `http://localhost:8081/contact` | Where the form is served; the header link (applied by `apply-brand.sh`) points here. On connor-server use the LAN URL, e.g. `http://10.76.88.214:8081/contact`. |
| `CONTACT_BIND` | `8081` | Host port the service binds (maps to container `8080`). |
| `CONTACT_RECIPIENT` | `cccadmin@vanderbilt.edu` | Fixed `To:` — submissions land here (Outlook rule → `Contact_form`). Never client-supplied. |
| `CONTACT_ALLOWED_EMAIL_DOMAIN` | `vanderbilt.edu` | Approved-sender gate; empty = any. `CONTACT_ALLOWED_SENDERS` (CSV) is an exact-address override. |
| `MAIL_TRANSPORT` | `agentmail` | `agentmail` (recommended) · `smtp` (Gmail/Brevo/SES) · `graph` (M365 send-as). |
| `AGENTMAIL_INBOX` | `ccc-3278@agentmail.to` | agentmail only — the sending inbox (also the `From`); set its display name in the AgentMail console. |
| `AGENTMAIL_API_BASE` | `https://api.agentmail.to` | agentmail only — override only for tests. (`AGENTMAIL_API_KEY` is a secret → `.env`.) |
| `MAIL_FROM_ADDRESS` | `cccwiki.contact@gmail.com` | smtp only — `From:`; for Gmail must equal `MAIL_USERNAME`. Reply-To is always the submitter. |
| `MAIL_FROM_NAME` | `CCC Wiki Contact` | smtp only — `From:` display name. |
| `MAIL_HOST` / `MAIL_PORT` / `MAIL_ENCRYPTION` | `smtp.gmail.com` / `587` / `starttls` | smtp only. SES later: `email-smtp.<region>.amazonaws.com`. |
| `CONTACT_GITHUB_REPO` | `PMQ9/ccc-internal-documentation` | Repo for auto-filed issues (label by type: bug→bug, request→enhancement, feedback→feedback, other→question). Empty token disables. |
| `CONTACT_RATE_LIMIT_PER_HOUR` | `20` | Per source IP. |
| `CONTACT_GLOBAL_RATE_LIMIT_PER_HOUR` | `100` | Aggregate circuit-breaker across all IPs — caps total submissions/hour even when no single IP is over its limit. Trips fail-safe (protects the mailbox); auto-resets. |
| `CONTACT_GITHUB_DAILY_CAP` | `50` | Max GitHub issues filed per 24h. Over the cap, the email still sends; only the issue is skipped. |
| `CONTACT_TRUST_PROXY` / `CONTACT_SECURE_COOKIE` | `false` / `false` | Set both `true` on AWS (behind the ALB, over HTTPS). |
| `CONTACT_TRUSTED_PROXY_HOPS` | `1` | Only with `CONTACT_TRUST_PROXY=true`: # of trusted proxies appending `X-Forwarded-For`. The client IP is read that-many entries from the **right** (1 = a single ALB), so a client can't forge it. |

## 2) Deploy script knobs — change in shell env when invoking the script

Toggles for [dev-up.sh](../deploy/local/dev-up.sh) / [deploy-remote.sh](../deploy/local/deploy-remote.sh) /
[snapshot.sh](../deploy/local/snapshot.sh), e.g. `PULL=1 ./dev-up.sh`.

| Knob | Default | Effect |
|---|---|---|
| `PULL` | `0` | `1` = `docker compose pull` before bring-up (fetch a bumped pin). |
| `NO_OPEN` | `0` | `1` = do not open a browser (headless / CI). |
| `--fresh` | off | Wipes volumes for a from-scratch deploy (**deletes local data**). |
| `SKIP_BRAND` | `0` | `1` = skip applying the CCC brand (test bare upstream). |
| `SKIP_SNAPSHOT` | `0` | `1` = skip the pre-deploy DB+media backup (deploy-remote.sh). |
| `SKIP_VERIFY` | `0` | `1` = skip the post-deploy smoke test (deploy-remote.sh). |
| `NO_PULL` | `0` | `1` = do not pull images on remote deploy (faster; no pin bump). |
| `BACKUP_DIR` | `$HOME/ccc-wiki-backups` | Snapshot output dir (snapshot.sh). |
| `VOLUME` | `ccc-wiki_bookstack_config` | Docker named volume to archive (snapshot.sh). |
| `BOOKSTACK_SERVICE` | `bookstack` | Compose service name (apply-brand.sh). |

## 3) GitHub Actions repo Variables — `gh variable set NAME --body "value"`

Hot-editable in the UI (Settings → Secrets and variables → Actions → Variables);
no code change. All optional with the defaults shown.

| Variable | Default | Purpose |
|---|---|---|
| `DEPLOY_CONNOR_ENABLED` | _(unset)_ | `'true'` activates the connor-server auto-deploy. Default: skipped/inert. |
| `DEPLOY_RUNNER_LABELS` | `["self-hosted","connor-server","ccc-wiki"]` | Must match how the runner was registered. |
| `DEPLOY_LIVE_DIR` | `/home/connor/ccc-wiki` | Deploy path on the box. |
| `DEPLOY_BACKUP_DIR` | `/home/connor/ccc-wiki-backups` | Snapshot dir on the box. |
| `AWS_ROLE_ARN` | _(unset)_ | OIDC role for `terraform-plan.yml`. Unset ⇒ the plan job no-ops safely. |
| `AWS_REGION` | _(unset)_ | AWS region for the plan job. |
| `TFSTATE_BUCKET` | _(unset)_ | S3 state bucket for the plan job. |
| `TF_VERSION` | `1.13.3` | Pinned in [ci.yml](../.github/workflows/ci.yml) `env:` (lockstep with Makefile via check_pins.sh). |

## 4) Terraform inputs — change in `terraform/terraform.tfvars`

Defaults come from [terraform/variables.tf](../terraform/variables.tf); override per
environment in `terraform.tfvars` (gitignored; template:
[terraform.tfvars.example](../terraform/terraform.tfvars.example)). The **VUIT-gated**
rows must be supplied before `terraform apply`.

| Variable | Default | Notes |
|---|---|---|
| `region` | `us-east-1` | AWS region (confirm with VUIT). |
| `environment` | `prod` | Used in tags + resource names. |
| `name_prefix` | `ccc-wiki` | Prefix for **every** resource name (see §8). |
| `vpc_cidr` | `10.20.0.0/20` | Do not overlap anything VUIT may peer. |
| `az_count` | `2` | AZs to span (Balanced). |
| `vpn_ingress_cidrs` | `[]` | **THE access control.** Empty = ALB fully **closed** (fails safe). Set VUIT VPN egress CIDRs before apply. |
| `domain_name` | `wiki.ccc.vanderbilt.edu` | Coordinate with VUIT. |
| `certificate_arn` | `""` | Imported ACM cert ARN, or `""` to have TF request one. |
| `route53_zone_id` | `""` | Only if the zone is in **this** account; usually `""` (VUIT owns DNS). |
| `instance_type` | `t4g.small` | EC2 (Graviton/arm64). |
| `bookstack_image` | `lscr.io/linuxserver/bookstack:v26.05-ls265` | Pin — see §0. |
| `root_volume_gb` | `30` | Root EBS (media is on EFS, so small). |
| `db_instance_class` | `db.t4g.small` | t4g.small (2 GiB) minimum. |
| `db_engine_version` | `8.0` | RDS MySQL. |
| `db_allocated_gb` | `20` | Initial RDS storage. |
| `db_max_allocated_gb` | `100` | Storage autoscaling cap. |
| `db_backup_retention_days` | `35` | PITR window. |
| `db_name` | `bookstackapp` | |
| `db_username` | `bookstack` | |
| `backup_retention_days` | `35` | AWS Backup retention (EFS + RDS). |
| `log_retention_days` | `90` | CloudWatch Logs retention. |
| `alarm_email` | `""` | SNS alarm subscriber, e.g. `ccc-ops@vanderbilt.edu`. `""` = none. |
| `app_timezone` | `America/Chicago` | TZ for the prod container. |

## 5) Terraform state backend — change in `terraform/backend.hcl`

The state bucket is created **out of band** (it cannot be managed by the state it
stores — see [terraform/backend.tf](../terraform/backend.tf)). Template:
[backend.hcl.example](../terraform/backend.hcl.example).

| Setting | Value |
|---|---|
| `bucket` | `ccc-wiki-tfstate-REPLACE` (pre-created S3 bucket) |
| `key` | `ccc-wiki/prod/terraform.tfstate` |
| `region` | `us-east-1` |
| `encrypt` | `true` |
| `use_lockfile` | `true` |

## 6) AWS runtime config — SSM Parameter Store (non-secret)

Set by `terraform apply` under `/<name_prefix>/...`; the instance reads them at
boot. Terraform `ignore_changes` lets ops update SAML endpoints live without TF
reverting (`aws ssm put-parameter --overwrite ...` — see How to apply).

| Parameter | Value at launch |
|---|---|
| `/ccc-wiki/app_url` | `https://<domain_name>` |
| `/ccc-wiki/auth_method` | `standard` (flip to `saml2` in Phase 2, then roll the ASG) |
| `/ccc-wiki/saml_idp_entityid` | `SET-FROM-VUIT` |
| `/ccc-wiki/saml_idp_sso` | `SET-FROM-VUIT` |
| `/ccc-wiki/saml_idp_slo` | `SET-FROM-VUIT` |
| `/ccc-wiki/saml_email_attribute` | `mail` |
| `/ccc-wiki/saml_display_name_attributes` | `givenName\|sn` |
| `/ccc-wiki/saml_external_id_attribute` | `eduPersonPrincipalName` |
| `/ccc-wiki/saml_user_to_groups` | `false` |
| `/ccc-wiki/saml_group_attribute` | `groups` |

## 7) Branding tokens (config-as-code) — change in `deploy/branding/*`

The repo is the source of truth: [apply-brand.sh](../deploy/local/apply-brand.sh)
re-applies these on every deploy and **overwrites live UI brand edits**. See
[deploy/branding/README.md](../deploy/branding/README.md).

| Token | Value | Notes |
|---|---|---|
| App color (CCC black) | `#1C1C1C` | `app-color` / `app-color-dark` in apply-brand.sh. Black because BookStack paints the header with it and strips the custom head on `/settings/{category}` pages (issue #40); the head re-points `--color-primary` to Oak (`--ccc-gold-oak`) for buttons elsewhere. |
| App color tint | `rgba(148,110,36,0.15)` | `app-color-light` / `app-color-light-dark` — the Oak accent tint (subtle selected/hover fills). |
| App name | `CCC Wiki` | From `APP_NAME` (§1); apply-brand.sh tracks it. |
| Logo / favicon | `assets/ccc-logo-reversed.svg`, `ccc-favicon.svg` | Override paths via `HEAD_FILE` / `LOGO_FILE` / `FAVICON_FILE`. |

## 8) Project identity (currently hardcoded)

The string `ccc-wiki` couples several things. There is no single knob today; that
is fine for single-tenant Phase 0. If you ever **rename the project**, change all
of these together:

- compose `name: ccc-wiki` + `container_name` `ccc-wiki-app` / `ccc-wiki-db` ([compose.yaml](../deploy/local/compose.yaml))
- `VOLUME` `ccc-wiki_bookstack_config` (snapshot.sh, §2)
- `name_prefix` (Terraform §4 — drives every AWS resource name)
- `DEPLOY_RUNNER_LABELS` includes `ccc-wiki` (GitHub §3)
- tfstate `key` prefix `ccc-wiki/...` (backend §5)

## 9) Future-feature config (not yet implemented; from the open issues)

Add these knobs when the feature lands, so nothing gets hardcoded.

| Issue | Feature | New config to add |
|---|---|---|
| #22 | Outbound email (SES) — BookStack's **own** mail (invites/resets/notifications) | `MAIL_DRIVER=smtp`, `MAIL_HOST`, `MAIL_PORT`, `MAIL_ENCRYPTION`, `MAIL_FROM_ADDRESS`, `MAIL_FROM_NAME` (creds → `.env` / Secrets Manager). NB: the contact form (#15) already delivers via its own SMTP relay (§1b) — this row is BookStack's separate transactional mail. |
| #19 | WAF on the ALB | rate-limit threshold (req/5min), managed rule-group selection |
| #20 | Cost guardrails | monthly budget amount, alert thresholds (50/80/100%), notify email, cost-allocation tag set |
| #21 | Audit log export | export S3 bucket name, retention days |
| #27 | Agent write API | BookStack REST base URL, sanctioned endpoints, "Agent author" role name |
| #28 / #29 | CLI + MCP client | API base URL + token via env/config (token never logged) |
| #12 | Tag-gated deploy | release tag pattern (e.g. `v*.*.*`) that triggers the AWS image push |
| #15 | Contact-page intake — **shipped** (`services/contact`) | Configured now via `deploy/local/.env` (§1b) + secrets in [.env.example](.env.example) §5. |
| #5 | SSM/secrets VPC endpoints | toggle for interface endpoints (ssm / ssmmessages / ec2messages [/ secretsmanager / logs]) |
