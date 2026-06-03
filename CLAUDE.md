# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is (and isn't)

This is **not an application codebase** — there is no app source to unit-test. BookStack is an
upstream Docker image; what this repo owns and tests is **configuration and infrastructure**: the
BookStack runtime contract (auth gating, RBAC, revision history, media durability, backup/restore)
and the Terraform that provisions it on AWS without exposing the wiki off-VPN or losing data.

Two shippable artifacts, deployed in two phases:

- **`deploy/local/`** — Docker Compose validation stack (BookStack + MariaDB) run on `connor-server`.
  **Phase 0: validated green** on BookStack `v26.05-ls265`.
- **`terraform/`** — AWS production footprint. **Phase 1+: `terraform validate`-clean but NOT yet
  applied.** Applying is gated on VUIT (Vanderbilt IT) inputs: VPN CIDRs, DNS/subdomain, TLS cert,
  SAML SP registration + attribute release.

Before changing infra or config, read [docs/architecture.md](docs/architecture.md) — it carries the
design decisions, the honest security framing, and the findings validated on `connor-server`.

## Commands

The [Makefile](Makefile) is the entry point. **Every tool runs via a pinned Docker image**, so you
only need Docker, and you run the exact versions CI does (image tags mirror `.github/workflows/`).

```bash
make help              # list all targets
make check             # all static/lint/IaC gates — the PR fast path, no cloud creds
make test              # check + integration (everything the PR pipeline runs)
make integration       # integration suite, PR profile (bats + stress + health/persistence)
make integration-full  # full drill: adds backup/restore + the down-v durability boundary
```

Individual gates (all in `make check`): `fmt` `validate` `tflint` `tf-test` `trivy` `checkov`
`shellcheck` `compose-config` `actionlint` `secrets` `links` `pins` `user-data-contract`.

Terraform-only loop (run from `terraform/`):
```bash
terraform init -backend=false   # then:
terraform validate
terraform test                  # plan-time security/edge assertions, mocked providers, no AWS creds
terraform fmt -recursive -check
```

Integration runner directly (`tests/integration/run.sh`) — it brings up an **isolated** compose
project on port `8089`, so it never touches a running local stack:
```bash
tests/integration/run.sh --profile bats          # fast loop: bring-up + bats behavioral tests only
tests/integration/run.sh                          # default --profile pr
tests/integration/run.sh --profile full --keep    # full drill; --keep leaves the stack up to debug
BOOKSTACK_TAG=latest MARIADB_TAG=latest tests/integration/run.sh --profile full   # upstream drift check
```

## CI layout (`.github/workflows/`)

- **`ci.yml`** — PR + push-to-main. Jobs: `terraform`, `iac-security` (trivy+checkov), `lint`,
  `secrets`, `links`, `integration` (PR profile). Zero cloud creds; everything pinned (actions by
  commit SHA, scanner images by tag).
- **`weekly.yml`** — full integration drill, upstream-image drift detection (runs the contract
  against `:latest`), image CVE scan, online link check.
- **`terraform-plan.yml`** — manual (`workflow_dispatch`), OIDC-authenticated `terraform plan`
  against the real account. **No-ops safely** until repo Variables (`AWS_ROLE_ARN`, `AWS_REGION`,
  `TFSTATE_BUCKET`) are set — it's staged for the AWS phase, not active yet.

## Terraform structure

Flat root module, **one file per concern**: `network.tf` `compute.tf` `data.tf` (RDS) `edge.tf`
(ALB/ACM) `iam.tf` `secrets.tf` `observability.tf` `outputs.tf` `variables.tf` `providers.tf`
`versions.tf` `backend.tf`. EC2 user-data is `user-data.sh.tftpl`.

- `vpn_ingress_cidrs` is **the access control**. Empty = the ALB is fully closed (fails safe).
- Secrets (`APP_KEY`, break-glass admins) are Terraform-generated into Secrets Manager + encrypted
  state. SAML cert secrets are placeholders you populate in Phase 2 (`ignore_changes` keeps them).
  RDS master password is RDS-managed.
- The state bucket is created **out of band** (see [terraform/backend.tf](terraform/backend.tf)) —
  it can't be managed by the state it stores. See [terraform/README.md](terraform/README.md) for
  apply order and the VUIT-coordination runbook.

## Conventions and gotchas

- **No floating `:latest` in committed prod config** — enforced by `make pins` (CI fails on it).
  Pin BookStack/MariaDB tags and tool images to a tested version. The validated pins live in
  `tests/integration/run.sh` (`BOOKSTACK_TAG`, `MARIADB_TAG`).
- **BookStack v26.05 schema**: no `books`/`pages` tables — entities live in a unified `entities` +
  `entity_page_data` schema. Backup/restore is whole-DB so it's transparent, but **don't write
  ad-hoc queries against `books`/`pages`.** `page_revisions`, `attachments`, `images`, `users`,
  `roles` still exist.
- **Health check is `/icon.png`** (static, DB-free) — never `/status` (hits PHP/DB), so an RDS
  failover doesn't churn the instance.
- **All media on a persistent volume** (EFS in prod, named volume locally) — not S3. Attachments →
  `/config/www/files/`, images → `/config/www/uploads/images/`.
- **MariaDB `root@localhost` uses `unix_socket` auth** in the LinuxServer image — back up/connect as
  the app user over TCP (`-h 127.0.0.1 -u bookstack`), not as root.
- The load-bearing BookStack auth config (public-read toggle vs role, Viewer as default registration
  role, SAML2 break-glass via `AUTH_AUTO_INITIATE=false`, group→role sync OFF at launch) is detailed
  in [docs/architecture.md](docs/architecture.md#bookstack-configuration-the-load-bearing-bits) — read
  it before touching auth.

## Where docs live

- [docs/architecture.md](docs/architecture.md) — design, security model, validated findings.
- [docs/test-plans/bookstack-platform.md](docs/test-plans/bookstack-platform.md) — what each test
  layer proves and why; [tests/README.md](tests/README.md) is how to run them.
- [docs/runbooks/](docs/runbooks/) — VUIT coordination, break-glass admin, BookStack upgrade, DR
  restore drill, SAML cert rotation, CI/CD pipeline.
