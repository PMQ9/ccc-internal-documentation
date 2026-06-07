# Architecture — CCC Internal Documentation Platform

Self-hosted **BookStack** wiki for College of Connected Computing staff. Read on Vanderbilt VPN
with no login; edit/admin via Vanderbilt SSO. Proven on `connor-server`, then promoted to AWS.

## Requirements → how they're met

| Requirement | Mechanism |
|---|---|
| BookStack, browser-only editing | LinuxServer BookStack image (`v26.05-ls265`), WYSIWYG/markdown editor |
| Reachable only on Vanderbilt VPN | ALB ingress Security Group = VUIT VPN-CIDR managed prefix list (see security note) |
| Read without login | BookStack "Allow public viewing" + Public role (View only) |
| Edit/admin via SSO | SAML2 against Vanderbilt Shibboleth IdP (`AUTH_METHOD=saml2`) |
| Roles Viewer/Editor/Admin | Built-in BookStack roles (Viewer **is** built-in in v26.05) |
| Revision history + diff + restore | BookStack built-in (`page_revisions`); validated on connor-server |
| AWS via VUIT | Terraform footprint in [../terraform](../terraform) |
| Attachments separate from server | Media on **EFS**, decoupled from the disposable EC2 instance, backed up via AWS Backup |
| Vanderbilt subdomain | ALB + ACM/Sectigo cert; DNS via VUIT |

## Security model — honest framing

The requirement is "unreachable off-VPN." Two ways to deliver it:

- **Chosen (v1): internet-facing ALB + Security-Group allowlist** of Vanderbilt VPN egress CIDRs.
  This is **IP-allowlisting**, not topological isolation: the ALB DNS name resolves publicly and a
  TLS listener exists; only allowed source IPs complete a connection. Correctness depends entirely on
  the VUIT-supplied CIDRs being accurate and current (they are a managed prefix list, treated as
  config). Campus egress NAT pools may be shared, so this is "allowlisted to VPN/campus egress."
  Hardening: no `0.0.0.0/0` ingress, TLS-only, default-deny SGs; **add AWS WAF** for defense-in-depth.
- **Stronger (future): internal ALB** with VUIT routing the VPN into the VPC (TGW/DX/peering) +
  split-horizon DNS → unreachable off-VPN by *topology*. Migration path: flip `aws_lb.internal=true`,
  move to private-only DNS, drop the public listener. Adopt if/when VUIT can route in.

## Components (AWS prod)

```
Vanderbilt VPN client
   │  HTTPS (443), source IP ∈ VUIT VPN prefix list
   ▼
[ Internet-facing ALB ]  ACM/Sectigo cert · TLS1.2+ · HTTP→HTTPS · health=/icon.png (DB-free)
   │  HTTP 80 (SG: from ALB only)
   ▼
[ EC2 t4g.small, ASG(1) across 2 AZs, private subnet ]  docker-compose → BookStack container
   ├── MySQL 3306 ─────▶ [ RDS MySQL 8.0 db.t4g.small, Multi-AZ, private ]  (master secret in SM)
   └── NFS 2049 ───────▶ [ EFS /config: images + attachments ]  (AWS Backup)
Secrets Manager: APP_KEY, break-glass admins, SAML certs   SSM: APP_URL, auth_method, SAML endpoints
CloudWatch: container logs + alarms → SNS                  Single NAT + S3 gateway endpoint
```

Decisions (confirmed with the requester): internet-facing ALB + SG allowlist; **all media on a
persistent volume** (EFS, not S3 — sidesteps BookStack's public-read-image-on-S3 conflict with S3
Block-Public-Access, and survives instance replacement); **Balanced** cost/HA (Multi-AZ DB, single
NAT, trimmed endpoints); Terraform IaC.

## Contact form (issue #15)

BookStack has no form handler and sanitizes page HTML, so the **Contact** page is served
by a small, separate, standard-library Go service ([`services/contact/`](../services/contact/)) and
linked from the wiki header (a link injected via `app-custom-head` by `apply-brand.sh`). One
submission becomes one email to the CCC mailbox (`Reply-To` = the submitter) plus, optionally, one
GitHub issue (best-effort — a GitHub outage never blocks the email). It owns no database.

**Transport is pluggable** (`MAIL_TRANSPORT`): `agentmail` (recommended — an email API for agents
that sends from an authenticated `agentmail.to` inbox, so no DMARC rejection and no IT), `smtp` (Gmail
App Password / own-domain + Brevo / AWS SES), or `graph` (M365 send-as, needs an Entra app
registration). The forcing constraint: you cannot send *as* `@vanderbilt.edu` without VUIT, and
free-webmail / Proton senders are refused by relays on **DMARC** — so the `From` is a service identity
and the submitter is carried in `Reply-To`. Setup, transport trade-offs, and the AWS wiring (kept out
of Terraform until the AWS phase so the IaC can be validated) are in
[runbooks/contact-form.md](runbooks/contact-form.md).

