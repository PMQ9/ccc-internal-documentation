# Runbook: Deploy to connor-server

**Implements:** the Phase-0 validation deployment in [docs/status.md](../status.md) and the local stack in [deploy/local/README.md](../../deploy/local/README.md)
**Owner:** CCC maintainer (PMQ9)
**Last reviewed:** 2026-06-05

How to push the BookStack validation stack to `connor-server` — both **on demand** from a laptop
and **automatically on merge to `main`**. This is Phase-0 dev infra (LAN box `10.76.88.214`); it is
the precursor to the AWS phase, which replaces it with the OIDC `terraform apply` path
([terraform-plan.yml](../../.github/workflows/terraform-plan.yml)).

> **The one rule:** the server's live `deploy/local/.env` (real `APP_KEY` + DB creds) is **never
> touched**. Both deploy paths rsync with `*.env` excluded, and `rsync --delete` protects excluded
> files — so sessions and the DB connection survive every deploy. Overwriting `APP_KEY` would
> invalidate all sessions/2FA; overwriting `DB_PASSWORD` would break the DB connection.

## When to use this runbook

- You changed config/scripts on your laptop and want it running on `connor-server` to test.
- A change merged to `main` and you want to confirm (or trigger) the auto-deploy.
- A deploy failed or regressed and you need to roll back.
- You're setting up the self-hosted runner for the first time.

## On-demand deploy (from your laptop)

```bash
make deploy                       # or: deploy/local/deploy-remote.sh
                                  # or: VSCode ▶ "Deploy to connor-server (remote)"
```

It runs, in order: SSH preflight → **rsync** the working tree to `connor-server:~/ccc-wiki`
(excluding `.env`/backups/state; only files change, the running stack is untouched) → DB+media
**snapshot** into `~/ccc-wiki-backups` via `snapshot.sh` (rollback point, taken before any
bring-up) → remote `PULL=1 NO_OPEN=1 ./dev-up.sh` (pull pinned images, recreate, wait for
`/icon.png`) → `verify.sh` smoke test → print the LAN URL + `docker ps`.

Every step is toggleable per run:

| Env var | Effect |
|---|---|
| `SKIP_SNAPSHOT=1` | Skip the pre-deploy DB+media backup |
| `SKIP_VERIFY=1` | Skip the post-deploy `verify.sh` smoke test |
| `NO_PULL=1` | Don't `docker compose pull` (faster; use when no pin changed) |
| `REMOTE_HOST=…` | Target a different SSH host (default `connor-server`) |
| `REMOTE_DIR=…` | Target a different remote dir under `$HOME` (default `ccc-wiki`) |

## Auto-deploy on merge to `main`

