# Runbook: CI/CD pipelines

**Implements:** the verification strategy in [docs/test-plans/bookstack-platform.md](../test-plans/bookstack-platform.md)
**Owner:** CCC maintainer (PMQ9)
**Last reviewed:** 2026-06-03

The pipelines that decide whether a change to this repo is shippable, and how to
respond when one goes red. CI is defined in [.github/workflows/](../../.github/workflows/);
everything also runs locally via `make` (see the [Makefile](../../Makefile) and
[tests/README.md](../../tests/README.md)).

## When to use this runbook

- A PR check is red and you need to know which gate failed and why.
- The **weekly** job opened a failure (upstream image drift, a new CVE, a broken link).
- You're onboarding and want to know what "green" means here.
- You're about to bump a pinned version (image tag, action SHA, provider) and need the
  re-baseline steps.

## The pipelines

| Workflow | Trigger | What it proves | Gate? |
|---|---|---|---|
| [`ci.yml`](../../.github/workflows/ci.yml) | every PR + push to `main` | repo is shippable: TF fmt/validate/tflint/**test**, trivy+checkov, shellcheck, compose, actionlint, gitleaks, offline link check, `:latest`-pin gate, user-data contract, and the **integration suite** (stack + bats + stress, PR profile) | **yes** |
| [`weekly.yml`](../../.github/workflows/weekly.yml) | Mon 09:17 UTC + manual | full DR drill (backup/restore + durability boundary); upstream **drift** (`:latest`); image **CVE** scan; **online** link check | partial — see below |
| [`terraform-plan.yml`](../../.github/workflows/terraform-plan.yml) | manual (`workflow_dispatch`) | real `terraform plan` against AWS via OIDC — **no-ops until the AWS phase is configured** | n/a (AWS phase) |

The PR pipeline runs entirely **without cloud credentials** (Terraform providers are
mocked in `terraform test`; everything else is local containers), so it is fast and safe
on forks.

### Which weekly jobs gate vs signal

- **`full-drill-pinned`** — a gate. If the backup/restore drill fails on the *pinned*
  images, the durability contract is broken; treat as a release blocker.
- **`upstream-drift-latest`** — a **signal** (`continue-on-error`). It runs the contract
  against `:latest`. Red here means a future BookStack/MariaDB release will break us — it
  does not mean `main` is broken.
- **`image-cve`** — report-only. Informs *when* to bump the image pin.
- **`links-online`** — a signal; external sites flake.

## Procedure — triage a red PR check

1. Open the failed job; the job name is the gate (e.g. `terraform`, `iac-security`,
   `integration`).
2. Reproduce locally with the matching `make` target — they run the identical pinned tool:
   - `terraform` → `make fmt validate tflint tf-test`
   - `iac-security` → `make trivy checkov`
   - `lint` → `make shellcheck compose-config actionlint pins user-data-contract`
   - `secrets` → `make secrets` · `links` → `make links`
   - `integration` → `make integration` (or `tests/integration/run.sh --profile pr`)
3. Fix the code, or — if the finding is an intentional, documented decision — triage it
   into the right baseline **with a justification comment** (see below). Never blanket-disable
   a scanner.

## What to do if a step fails

- **`terraform test` fails** — a real plan-time security/edge property regressed (e.g. an
  ingress rule gained an open CIDR, encryption was turned off, the prefix list stopped
  failing closed). Read the assert's `error_message`; it names the requirement (SEC-/OPS-/CFG-).
- **trivy / checkov fails on a NEW finding** — decide: fix the Terraform, or (if it's an
  accepted design decision) add the rule ID to [`.trivyignore`](../../.trivyignore) /
  [`.checkov.yaml`](../../.checkov.yaml) **with a one-line justification** referencing
  [docs/architecture.md](../architecture.md). The existing entries are the model.
- **gitleaks fails** — a secret was (nearly) committed. If it's a real secret: rotate it,
  remove it, and force-clean history if already pushed. If it's an intentional fixture/example,
  allowlist it in [`.gitleaks.toml`](../../.gitleaks.toml) (paths or `regexes`).
- **integration fails** — re-run locally with `tests/integration/run.sh --profile bats --keep`
  to leave the stack up, then poke the failing surface by hand (`docker compose -p ccc-wiki-test
  ... logs bookstack`). bats prints the failing assertion's message and the last output.
- **`upstream-drift-latest` fails (weekly)** — a newer BookStack/MariaDB broke the contract.
  Read which test failed, check the BookStack release notes, and decide whether to (a) hold the
  pin, or (b) adapt the config/tests and bump the pin via the
  [bookstack-upgrade runbook](bookstack-upgrade.md) (snapshot-first).

## Recovery — re-baselining after a deliberate bump

When you bump a pinned version, re-run the affected gate and update the pin in **both** CI and
the Makefile (they're kept in sync deliberately):

- **Image tags** (BookStack/MariaDB): update `deploy/local/.env.example`,
  `terraform/variables.tf` (`bookstack_image`), and the tags in `tests/integration/run.sh`
  defaults + `weekly.yml`'s `image-cve` list. Run `make integration-full`.
- **Tool images** (trivy/checkov/etc.): update the pins in `Makefile` and the workflow that
  uses them; re-run `make check`. Re-confirm the scanner baselines still apply (new tool
  versions add new checks — triage or fix them).
- **GitHub Actions**: Dependabot ([.github/dependabot.yml](../../.github/dependabot.yml)) opens
  PRs that bump the SHA-pinned actions weekly; CI re-validates them.
- **Terraform provider**: Dependabot opens a PR; `terraform test` + `tflint` + scanners re-run.

## Notes

(Append-only, dated.)

- 2026-06-03 — Pipelines authored and validated end-to-end locally via Docker: all static/IaC
  gates green, `terraform test` 4/4, and the integration suite (PR profile) 30/30 bats + stress
  + health-with-DB-down + persistence. Scanner baselines triaged with justifications; one real
  gap fixed (SNS-topic encryption). Branch protection (require these checks + PR review before
  merge to `main`) is a repo *setting* to enable in GitHub — not code in this repo.
