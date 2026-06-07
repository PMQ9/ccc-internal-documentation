# Project status — CCC Internal Documentation (BookStack)

> **Snapshot as of 2026-06-05.** This is a living tracker: what's done, the decisions made (and the
> trade-offs accepted), what was *deliberately left open* and why, and the roadmap forward. For the
> full design rationale read [architecture.md](architecture.md); for *how* to run things read
> [../CLAUDE.md](../CLAUDE.md), [../README.md](../README.md), and the [runbooks](runbooks/).

## At a glance

A self-hosted **BookStack** wiki for Vanderbilt's College of Connected Computing: read on VPN with no
login, edit/admin via Vanderbilt SSO. Built in two shippable artifacts — a local validation stack and
an AWS Terraform footprint — deployed in phases. **Phase 0 is validated green and now runs as a live,
auto-deploying LAN instance; Phases 1–3 are written and gated on VUIT (Vanderbilt IT) inputs**, not on
engineering work.

| Phase | Scope | State |
|---|---|---|
| **0 — Local validation + live LAN dev/staging** | Docker Compose stack on `connor-server` | ✅ **Validated green** on BookStack `v26.05-ls265`; now a **live, auto-deploying** LAN instance |
| **1 — AWS apply (standard auth)** | Terraform footprint live, standard login | 🔲 Ready; `terraform validate`-clean, **not applied** — gated on VUIT |
| **2 — SSO activation** | SAML2/Shibboleth login + break-glass | 🔲 Written; gated on Phase 1 + VUIT SP registration |
| **3 — Production sign-off** | DR drill, alarms, WCAG 2.2 AA | 🔲 Written; gated on Phase 2 |

The single thing standing between "validated" and "in production" is **external coordination with
VUIT**, tracked in [runbooks/vuit-coordination-checklist.md](runbooks/vuit-coordination-checklist.md).

## What's done