[`deploy.yml`](../../.github/workflows/deploy.yml) runs the same steps on a **self-hosted runner on
connor-server** (GitHub-hosted runners can't reach the LAN). It is **staged**: the job is *skipped*
(not left pending) until the runner is registered **and** the repo Variable
`DEPLOY_CONNOR_ENABLED=true` is set. Deploys are serialized (`concurrency`, never cancelled
mid-flight). It triggers on `push: main` + `workflow_dispatch` only — **never `pull_request`**.

### One-time setup (on connor-server)

A second runner instance alongside the existing one is fine. The registration token is short-lived
and interactive — **never commit it**.

```bash
ssh connor-server
mkdir -p ~/actions-runner-ccc-wiki && cd ~/actions-runner-ccc-wiki

# Download the runner (use the version + sha256 shown on the repo's "New self-hosted runner" page):
#   github.com/PMQ9/ccc-internal-documentation -> Settings -> Actions -> Runners -> New self-hosted runner
curl -o actions-runner.tar.gz -L \
  https://github.com/actions/runner/releases/download/v<X.Y.Z>/actions-runner-linux-x64-<X.Y.Z>.tar.gz
tar xzf actions-runner.tar.gz

# Configure against THIS repo. <REG_TOKEN> from the same page (one-time, ~1h TTL):
./config.sh \
  --url https://github.com/PMQ9/ccc-internal-documentation \
  --token <REG_TOKEN> \
  --labels self-hosted,connor-server,ccc-wiki \
  --name connor-ccc-wiki --work _work --unattended --replace

sudo ./svc.sh install connor
sudo ./svc.sh start
sudo ./svc.sh status            # expect active (running); runner shows "Idle" in repo Settings -> Runners
```

Then **activate last**, after `svc.sh status` shows the runner Idle:
Settings → Secrets and variables → Actions → Variables → **`DEPLOY_CONNOR_ENABLED` = `true`**.

> Do **not** add `deploy` to branch-protection required checks — a skipped job reports neutral,
> which most "required" configs treat as not-passing, blocking merges while it's disabled. Only
> `ci` gates merges; `deploy` stays advisory.

### Security notes

- **Push-to-main only, never `pull_request`** — the runner holds the live `.env` and a Docker
  socket; running fork-PR code on it would be remote code execution.
- **Least privilege:** `permissions: contents: read`, no cloud creds; runner user is `connor`
  (non-root, already in the `docker` group).
- **Label-scoped** (`self-hosted,connor-server,ccc-wiki`) so it never collides with the unrelated
  `actions-runner-marcomms-agent-v3` runner on the same box.

## Rollback (target: minutes)

1. **Code / image rollback (fast path):** `git revert` the bad commit on `main` (re-triggers the
   deploy with the prior tree + pins), or on the box edit `deploy/local/.env` back to the previous
   `BOOKSTACK_TAG`/`MARIADB_TAG` and run `cd ~/ccc-wiki/deploy/local && PULL=1 NO_OPEN=1 ./dev-up.sh`.
   No rebuild — pinned images are re-pulled.
2. **Data rollback (rare):** restore the latest pre-deploy snapshot **pair** from
   `~/ccc-wiki-backups` (`backup-db-<ts>.sql` + `backup-media-<ts>.tgz`, same `<ts>`) using the
   validated drill in [deploy/local/README.md](../../deploy/local/README.md#5-backup--restore-drill-v10)
   — DB dump and media archive are restored **together** (referential integrity spans both).
3. Re-run `verify.sh`. To freeze auto-deploy while investigating, set `DEPLOY_CONNOR_ENABLED=false`.

## What to do if a step fails

- **Preflight fails** — `ssh connor-server` by hand; confirm the host is up and `docker compose
  version` works.
- **Job stuck pending** (auto-deploy) — the runner is offline (`sudo ~/actions-runner-ccc-wiki/svc.sh
  status`) or a label doesn't match `runs-on`.
- **`.env` missing on a fresh host** — the orchestrator is for an already-initialized host. On a
  brand-new host, `dev-up.sh` would generate a wrong `localhost:8080` `.env`; instead do the one-time
  `cp .env.example .env` + fill-in (LAN `APP_URL`, generated `APP_KEY`, DB passwords) per
  [deploy/local/README.md §1](../../deploy/local/README.md#1-configure), then deploy.
- **`verify.sh` fails** — the deploy regressed behavior; roll back (above) and inspect the logs the
  workflow's failure step printed.
- **Data "disappeared"** — almost always a volume-name mismatch. The named volume
  `ccc-wiki_bookstack_config` is scoped by `name: ccc-wiki` in
  [compose.yaml](../../deploy/local/compose.yaml); never rename it. Confirm with
  `docker volume ls | grep bookstack_config`.

## Maps forward to AWS

This proves the merge → deploy → verify loop on a single box. In the AWS phase that loop becomes the
OIDC-authenticated `terraform apply` / SSM / ASG-roll path (see
[terraform-plan.yml](../../.github/workflows/terraform-plan.yml) and
[docs/architecture.md](../architecture.md)) — not a self-hosted box — but the contract enforced by
`verify.sh` carries over unchanged.

## Notes

(Append-only, dated.)

- 2026-06-05 — Initial version. On-demand path via `deploy/local/deploy-remote.sh` + `make deploy`;
  auto-deploy via `deploy.yml` on a self-hosted runner, staged behind `DEPLOY_CONNOR_ENABLED`.