**Security — honest framing.** Proportionate for an internal VPN-only tool, *not* cryptographic proof
of the submitter's wiki identity (that needs SAML/BookStack integration — a documented future
hardening): reachable only on the VPN, link shown only to logged-in users, `@vanderbilt.edu` (or an
explicit allowlist) required, a synchronizer CSRF token + honeypot + per-IP rate-limit, and a **fixed
recipient** (never an open relay).

## BookStack configuration (the load-bearing bits)

- **Public read** is a UI toggle ("Allow public viewing") + the **Public** role granted View only —
  not an env var. Anonymous read needs no IdP round-trip, so an IdP outage degrades to read-only.
- **Roles:** Admin / Editor / **Viewer** are built-in in v26.05. Set **Viewer as the Default
  Registration Role** so auto-registered SSO users are read-only by default — otherwise every SSO
  user could land with edit rights.
- **SAML2:** explicit `SAML2_IDP_*` (no per-login metadata fetch). `AUTH_AUTO_INITIATE=false` keeps a
  local `/login` form reachable for break-glass during IdP outages. Expect `SAML2_IDP_AUTHNCONTEXT=false`
  (Vanderbilt Duo/MFA). **Group→role sync is OFF at launch** — `SAML2_REMOVE_FROM_GROUPS=true` plus a
  wrong/unreleased group attribute would strip everyone's roles on next login (lockout-class). Enable
  only after confirming the released attribute end-to-end with `SAML2_DUMP_USER_DETAILS=true`.
- **Break-glass:** ≥2 local admins (passwords in Secrets Manager), independent of SAML.
- **Proxy:** `APP_PROXIES` = ALB subnet CIDRs (never `*` behind an IP-allowlisted public ALB).
- **Revisions:** `REVISION_LIMIT=false` (indefinite) — reconcile with any VU records-retention policy.

## Findings validated on connor-server (BookStack v26.05-ls265 / MariaDB 11.4.12)

`connor-server` is now a **live, continuously-deployed Phase-0 instance** on the Vanderbilt LAN
(`http://10.76.88.214`) holding real data — accounts, pages, revisions, uploaded media — on Docker
named volumes (`ccc-wiki_db_data` for the DB, `ccc-wiki_bookstack_config` for media). It is both the
host where these findings were validated **and** an always-on dev/staging wiki. It is **not**
production: LAN-only, plain HTTP (no TLS), standard DB auth (no SAML yet), single node, seeded admin —
the AWS phases close those gaps and stay gated on VUIT inputs. Two deploy paths keep it current —
on-demand `make deploy` and auto-on-merge via a self-hosted runner — both snapshot DB+media first and
never touch the named volumes or the live `.env`; see
[runbooks/connor-server-deploy.md](runbooks/connor-server-deploy.md).

- Anonymous gating, admin login, admin RBAC: **pass**. Edit/admin require login.
- **Revision history** records every edit (3 revisions after create + 2 edits); diff + one-click restore.
- **Media on the persistent volume**: attachments → `/config/www/files/`, images → `/config/www/uploads/images/`.
- **Persistence**: data + media identical across `docker compose down && up`.
- **Backup/restore drill**: DB dump + media archive restored together into a wiped stack; fingerprint matched.
- **v26.05 schema**: no `books`/`pages` tables — unified `entities` + `entity_page_data` (backup/restore is
  whole-DB, so transparent; don't write ad-hoc queries against `books`/`pages`).
- LinuxServer MariaDB `root@localhost` uses `unix_socket` auth — back up as the app user over TCP.
- **Health check** uses `/icon.png` (static, ~0.7ms, no PHP/DB), not `/status` (PHP/DB, ~18ms).

## What is NOT faithfully testable locally (AWS-only)

VPN SG enforcement · real Shibboleth attribute release · ACM/ALB TLS · RDS Multi-AZ failover ·
EFS auto-heal across instance replacement. Covered in the AWS verification section of the root README.

## See also

[../terraform/README.md](../terraform/README.md) · [../deploy/local/README.md](../deploy/local/README.md) ·
[brand/ccc-brand-guidelines.md](brand/ccc-brand-guidelines.md) (design language) ·
[../deploy/branding/README.md](../deploy/branding/README.md) (apply + a11y validation) ·
[runbooks/connor-server-deploy.md](runbooks/connor-server-deploy.md) (live Phase-0 deploy) ·
runbooks in [runbooks/](runbooks/).