**Local validation stack — Phase 0 green, now a live LAN instance** ([../deploy/local](../deploy/local), PR #1)
Docker Compose BookStack + MariaDB on `connor-server`. The behavioral contract is validated on
BookStack `v26.05-ls265` / MariaDB `11.4.12-r0-ls220`: anonymous read gating, admin login + RBAC,
revision history with diff + one-click restore, media on the persistent volume, persistence across
`docker compose down && up`, and the DB+media backup/restore drill. Checks V2/V6/V7/V9 are automated
in [../deploy/local/verify.sh](../deploy/local/verify.sh); the rest are the manual checklist in
[../deploy/local/README.md](../deploy/local/README.md). Beyond a one-time validation, this is now an
always-on dev/staging instance on the Vanderbilt LAN at `http://10.76.88.214`, holding real data (user
accounts, pages, revisions, uploaded media) on persistent Docker named volumes. Still Phase 0: LAN-only,
plain HTTP (no TLS), standard DB auth (no SAML), single node, seeded admin — **not production**.

**AWS production footprint — validate-clean, not applied** ([../terraform](../terraform), PR #1)
Flat root module, one file per concern (`network`/`compute`/`data`/`edge`/`iam`/`secrets`/
`observability`). Provisions a VPC across 2 AZs, an internet-facing ALB (HTTPS + HTTP→HTTPS), one
EC2 `t4g.small` in an ASG(1) running docker-compose, RDS MySQL 8.0 Multi-AZ, EFS for media + AWS
Backup, Secrets Manager + SSM, and CloudWatch logs/alarms. `terraform validate`, `fmt`, `tflint`,
and the plan-time security tests in [../terraform/tests/plan.tftest.hcl](../terraform/tests/plan.tftest.hcl)
(mocked providers, no AWS creds) all pass. **Nothing has been applied to an AWS account.**

**Test suite + CI/CD** ([../tests](../tests), [../.github/workflows](../.github/workflows), PRs #2, #6)
Numbered `bats` behavioral tests, a stdlib-only stress driver, and an isolated integration runner
(port `8089`, never touches a running stack) with `bats`/`pr`/`full` profiles. CI (`ci.yml`) runs the
PR profile + all static/IaC gates with **zero cloud creds, everything pinned**; `weekly.yml` runs the
full drill + upstream-image drift + CVE scan; `terraform-plan.yml` is staged (no-ops until AWS repo
vars are set). The [../Makefile](../Makefile) is the single entry point — `make check` / `make test`.

**Brand & theming layer** ([../deploy/branding](../deploy/branding), [brand/](brand/), PR #8)
Real CCC logo lockup (not a placeholder), favicon, and a custom-head CSS layer applied via the
BookStack UI; design language and WCAG 2.2 AA criteria documented. Applied once per environment, not
in code.

**Contact-form intake** ([../services/contact](../services/contact), issue #15)
A small standard-library Go service serves the wiki's **Contact** page and turns each
submission into one email to the CCC mailbox (`Reply-To` = the submitter) plus, optionally, a GitHub
issue (best-effort). Transport is pluggable — `agentmail` (recommended; an email API for agents, no
IT, no DMARC wall), `smtp` (Gmail App Password / own-domain + Brevo / AWS SES), or `graph` (M365
send-as). Guarded by VPN + a login-gated header link + `@vanderbilt.edu` + CSRF + honeypot + per-IP
rate-limit + a fixed recipient. Runs behind the `contact` compose profile on connor-server; Go unit
tests + an isolated integration test ([../tests/integration/bats/08_contact.bats](../tests/integration/bats/08_contact.bats),
MailHog sink) gate it. AWS routing is deferred to Phase 1 so the IaC is validated then. Setup +
trade-offs: [runbooks/contact-form.md](runbooks/contact-form.md).

**Architecture hardening + dev ergonomics** (PRs #6, #9, #14)
Pin-drift fitness gate ([../tests/lib/check_pins.sh](../tests/lib/check_pins.sh)), user-data contract
gate, engineering conventions in [../CLAUDE.md](../CLAUDE.md), a local `dev-up.sh` + VSCode run button,
and on-demand `make deploy` (the "Deploy to connor-server (remote)" VSCode button) to push the working
tree to the live instance.

**Deploy automation to `connor-server`** ([../deploy/local/deploy-remote.sh](../deploy/local/deploy-remote.sh), [../.github/workflows/deploy.yml](../.github/workflows/deploy.yml), PR #14)
Two validated deploy paths, both safe by construction. On-demand: `make deploy` rsyncs the working tree
up, snapshots DB+media to `~/ccc-wiki-backups` first, relaunches via `dev-up.sh`, and runs `verify.sh`.
Auto-on-merge (GitOps): `deploy.yml` runs on push to `main` via a self-hosted runner on `connor-server`,
gated by repo Variable `DEPLOY_CONNOR_ENABLED=true`. Each deploy snapshots first, never overwrites the
live `.env` (APP_KEY + DB creds), and never runs `down -v`, so named volumes persist. Deploy paths and
runner labels are repo Variables, not hardcoded. Procedure in
[runbooks/connor-server-deploy.md](runbooks/connor-server-deploy.md).

## Decisions made (and the trade-offs accepted)

These are the load-bearing choices; the full framing lives in [architecture.md](architecture.md).
Each is `decision → why → trade-off`.

**Architecture**

1. **VPN enforcement = internet-facing ALB + Security-Group allowlist** of VUIT VPN egress CIDRs (a
   managed prefix list). *Why:* VUIT can't yet route the VPN into a VPC, so topological isolation
   isn't available. *Trade-off accepted:* this is honestly *IP-allowlisting*, not isolation — the ALB
   DNS resolves publicly and the listener exists. Hardened with default-deny SGs, TLS-only, and an
   empty allowlist that **fails closed**. The stronger internal-ALB topology is a documented migration
   (see open items).
2. **Media on a persistent volume (EFS), not S3 as the live store.** *Why:* sidesteps BookStack's
   public-read-image-vs-S3-Block-Public-Access conflict, and EFS survives instance replacement.
   *Trade-off:* EFS small-file latency, acceptable at this volume; revisit if image-heavy browsing
   loads the single node.
3. **Balanced cost/HA:** RDS **Multi-AZ** (the hard-to-recover stateful part) with auto-failover; trim
   elsewhere — single NAT gateway + S3 gateway endpoint, modest `t4g.small` sizing. *Trade-off:* the
   compute tier is ASG(1) auto-*replacement*, not HA (see risks).
4. **AWS codified as Terraform** with remote state. *Why:* reproducible + reviewable. Items Terraform
   can't own (VUIT DNS, SAML SP registration) are runbooks, not code.

**BookStack configuration** (detailed in [architecture.md](architecture.md#bookstack-configuration-the-load-bearing-bits))

- **Public read** via the UI toggle + built-in Public role (View-only) — so an IdP outage degrades to
  read-only, the desirable failure mode.
- **Viewer as the Default Registration Role** — Viewer is *not* built-in; without this, auto-registered
  SSO users would inherit edit perms.
- **`AUTH_AUTO_INITIATE=false`** keeps the local `/login` reachable for **break-glass** (≥2 local DB
  admins, independent of SAML) during IdP outages.
- **Group→role sync OFF at launch** — a wrong/unreleased group attribute would lock everyone out;
  admins are promoted manually until the released attribute is confirmed against the real IdP.
- **Health check = `/icon.png`** (static, DB-free), never `/status` — so an RDS failover blip can't
  churn the only node.
- **`APP_KEY` generated once, never rotated** (rotating invalidates sessions/2FA);
  **`APP_PROXIES` = exact ALB subnet CIDRs**, never `*`.

**Engineering posture**

- **Fitness functions over prose:** version pins, security posture, and contracts are machine-enforced
  gates (`make pins`, `terraform test`, the user-data contract check), not just documentation.
- **Pins never float** in committed prod config; the canonical BookStack/MariaDB pin lives in
  [../tests/integration/run.sh](../tests/integration/run.sh) and is mirrored in lockstep elsewhere.

## Decisions deliberately NOT made (open / deferred)

Most of these are open *by design* — they need VUIT inputs or a future trigger, and committing to them
now would be guessing. They map to [runbooks/vuit-coordination-checklist.md](runbooks/vuit-coordination-checklist.md).

**Blocked on VUIT (must resolve before / during Phase 1–2):**

- 🔲 **VPN egress CIDRs** for the allowlist prefix list — *the* access control; empty = closed.
- 🔲 **Subdomain + DNS record** pointing at the ALB.
- 🔲 **TLS cert path** — ACM-issued vs imported Sectigo/InCommon (changes whether a cert-expiry alarm is needed).
- 🔲 **SAML SP registration + per-SP attribute release**, including *which* group attribute
  (`isMemberOf` vs `eduPersonScopedAffiliation`) and the stable external ID (`eduPersonPrincipalName`).
- 🔲 **AuthnContext/Duo compatibility** — expect `SAML2_IDP_AUTHNCONTEXT=false` pending VUIT confirm.
- 🔲 **AWS account/role + region** for `terraform apply` (sets the `terraform-plan.yml` repo vars).
- 🔲 **WCAG 2.2 AA formal sign-off** on the production-themed reader + editor surfaces.

**Deferred by choice (have a documented trigger, not built yet):**

- 🔲 **Internal-ALB true isolation** — the stronger topology; deferred until VUIT can route the VPN
   into the VPC. Migration path is documented in architecture.md.
- 🔲 **AWS WAF** — defense-in-depth on the public listener; add if/when wanted.
- 🔲 **Fargate / multi-node HA** — the documented step-up from ASG(1); not justified for a low-traffic
   internal wiki today.
- 🔲 **`REVISION_LIMIT`** stays unlimited pending reconciliation with any Vanderbilt records-retention policy.
- 🔲 **Contact form on AWS** — the service runs on connor-server now; its AWS wiring (ALB `/contact*`
   rule + target group, the container in EC2 user-data, secrets in Secrets Manager) is added during
   Phase 1, so the Terraform can be `validate`/`test`/scanner-checked then. Spec in
   [runbooks/contact-form.md](runbooks/contact-form.md#aws-phase-1--remaining-work).

## Roadmap

Incremental, gated phases — each ships something working before the next begins.

**Phase 1 — AWS apply with standard auth.** Once VUIT supplies VPN CIDRs, DNS, and a cert path:
create the out-of-band state bucket, `terraform apply` with `auth_method=standard`, take a manual RDS
snapshot before first real use, and hand the outputs (SAML SP URLs, ACM DNS-validation records, ALB
DNS) to VUIT. *Exit:* wiki reachable on-VPN over HTTPS with standard login. See
[../terraform/README.md](../terraform/README.md).

**Phase 2 — SSO activation.** After VUIT registers the SP and releases attributes: populate the SAML
secrets/params, set `auth_method=saml2`, roll the ASG. Validate Vanderbilt login maps to Viewer/
Editor/Admin **and** that break-glass local login still works with SAML live. Keep group→role sync off
until the group attribute is confirmed end-to-end.

**Phase 3 — Production sign-off.** Run the DR restore drill (RDS + EFS together, record RTO/RPO),
confirm CloudWatch alarms fire, and complete the WCAG 2.2 AA pass. *Exit:* go-live.

## Risks & watch-items

- **Allowlist correctness depends on VUIT CIDR accuracy** — treat the prefix list as managed config;
  stale CIDRs are the most likely access-control failure.
- **ASG(1) is auto-replacement, not HA** — instance/AZ loss means minutes of cold-boot downtime (no
  data loss: EFS + RDS Multi-AZ). Acceptable for a low-traffic wiki; Fargate is the step-up.
- **IdP signing-cert rotation silently breaks all SSO** — mitigated by a cert-expiry alarm + the
  [SAML cert rotation runbook](runbooks/saml-cert-rotation.md), coordinated with VUIT.
- **Upstream image drift** — `weekly.yml` runs the contract against `:latest` as a signal; bumps follow
  the [upgrade runbook](runbooks/bookstack-upgrade.md) in pin-lockstep.

## Where to go deeper

- Design, security model, validated findings — [architecture.md](architecture.md)
- What each test layer proves — [test-plans/bookstack-platform.md](test-plans/bookstack-platform.md)
- Apply order + VUIT coordination — [../terraform/README.md](../terraform/README.md), [runbooks/vuit-coordination-checklist.md](runbooks/vuit-coordination-checklist.md)
- Operational procedures — [runbooks/](runbooks/)
