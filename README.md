# CCC Internal Documentation

Internal knowledge base and documentation platform for Vanderbilt University's College of Connected
Computing (CCC). A self-hosted **BookStack** wiki: staff read, write, and maintain internal docs
directly in the browser — no Git knowledge required.

## Access model

- **Read** — on Vanderbilt VPN, no login required.
- **Edit** — VPN + Vanderbilt SSO (SAML2 / Shibboleth).
- **Admin** — VPN + Vanderbilt SSO (elevated role).

Roles are **Viewer / Editor / Admin**. All pages have full revision history with diffs and one-click
restore. VPN reachability is enforced at the AWS network layer — see the [security note](docs/architecture.md#security-model--honest-framing)
(v1 is an internet-facing ALB allowlisted to VUIT VPN CIDRs; internal-ALB topology is the documented stronger option).

## Tech stack

| Layer | Technology |
|---|---|
| Platform | [BookStack](https://www.bookstackapp.com/) (LinuxServer image, pinned `v26.05-ls265`) |
| Compute | AWS EC2 `t4g.small` (Graviton) in an ASG(1), docker-compose |
| Edge / TLS | Application Load Balancer + ACM (or imported VUIT/Sectigo cert) |
| Database | AWS RDS MySQL 8.0 `db.t4g.small`, Multi-AZ |
| Media (images + attachments) | AWS EFS (`/config`), backed up via AWS Backup |
| Auth | Vanderbilt SSO via SAML2 / Shibboleth |
| Secrets / config | AWS Secrets Manager + SSM Parameter Store |
| IaC | Terraform |

## Repository layout

```
deploy/local/     Local validation stack for connor-server (BookStack + MariaDB + verify.sh)
terraform/        AWS production footprint (validated; apply is the AWS phase, gated on VUIT inputs)
docs/architecture.md   Design, decisions, security note, validated findings
docs/runbooks/    VUIT coordination, break-glass admin, upgrades, DR restore, SAML cert rotation
```

## Status

- **Phase 0 (local on connor-server): validated green.** Anonymous read gating, admin login + RBAC,
  revision history (diff + restore), media on the persistent volume, persistence across restart, and
  the DB+media backup/restore drill all pass on BookStack `v26.05-ls265`. See
  [deploy/local/README.md](deploy/local/README.md).
- **Phase 1+ (AWS): Terraform written and `terraform validate`-clean**, not yet applied. Applying is
  gated on VUIT inputs (VPN CIDRs, DNS/subdomain, TLS cert, SAML SP registration + attribute release).
  See [terraform/README.md](terraform/README.md) and
  [docs/runbooks/vuit-coordination-checklist.md](docs/runbooks/vuit-coordination-checklist.md).

## Updates

- **What changed, when** — see [CHANGELOG.md](CHANGELOG.md).
- **Current state** — see the [project status tracker](docs/status.md).
- **Editing the docs?** Expand an existing doc before adding a new one; keep it short and table-first;
  no emojis; log notable changes in the changelog. Full policy in [CLAUDE.md](CLAUDE.md#documentation).

## Quick start (local test)

```bash
ssh connor-server
cd ccc-internal-documentation/deploy/local
cp .env.example .env        # generate APP_KEY + set DB passwords (see README there)
docker compose up -d
bash verify.sh              # automated deployment checks
```

## AWS verification (after apply, AWS-only)

VPN enforcement (off-VPN refused) · valid HTTPS + HTTP→HTTPS redirect · real Vanderbilt SAML login
mapping to Viewer/Editor/Admin · break-glass local login with SAML live · EFS survives instance
replacement · RDS Multi-AZ failover · DB+EFS DR drill (RTO/RPO recorded) · WCAG 2.2 AA pass on reader
+ editor surfaces. Details in [docs/architecture.md](docs/architecture.md) and the runbooks.
