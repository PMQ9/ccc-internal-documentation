# Test plan: CCC BookStack documentation platform

**Status:** Approved
**Linked artifacts:** [README.md](../../README.md) · [docs/architecture.md](../architecture.md) · [deploy/local/](../../deploy/local/) · [terraform/](../../terraform/)
**Last updated:** 2026-06-03

## Scope

This plan proves the CCC internal-documentation platform behaves correctly across the two
artifacts the repo actually ships: the **local BookStack validation stack** (`deploy/local/`,
Docker Compose) and the **AWS Terraform footprint** (`terraform/`). It is the written argument
for *which* tests give us confidence — the implementations live in [`tests/`](../../tests/) and
[`terraform/tests/`](../../terraform/tests/), wired into CI.

There is no application source in this repo to unit-test; BookStack itself is an upstream image.
What we own — and therefore test — is **configuration and infrastructure**: the BookStack runtime
contract (auth gating, RBAC, revision history, media durability, backup/restore), and the
Terraform that has to provision that contract on AWS without ever exposing the wiki off-VPN or
losing data. So the suite is weighted toward **IaC correctness/security** and **integration
behavior against a real BookStack + MySQL**, exactly where the production failure modes live.

**Out of scope (AWS-only, not faithfully testable in CI — see [Known gaps](#known-gaps)):** real
VPN Security-Group enforcement, real Shibboleth attribute release, ACM/ALB TLS termination, RDS
Multi-AZ *failover behavior*, EFS auto-heal across instance replacement. CI asserts the Terraform
*declares* these correctly (plan-time); the live behaviors are validated in the AWS phase per the
root README and the runbooks.

## Strategy

Four layers, weighted toward integration and IaC:

1. **Static / lint (seconds, every PR).** `terraform fmt -check`, `terraform validate`, `tflint`,
   `shellcheck` (incl. the rendered user-data template), `docker compose config`, `actionlint`,
   markdown link check, `gitleaks` secret scan. Cheapest layer; runs first and fast-fails.
2. **IaC policy / plan-assertion (seconds, every PR).** Terraform native `terraform test`
   (`.tftest.hcl`, `command = plan`) asserting the load-bearing *security* and *edge* properties of
   the plan — no AWS credentials needed. Plus `trivy config` + `checkov` as a second, rule-based
   opinion on the same Terraform.
3. **Integration (minutes, every PR + weekly).** Bring up the real `deploy/local` stack
   (BookStack + MariaDB) in containers and exercise the runtime contract end-to-end via the HTTP
   API and direct DB assertions — functional, edge, and negative cases. Real DB, real app; nothing
   mocked, because the whole point is that the *wiring* works.
4. **Stress / resilience (minutes, weekly + on-demand).** Concurrency, large payloads, and load
   against the running stack — the cases bash can't express are a small Python (stdlib-only)
   driver.

What's real vs mocked: **everything is real.** The system under test is a deployment, so mocking
the database or the app would test nothing. The third-party we don't own (Vanderbilt's IdP) is the
one thing we don't stand up; SAML is exercised for *wiring shape only* against the documented mock
IdP, and the live IdP is an AWS-phase concern.

Test-data lifecycle: each integration run gets a **fresh stack** (`docker compose down -v` →
`up`), so there is no cross-run bleed. Within a run, tests create their own scoped entities
(builder helpers) and assert on them by id — no shared snowflake fixtures, no order dependence.

## Working acceptance criteria

No formal `docs/requirements/` directory exists, so these ACs are extracted from
[README.md](../../README.md), [docs/architecture.md](../architecture.md), the
[local README](../../deploy/local/README.md) verification table (V1–V11), and the runbooks. They
carry stable typed IDs; every test below traces to one.

### Functional — BookStack runtime contract (`FN`)

- **FN-001** Anonymous gating: with public viewing off, unauthenticated requests to read/edit/admin
  paths redirect to login (302); edit/admin paths are never served to anon.
- **FN-002** RBAC: Viewer is view-only, Editor can create/edit but not reach `/settings`, Admin
  reaches settings + user management. Viewer is the default registration role.
- **FN-003** Revision history: every page edit records a revision; history exposes a diff and a
  one-click restore that itself records a new revision.
- **FN-004** Media durability: uploaded images and attachments are written under the persistent
  volume (`/config/...`), addressed by hashed on-disk name.
- **FN-005** Persistence: content + media survive `docker compose down && up` (volume retained).
- **FN-006** Durability boundary: `docker compose down -v` is the *only* thing that destroys
  state — proving the named volumes hold all durable state.
- **FN-007** Backup/restore together: a DB dump + media archive, restored together into a wiped
  stack, returns pages, users, revisions, **and** media with referential integrity intact.
- **FN-008** API auth: a valid API token authenticates; an absent/invalid token is rejected.
- **FN-009** Break-glass: with `AUTH_METHOD=saml2` and `AUTH_AUTO_INITIATE=false`, the local
  `/login` form stays reachable (does not auto-redirect to the IdP).

### Security / IaC (`SEC`)

- **SEC-001** No `0.0.0.0/0` *ingress* anywhere; the only ALB ingress is the VUIT VPN managed
  prefix list, on 443 and on 80 (redirect only).
- **SEC-002** Fail-closed: with `vpn_ingress_cidrs = []` the prefix list has zero entries, so the
  ALB admits nobody — a misconfigured apply does not expose the wiki.
- **SEC-003** TLS posture: HTTPS listener uses a TLS-1.2+ SSL policy; port 80 is a 301 redirect to
  443; ALB drops invalid header fields.
- **SEC-004** Encryption at rest on every store: EBS root volume, RDS storage, EFS file system.
- **SEC-005** IMDSv2 required on the launch template (`http_tokens = required`).
- **SEC-006** RDS is `publicly_accessible = false` and lives in the private subnet group.
- **SEC-007** IAM least privilege: the instance role reads only the named secret ARNs (no `*`),
  the SSM read is scoped to `/<name_prefix>/*`, and EFS mount is conditioned on the access-point
  ARN.
- **SEC-008** No secrets in the launch template or plaintext: APP_KEY/break-glass come from
  Secrets Manager at boot; the RDS master password is RDS-managed.
- **SEC-009** Destructive-action guards: ALB + RDS deletion protection on; RDS takes a final
  snapshot.
- **SEC-010** No secrets committed to the repo; `.env`, `*.tfvars`, state, and dumps are
  gitignored (but `*.example` is not).
- **SEC-011** Network segmentation: app ingress is from the ALB SG only; DB ingress from the app
  SG only; EFS ingress from the app SG only.

### Reliability / Ops (`OPS`)

- **OPS-001** RDS is Multi-AZ.
- **OPS-002** The ASG ELB health check targets the DB-free `/icon.png` so an RDS failover blip does
  not churn the single instance; grace period covers first-boot EFS+pull+start.
- **OPS-003** AWS Backup selects both the RDS instance and the EFS file system; retention is set.
- **OPS-004** CloudWatch alarms exist for the golden signals (ALB 5xx, unhealthy hosts, p95
  latency), host saturation (CPU credits, disk, status check), RDS (CPU, storage, connections),
  and **cert expiry** (imported certs don't auto-renew).
- **OPS-005** EFS has a mount target per AZ; the access point pins uid/gid 1000 (BookStack
  PUID/PGID).

### Config / Contract (`CFG`)

- **CFG-001** `compose.yaml` is valid and *fails closed* on missing required env (`:?` on APP_KEY,
  APP_URL, DB passwords).
- **CFG-002** `user-data.sh.tftpl` renders to syntactically valid bash and is shellcheck-clean.
- **CFG-003** Committed production config pins image tags (no `:latest` in `.env.example` /
  `bookstack_image`).
- **CFG-004** Terraform is `fmt`-clean, `validate`-clean, and `tflint`-clean.
- **CFG-005** Internal doc links resolve (the docs cross-reference heavily).

### Performance / Stress (`PERF`)

- **PERF-001** `/icon.png` returns 200 and is served without touching PHP/DB (health-check
  contract).
- **PERF-002** Under N concurrent anonymous/admin reads, responses stay 2xx/3xx — no 5xx, no
  connection resets.
- **PERF-003** Concurrent edits to the same page do not crash the app and each leaves the revision
  chain consistent (no orphaned/lost revisions).
- **PERF-004** A large page (≥1 MiB markdown) is accepted and re-rendered without error.
- **PERF-005** After bulk-creating many entities, list/read endpoints still respond within a sane
  bound.

## Coverage matrix

Every AC appears here; every test below appears in this matrix. `T-0xx` = integration/stress
(`tests/`), `TF-0xx` = Terraform plan-assertion (`terraform/tests/`), `L-0xx` = static/lint.

| Requirement | Tests |
|---|---|
| FN-001 anonymous gating | T-001, T-002 |
| FN-002 RBAC | T-003, T-004, T-005 |
| FN-003 revision history | T-006, T-007 |
| FN-004 media durability | T-008, T-009 |
| FN-005 persistence across restart | T-010 |
| FN-006 durability boundary | T-011 *(weekly `--profile full` only)* |
| FN-007 backup/restore together | T-012 *(weekly `--profile full` only)* |
| FN-008 API auth | T-013, T-014 |
| FN-009 break-glass /login | T-015 *(planned — manual until AWS/SAML phase)* |
| SEC-001 no 0.0.0.0/0 ingress | TF-001, L-IAC |
| SEC-002 fail-closed empty CIDRs | TF-002 |
| SEC-003 TLS posture | TF-003 |
| SEC-004 encryption at rest | TF-004 |
| SEC-005 IMDSv2 required | TF-005 |
| SEC-006 RDS not public | TF-006 |
| SEC-007 IAM least privilege | TF-007 |
| SEC-008 no secrets in LT | TF-008, L-SECRET |
| SEC-009 deletion protection / final snapshot | TF-009 |
| SEC-010 no committed secrets | L-SECRET, L-GITIGNORE |
| SEC-011 SG segmentation | TF-010 |
| OPS-001 RDS Multi-AZ | TF-011 |
| OPS-002 DB-free health check | TF-012, T-016 |
| OPS-003 AWS Backup selection | TF-013 |
| OPS-004 CloudWatch alarms | TF-014 |
| OPS-005 EFS mount targets + AP | TF-015 |
| CFG-001 compose valid + fail-closed | L-COMPOSE, T-017 |
| CFG-002 user-data renders + shellcheck | L-SHELL, TF-016 |
| CFG-003 image pins (no :latest) | TF-017, L-PIN |
| CFG-004 fmt/validate/tflint clean | L-FMT, L-VALIDATE, L-TFLINT |
| CFG-005 doc links resolve | L-LINKS |
| PERF-001 health endpoint DB-free | T-016 |
| PERF-002 concurrent reads | T-018 |
| PERF-003 concurrent edits | T-019 |
| PERF-004 large page | T-020 |
| PERF-005 bulk entities | T-021 |

> **PR vs weekly coverage.** The PR pipeline runs the `pr` profile. **T-011** (durability boundary)
> and **T-012** (backup/restore together) run only in the weekly `--profile full` drill, so a
> DR-restore regression can go undetected for up to a week — a green PR is *not* a DR-verified PR.

## Test cases

Append-only; stable IDs. If a case becomes wrong because an AC was superseded, mark it
`Superseded by T-NNN` and add a new one — don't rewrite in place.

### Happy paths (integration)

#### T-001 — Anonymous edit/admin paths redirect to login
- **Covers:** FN-001 · **Layer:** integration · **Status:** Active
- **Preconditions:** fresh stack, `AUTH_METHOD=standard`, public viewing not enabled.
- **Steps:** `GET /`, `/settings/users`, `/books/create` with no session.
- **Expected:** each returns `302` (redirect to `/login`). Edit/admin HTML is never returned to anon.

#### T-003 — Editor can create/edit, cannot reach settings
- **Covers:** FN-002 · **Layer:** integration
- **Preconditions:** an Editor-role user/token exists.
- **Steps:** create a book + page via API as Editor; then `GET /settings`.
- **Expected:** create/edit succeed (2xx); `/settings` → `403`.

#### T-004 — Viewer is view-only
- **Covers:** FN-002 · **Layer:** integration
- **Steps:** as a Viewer token, attempt `POST /api/books`; `GET` an existing page.
- **Expected:** read → 200; create → `403`. No edit affordance.

#### T-005 — Admin reaches settings + user management
- **Covers:** FN-002 · **Layer:** integration
- **Steps:** as Admin token, `GET /api/users`, create a user.
- **Expected:** 2xx; admin-only endpoints reachable.

#### T-006 — Edits accumulate revisions
- **Covers:** FN-003 · **Layer:** integration
- **Steps:** create a page, `PUT` it twice with different markdown.
- **Expected:** `page_revisions` for that page ≥ 2 (this is the automated half of local V9).

#### T-007 — Revision history retains old versions; live page renders the newest
- **Covers:** FN-003 · **Layer:** integration
- **Steps:** create page (v1), edit (v2); assert the v1 marker is still present in `page_revisions`;
  `GET` the page and confirm it renders the v2 content.
- **Expected:** old content is retained as a revision (the durable property that makes restore
  possible) and the live page shows the latest edit. The one-click *restore* action itself is the
  manual V9 check (deploy/local/README.md), not yet automated in CI.

#### T-008 — Uploaded attachment lands on the persistent volume
- **Covers:** FN-004 · **Layer:** integration
- **Steps:** upload an attachment to a page via API; read its `path` from the `attachments` table;
  `find` that basename under `/config` inside the container.
- **Expected:** file found under `/config/...`; display name is metadata only.

#### T-009 — Uploaded image lands on the persistent volume
- **Covers:** FN-004 · **Layer:** integration
- **Steps:** upload a 1×1 PNG via the image gallery API; `find verify*.png` under `/config`.
- **Expected:** file present under `/config/.../uploads/images/...`.

#### T-013 — Valid API token authenticates
- **Covers:** FN-008 · **Layer:** integration
- **Steps:** mint a token via DB (deterministic), `GET /api/books`.
- **Expected:** `200`.

### Boundary and edge cases (integration)

#### T-002 — Public-read toggle flips anon read to 200 (and only read)
- **Covers:** FN-001 · **Layer:** integration · **Edge:** auth — public path enabled
- **Preconditions:** enable "Allow public viewing" + grant Public role View (done via DB/API in
  setup).
- **Steps:** anon `GET /books` (a read surface) vs anon `GET /books/create` (an edit surface).
- **Expected:** read → `200`; edit → still `302/403`. Public-read must not leak edit.

#### T-010 — Persistence across `down && up` (no `-v`)
- **Covers:** FN-005 · **Layer:** integration · **Edge:** data state — process restart
- **Steps:** create a page + upload media; `docker compose down` (no `-v`); `up`; re-`GET` the page
  and media.
- **Expected:** page, image, attachment all still present and byte-identical.

#### T-011 — `down -v` is the durability boundary
- **Covers:** FN-006 · **Layer:** integration (destructive, runs last) · **Edge:** data state — wipe
- **Steps:** record an entity count; `docker compose down -v`; `up`; count again.
- **Expected:** state is gone (fresh install), proving named volumes — not the container layer —
  hold durable state.

#### T-014 — Missing/invalid API token is rejected
- **Covers:** FN-008 · **Layer:** integration · **Edge:** auth — bad/absent credential
- **Steps:** `GET /api/books` with no header; then with `Authorization: Token bogus:bogus`.
- **Expected:** `401` (not 200, not 500).

#### T-015 — `/login` reachable with SAML configured + auto-initiate off
- **Covers:** FN-009 · **Layer:** integration · **Edge:** auth — IdP-down resilience shape
- **Status:** Planned (AWS-phase / SAML wiring) — **not yet automated**. CI runs with
  `AUTH_METHOD=standard`, so the SAML break-glass property is currently a manual check (see Known gaps).
- **Preconditions:** stack restarted with `AUTH_METHOD=saml2`, `AUTH_AUTO_INITIATE=false`, mock/empty
  IdP endpoints.
- **Steps:** `GET /login`.
- **Expected:** `200` and the local username/password form is present (does **not** 302 to the IdP).
  This is the break-glass precondition; full outage test is AWS-phase.

#### T-017 — compose fails closed on missing required env
- **Covers:** CFG-001 · **Layer:** integration · **Edge:** input — missing required config
- **Steps:** run `docker compose config` with `APP_KEY` unset.
- **Expected:** non-zero exit citing the `:?` guard — the stack refuses to start half-configured.

#### T-020 — Large page (≥1 MiB markdown) accepted and re-rendered
- **Covers:** PERF-004 · **Layer:** integration · **Edge:** input — max size
- **Steps:** create a page whose markdown is ≥1 MiB (and includes Unicode: emoji, RTL, combining
  chars, and HTML/JS-special characters to check escaping); `GET` it back.
- **Expected:** create 2xx; fetched content round-trips; no 5xx; special chars are escaped, not
  executed.

#### T-021 — Bulk entities keep list/read responsive
- **Covers:** PERF-005 · **Layer:** integration · **Edge:** data state — large aggregate
- **Steps:** create N (≥50) books/pages; time `GET /api/books?count=...` and a page read.
- **Expected:** all creates 2xx; list + read each return 200 under a generous wall-clock bound.

### Negative tests / failure modes

#### T-012 — Backup/restore round-trip into a wiped stack
- **Covers:** FN-007 · **Layer:** integration (the DR `together` property)
- **Preconditions:** content + media exist; capture a fingerprint (page count, a known page's text,
  a media file hash).
- **Steps:** `mariadb-dump` the DB + `tar` the media; `docker compose down -v && up`; restore DB +
  media together; restart app.
- **Expected:** fingerprint matches — pages, users, revisions, and media all return and references
  resolve. (Restoring DB *without* media, or vice versa, would leave dangling refs — that asymmetry
  is the bug this guards.)

#### T-016 — Health endpoint is 200 and DB-free
- **Covers:** OPS-002, PERF-001 · **Layer:** integration · **Failure mode:** DB down must not fail
  health
- **Steps:** `GET /icon.png` → expect 200; then stop the `db` container and `GET /icon.png` again.
- **Expected:** still `200` while DB is down (static asset, no PHP/DB) — proving an RDS failover
  won't flap the ASG health check. (Restart db afterward.)

#### T-018 — Concurrent reads: no 5xx under load
- **Covers:** PERF-002 · **Layer:** stress (Python driver) · **Failure mode:** overload → 5xx/resets
- **Steps:** fire C concurrent workers × R requests at read endpoints (`/icon.png`, a page,
  `/api/books`).
- **Expected:** zero 5xx, zero connection errors; report p50/p95 latency and total throughput.

#### T-019 — Concurrent edits: app survives, revision chain stays consistent
- **Covers:** PERF-003 · **Layer:** stress (Python driver) · **Failure mode:** write contention →
  crash / lost update
- **Steps:** K workers each `PUT` the same page concurrently, then again in a second wave.
- **Expected:** no 5xx; the revision count strictly increases over the run (asserted as ≥ +1).
  BookStack may merge near-simultaneous same-user edits into a single revision, so the invariant is
  *monotonic growth*, not strict equality with the write count (see the run.sh T-019 note and Risks);
  page still readable.

### Non-functional checks — Terraform plan assertions (no AWS creds)

Each `TF-0xx` is a `run` block in a `.tftest.hcl` with `command = plan` asserting on planned
resource attributes.

#### TF-001 — No `0.0.0.0/0` ingress; ALB ingress is the prefix list
- **Covers:** SEC-001 · **Setup:** vars with a non-empty `vpn_ingress_cidrs`.
- **Expected:** both ALB ingress rules reference `aws_ec2_managed_prefix_list.vpn`; no ingress rule
  anywhere has `cidr_ipv4 == "0.0.0.0/0"`.

#### TF-002 — Fail-closed on empty CIDRs
- **Covers:** SEC-002 · **Setup:** default vars (`vpn_ingress_cidrs = []`).
- **Expected:** the managed prefix list plans **zero** entries (ALB admits nobody).

#### TF-003 — TLS-1.2+ policy + HTTP→HTTPS 301
- **Covers:** SEC-003 · **Expected:** HTTPS listener `ssl_policy` matches `ELBSecurityPolicy-TLS13`*;
  port-80 listener default action is `redirect` to `443` with `HTTP_301`; ALB
  `drop_invalid_header_fields == true`.

#### TF-004 — Encryption at rest everywhere
- **Covers:** SEC-004 · **Expected:** launch-template EBS `encrypted == true`, RDS
  `storage_encrypted == true`, EFS `encrypted == true`.

#### TF-005 — IMDSv2 required
- **Covers:** SEC-005 · **Expected:** launch template `metadata_options.http_tokens == "required"`.

#### TF-006 — RDS private
- **Covers:** SEC-006 · **Expected:** `publicly_accessible == false`; subnet group uses the private
  subnets.

#### TF-007 — IAM least privilege
- **Covers:** SEC-007 · **Expected:** the EC2 inline policy's secret-read statement lists explicit
  ARNs (no `"*"` / no `:secret:*`); the SSM statement is scoped to the `/<name_prefix>/*` path; the
  EFS statement carries the `AccessPointArn` condition.

#### TF-008 — No secret material in the launch template
- **Covers:** SEC-008 · **Expected:** rendered `user_data` contains no literal secret values; RDS
  uses `manage_master_user_password = true` (no `password` attribute set).

#### TF-009 — Deletion protection + final snapshot
- **Covers:** SEC-009 · **Expected:** ALB `enable_deletion_protection == true`; RDS
  `deletion_protection == true` and `skip_final_snapshot == false`.

#### TF-010 — SG segmentation
- **Covers:** SEC-011 · **Expected:** app ingress references the ALB SG; db ingress references the
  app SG; efs ingress references the app SG (by `referenced_security_group_id`, not CIDR).

#### TF-011 — RDS Multi-AZ
- **Covers:** OPS-001 · **Expected:** `multi_az == true`.

#### TF-012 — DB-free health check + adequate grace
- **Covers:** OPS-002 · **Expected:** target-group health-check `path == "/icon.png"`,
  `matcher == "200"`; ASG `health_check_type == "ELB"` with a grace period ≥ 300s.

#### TF-013 — AWS Backup covers RDS + EFS
- **Covers:** OPS-003 · **Expected:** the backup selection's `resources` includes both the RDS
  instance ARN and the EFS ARN; a retention/`delete_after` is set.

#### TF-014 — Golden-signal + cert-expiry alarms exist
- **Covers:** OPS-004 · **Expected:** alarms for ALB 5xx, unhealthy hosts, p95 latency, RDS storage,
  and cert expiry are present and wired to the SNS topic.

#### TF-015 — EFS mount targets per AZ + access point ownership
- **Covers:** OPS-005 · **Expected:** mount-target count == `az_count`; access point posix_user
  uid/gid == 1000.

#### TF-016 — user-data renders for a representative plan
- **Covers:** CFG-002 · **Expected:** plan succeeds with the template rendered (no
  templatefile/interpolation errors); rendered text contains the expected `.env` keys.

#### TF-017 — Image pin is not floating
- **Covers:** CFG-003 · **Expected:** `var.bookstack_image` resolves to a pinned tag (matches
  `:v…-ls…`), never `:latest`.

### Static / lint gates

These are pass/fail CI gates rather than narrated cases:

- **L-FMT** `terraform fmt -recursive -check` → clean. *(CFG-004)*
- **L-VALIDATE** `terraform validate` (after `init -backend=false`) → clean. *(CFG-004)*
- **L-TFLINT** `tflint --recursive` with the repo ruleset → no findings. *(CFG-004)*
- **L-IAC** `trivy config` + `checkov` over `terraform/` → no failures above the configured
  severity gate. *(SEC-001 and the whole SEC family, rule-based second opinion)*
- **L-SHELL** `shellcheck` over `deploy/local/verify.sh`, `tests/**/*.sh`, and the **rendered**
  user-data template → clean. *(CFG-002)*
- **L-COMPOSE** `docker compose -f deploy/local/compose.yaml config -q` with a complete `.env` →
  valid. *(CFG-001)*
- **L-PIN** grep gate: no `:latest` in `deploy/local/.env.example` or `terraform/variables.tf`
  image defaults. *(CFG-003)*
- **L-SECRET** `gitleaks detect` → no secrets in history/tree. *(SEC-008, SEC-010)*
- **L-GITIGNORE** assert `.env`, `*.tfvars`, `*.tfstate`, `*.sql` are ignored and `*.example`
  is not. *(SEC-010)*
- **L-LINKS** markdown link check over `**/*.md` → no broken internal links. *(CFG-005)*
- **L-ACTIONLINT** `actionlint` over `.github/workflows/` → clean (CI lints itself).

## Feature-service & infra coverage (part 2 additions)

The original plan (above) predates the contact service (#15/#41/#43), the headless agent API
(#27), and the shared Go client (`services/wiki-client/`). Those features ship their own coverage,
added here so the trace stays complete. The Go layers are **unit** tests (httptest, no network, run
under `go test -race` in CI); the agent-role cases are **integration** bats against the live stack.

| Area | Where | What it proves (added in part 2) |
|---|---|---|
| wiki-client transport | `services/wiki-client/resilience_test.go` | context cancel/deadline aborts the retry budget; malformed-vs-empty 2xx body handling; full retry/terminal status matrix (5xx retried, 4xx terminal); Content-Type only on writes; injected transport is used; jitter bounds |
| contact transport | `services/contact/transport_test.go` | AgentMail non-2xx + omit-empty payload; GitHub transport-error / no-labels / non-JSON-2xx; RFC822 Date + Message-ID + header sort |
| contact guards | `services/contact/handlers_more_test.go` | direct `themeClass`/`sameOrigin`/`verifyCSRF` tables; unreadable-body 400; global breaker not charged by invalid posts; CSRF-cookie attributes; per-kind issue labels |
| contact config | `services/contact/env_test.go` | `env`/`envInt`/`envBool` readers; `Load` allowed-senders parsing, bad-transport rejection, trust-proxy/hops |
| agent role (RBAC) | `tests/integration/bats/09_agent_role.bats` | **AGENT-009** no user creation (403), **AGENT-010** tampered token rejected (401/403); fixed a malformed-JSON attachment case |
| API failure modes | `tests/integration/bats/07_negative.bats` | **N-6** update missing page → 404, **N-7** orphan `chapter_id` → clean 4xx, **N-8** malformed JSON to `/api/pages` → clean 4xx |
| Terraform plan | `terraform/tests/plan.tftest.hcl` | **TF-018/019** bad VPN CIDR / cert ARN rejected at plan (`expect_failures`); **TF-020** subnet fan-out + internet-facing ALB + single-node ASG; **TF-021** IMDS endpoint + ALB→app HTTP/80 contract; **TF-022** RDS engine/version + backup retention; **TF-023** finite log retention; **TF-024** subnet + EFS mount-target fan-out scales with `az_count` |
| Stress driver | `tests/stress/stress_selftest.py` | offline unit tests for `_percentile`/`_gate`/`_drive`; new optional `--max-p95-ms` PERF gate (`make stress-selftest`, in `make check` + CI) |

The `T-020` id is reused by both the large-page edge case and the contact bats suite (they predate a
shared registry); the table above is keyed on file path to disambiguate. New ids are append-only.

## Known gaps

Considered and deliberately **not** automated here, each with a reason:

- **Real VPN SG enforcement (SEC-001 live).** Requires the actual VUIT CIDRs and a client on/off
  VPN. CI asserts the *declaration*; live enforcement is AWS-phase manual verification (root README).
- **Real Shibboleth attribute release + role mapping (FN-002 via SAML).** The live IdP is
  unreachable from CI and from the LAN. We test RBAC via standard-auth tokens (same enforcement
  path) and SAML *wiring shape* only. Live SSO is gated on VUIT (vuit-coordination-checklist).
- **RDS Multi-AZ *failover* and EFS auto-heal (OPS-001/005 live).** CI asserts the config; the
  failover/heal *behavior* needs real AWS and is covered by the DR restore drill runbook.
- **ACM/ALB TLS termination (SEC-003 live).** Plan-asserted only; real cert + HTTPS handshake is
  AWS-phase.
- **WCAG 2.2 AA / accessibility.** A manual review (VUIT checklist item 10); automated a11y
  scanning of the themed deployment is a follow-up, not in this plan.
- **Down-migration of BookStack schema.** BookStack migrations are forward-only by design
  (upgrade runbook is snapshot-first); we test forward upgrade drift weekly, not rollback-by-migration.
- **DB-level concurrency under true multi-instance.** Prod is ASG(1) — single instance — so
  multi-writer DB contention is out of scope by design.

## Risks

Blind spots that remain even with the plan fully implemented:

- **Upstream image drift.** Pinned tags (`v26.05-ls265`, MariaDB `11.4.12-r0-ls220`) are validated;
  a future upstream change can break the contract. Mitigated by the **weekly** pipeline running the
  full integration suite against `:latest` and reporting drift — but that's a detector, not a guard.
- **Plan-time ≠ apply-time.** `terraform test` with `command = plan` asserts intent; a provider bug
  or drift at apply could still differ. An `command = apply` test needs real AWS and is out of CI.
- **Concurrency tests are probabilistic.** T-019 can pass by luck on a fast machine; it asserts an
  invariant (the revision count grows monotonically under concurrent writes — BookStack may merge
  same-user edits, so not strict equality) rather than timing, which reduces but doesn't eliminate
  the blind spot.
- **Health-check semantics.** T-016 proves `/icon.png` is 200 with DB down; it can't prove the ALB
  health check itself is configured to use it (that's TF-012's job — the two together cover OPS-002).

## Revision history

- 2026-06-07 — part 2 test expansion: added the "Feature-service & infra coverage" section
  (contact / agent API / wiki-client unit + integration cases, TF-018..024, the stress self-test);
  no existing case rewritten (append-only).
- 2026-06-03 — initial plan: derived working ACs from architecture/README/local-V-table/runbooks;
  4-layer strategy; coverage matrix; cases T-001..T-021, TF-001..TF-017, L-* gates.
