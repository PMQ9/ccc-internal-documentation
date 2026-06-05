# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is (and isn't)

This is **not an application codebase** — there is no app source to unit-test. BookStack is an
upstream Docker image; what this repo owns and tests is **configuration and infrastructure**: the
BookStack runtime contract (auth gating, RBAC, revision history, media durability, backup/restore)
and the Terraform that provisions it on AWS without exposing the wiki off-VPN or losing data.

Two shippable artifacts, deployed in two phases:

- **`deploy/local/`** — Docker Compose stack (BookStack + MariaDB). **Phase 0: validated green** on
  BookStack `v26.05-ls265`, and now a **live, always-on dev/staging instance** on `connor-server`
  (Vanderbilt LAN, `http://10.76.88.214`) holding real data on named volumes. Two validated deploy
  paths: **on-demand** (`make deploy` → `deploy-remote.sh`) and **GitOps auto-on-merge**
  (`.github/workflows/deploy.yml` on a self-hosted runner on `connor-server`, gated by repo Variable
  `DEPLOY_CONNOR_ENABLED`). Each deploy snapshots DB+media first (`snapshot.sh`), never overwrites
  the live `.env`, and never runs `down -v`. Still **Phase 0 / pre-AWS**: LAN-only, plain HTTP, DB
  auth (no SAML), single node, seeded admin — **not production**. See
  [docs/runbooks/connor-server-deploy.md](docs/runbooks/connor-server-deploy.md).
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
- **`deploy.yml`** — GitOps deploy to `connor-server` on push to main, via a **self-hosted runner**
  registered there. Gated by repo Variable `DEPLOY_CONNOR_ENABLED`; push-only (never `pull_request`).
  See [docs/runbooks/connor-server-deploy.md](docs/runbooks/connor-server-deploy.md).
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

- **No floating `:latest` in committed prod config** — enforced by `make pins`
  (`tests/lib/check_pins.sh`), which also asserts the Makefile tool pins match the workflow pins (so
  local `make` and CI run identical versions) and forbids a floating `releases/latest` in the EC2
  bootstrap. The **canonical** BookStack/MariaDB pin is `tests/integration/run.sh` (`BOOKSTACK_TAG`,
  `MARIADB_TAG`); it is **deliberately mirrored** in `deploy/local/.env.example` (local stack),
  `terraform/variables.tf` (prod default), `tests/lib/render_user_data.sh` (shellcheck fixture), and
  `.github/workflows/weekly.yml` (CVE scan) — bump them in lockstep (see the upgrade runbook).
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

## Engineering conventions

How this repo stays changeable. These describe how it already works — follow them so changes land
without introducing drift. The bias is **subtraction**: fewer moving parts, enforced by gates.

- **Extend the existing seam; don't add one.** A new AWS resource goes in the existing
  one-file-per-concern `.tf` (`network`/`compute`/`data`/`edge`/`iam`/`secrets`/`observability`) —
  the root module stays flat; no submodules for a single deployment. A new behavioral check goes in a
  numbered `tests/integration/bats/NN_*.bats`; a new shared shell helper goes in `tests/lib/common.sh`
  or `helpers/load.bash`, never copy-pasted into a test. A new gate is a `.PHONY` Makefile target
  added to the `check` aggregate and mirrored in CI.
- **Terraform naming + DRY.** Singletons are `aws_<type>.this`; multi-instance resources get
  descriptive names. **Every resource name derives from `var.name_prefix`** (default `ccc-wiki`) —
  never hardcode a name. (Runbooks use literal `ccc-wiki-*` names for copy-paste; if you override
  `name_prefix`, substitute and confirm from `terraform output` before destructive commands.) Lift
  repeated expressions into a `locals` block (see `network.tf`, `edge.tf`); comments state the
  *why*/trade-off, not the obvious what.
- **Shell.** `#!/usr/bin/env bash` + strict mode (`set -uo pipefail`; helpers stay option-safe so a
  non-2xx HTTP code is data, not a fatal error). All shell is shellcheck-clean at `--severity=warning`
  (`make shellcheck`, including the rendered user-data). Reuse `tests/lib/common.sh` (`dc`, `dbq`,
  `http_status`, `wait_for_*`); poll a real condition, never `sleep N` and hope.
- **Test isolation is non-negotiable.** The runner stands up its own compose project on port `8089`
  with its own volumes and tears it down on exit — it never touches a running local stack. Anything
  you add preserves that: no fixed container names, no host-port collisions, clean teardown.
- **Fitness functions over prose that rots.** When you make a decision that must stay true — a
  security posture, a pinned version, a contract — encode it as a gate (a Makefile target, a
  `terraform test` assertion, `tests/lib/check_pins.sh`, `check_user_data_contract.sh`), not just a
  sentence here. A machine-enforced rule won't silently drift. The pin-lockstep and
  scanner-suppression-with-justification rules are detailed in
  [docs/runbooks/ci-cd-pipeline.md](docs/runbooks/ci-cd-pipeline.md).
- **Favor subtraction; rule of three.** Don't extract a module/helper/variable for the second
  occurrence unless it clearly pays off (this repo deliberately hand-rolls ~5 bats assertions rather
  than vendoring bats-support). Prefer deleting a layer to adding one; don't churn clean code to a
  style preference. A change that adds an abstraction should say, in the PR, what it buys and what it
  costs.

## Documentation

Docs are for humans to read — optimize for the reader, not the writer. The **subtraction** bias
applies here too: fewer, denser docs beat more, thinner ones.

- **Don't add a doc unless one is needed; expand an existing one first.** Same rule as code seams —
  extend `architecture.md`, `status.md`, a runbook, or the nearest README before creating a new
  top-level doc. A new file earns its place only when no existing doc fits. The map of what lives
  where is below.
- **Show, don't bury.** Prefer a table or a diagram; bullets are an acceptable middle ground; avoid
  long technical paragraphs. If a reader has to wade through prose to find one fact, restructure it.
- **Short, concise, readable.** Write so a non-expert can follow it. If it's too complex for an
  average reader, it isn't done — simplify or split it, don't ship it.
- **No emojis.**
- **Log notable changes in [CHANGELOG.md](CHANGELOG.md)** — reader- or operator-facing changes (new
  behavior, infra, gates, runbooks), newest first. Not every commit.

## Where docs live

- [docs/architecture.md](docs/architecture.md) — design, security model, validated findings.
- [docs/test-plans/bookstack-platform.md](docs/test-plans/bookstack-platform.md) — what each test
  layer proves and why; [tests/README.md](tests/README.md) is how to run them.
- [docs/runbooks/](docs/runbooks/) — VUIT coordination, break-glass admin, BookStack upgrade, DR
  restore drill, SAML cert rotation, CI/CD pipeline, connor-server deploy.
