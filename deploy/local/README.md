# Local stack (connor-server) — Phase-0 validation + live dev/staging

Proves the BookStack platform behaves correctly **before** the AWS deployment, *and* now runs as a
live, always-on Phase-0 instance on the Vanderbilt LAN holding real data (accounts, pages,
revisions, media) on Docker named volumes. Runs the same BookStack configuration keys we use in
production. See [../../docs/architecture.md](../../docs/architecture.md) for how this maps to AWS,
and the [plan](../../README.md) for the full design.

> **Still Phase 0 / pre-AWS** — LAN-only, plain HTTP (no TLS), standard DB auth (no SAML yet),
> single node, seeded admin. **Not production.** The AWS phases (1–3) are unchanged and gated on
> VUIT inputs.

Two deploy paths keep the live instance current (both snapshot DB+media to `~/ccc-wiki-backups`
first, never overwrite the live `.env`, and never `down -v`):

- **On-demand from a laptop** — `make deploy` (or the VSCode ▶ "Deploy to connor-server (remote)"
  button). See §1.
- **Auto-on-merge (GitOps)** — a push to `main` deploys via a self-hosted runner on connor-server,
  gated by the repo Variable `DEPLOY_CONNOR_ENABLED`.

Both are documented in [../../docs/runbooks/connor-server-deploy.md](../../docs/runbooks/connor-server-deploy.md).

> **Host:** `connor-server` (`connor-minipc`, `10.76.88.214`) — Ubuntu 24.04, Docker + Compose,
> ports 80/443 free. `ssh connor-server`.

## 1. Configure

```bash
ssh connor-server
git clone git@github.com:PMQ9/ccc-internal-documentation.git    # or pull latest
cd ccc-internal-documentation/deploy/local
cp .env.example .env
```

Generate the app key once and paste it into `.env` as `APP_KEY=base64:...`:

```bash
docker run --rm --entrypoint /bin/bash lscr.io/linuxserver/bookstack:latest appkey
```

Set strong `DB_ROOT_PASSWORD` / `DB_PASSWORD`, and confirm `APP_URL` matches how you'll browse
(`http://10.76.88.214` for LAN, or `http://localhost:8080` if you tunnel — see §3).

## 2. Launch

```bash
docker compose up -d
docker compose logs -f bookstack    # watch first-run migrations finish
```

Pin image tags once you know the version you tested (reproducibility):

```bash
docker inspect lscr.io/linuxserver/bookstack:latest --format '{{ index .Config.Labels "org.opencontainers.image.version" }}'
# put the result in .env as BOOKSTACK_TAG=...   (and similarly MARIADB_TAG=...)
```

## 3. Access from your laptop

- **LAN (on the same network as connor-server):** open `http://10.76.88.214`.
- **SSH tunnel (from anywhere with SSH):** set `HTTP_BIND=8080` + `APP_URL=http://localhost:8080`
  in `.env`, then on the laptop:
  ```bash
  ssh -fN -L 8080:localhost:8080 connor-server
  open http://localhost:8080
  ```

First login uses the seeded admin `admin@admin.com` / `password` — **change these immediately**
(Settings → Users), and create a second admin (break-glass parity with prod).

## 4. Verification checklist

Each item maps to a plan requirement. `$URL` = your `APP_URL`. Run `bash verify.sh` to automate
the deployment-specific checks (V2, V6, V7-relevant data, V9); V1, V3–V5 button-level behavior and
V10/V11 are quick manual passes. **Status:** V2, V6, V7, V8, V9, V10 were validated green on
connor-server (BookStack `v26.05-ls265`, MariaDB `11.4.12-r0-ls220`).

| # | Requirement | How to verify | Pass |
|---|---|---|---|
| V1 | Anonymous read, no login | Settings → Customization → **Allow public viewing? = ON**; grant the **Public** role View only. Then `curl -s -o /dev/null -w '%{http_code}\n' $URL/books` → `200` in a logged-out browser. | ☐ |
| V2 | Edit requires login | Logged out, open a page's `…/edit` URL → redirect to `/login` or `403`. After Editor login → form loads. | ☐ |
| V3 | Viewer role | **Viewer is a built-in role in v26.05** (roles seeded: Admin, Editor, Viewer, Public) — confirm it's view-only and set it as Settings → Registration → **Default Registration Role** so auto-registered SSO users land here, not as editors. A Viewer sees no edit buttons; hitting an edit URL → `403`. | ☐ |
| V4 | Editor role | Editor can create/edit pages; `/settings` → `403`. | ☐ |
| V5 | Admin role | Admin reaches `/settings` and user/role management. | ☐ |
| V6 | Media on the persistent volume | Upload an image + attach a file to a page. Confirm they land in the volume: `docker compose exec bookstack ls -R /config/www/files /config/www/uploads/images`. | ☐ |
| V7 | Media survives restart | `docker compose down` (NO `-v`) then `docker compose up -d` → page, image, attachment all still present. | ☐ |
| V8 | Volume is the durability boundary | (Destructive, last) `docker compose down -v` → data gone, proving the named volumes hold all state. | ☐ |
| V9 | Revision history + diff + restore | Edit a page twice → page history shows ≥2 revisions; open a diff (changes highlighted); **one-click restore** an older revision → content reverts and a new revision is recorded. | ☐ |
| V10 | DB + media backup/restore together | See §5. Restore into a clean stack → pages, users, revisions, and media all return (referential integrity spans DB + volume). | ☐ |
| V11 | Break-glass during IdP outage | With `AUTH_METHOD=saml2` set, block egress to the IdP, confirm the local `/login` form still authenticates the local admin. (Full test is on AWS; locally, confirm `/login` is reachable with `AUTH_AUTO_INITIATE=false`.) | ☐ |

## 5. Backup / restore drill (V10)

> **Auth note (verified):** in the LinuxServer MariaDB image, `root@localhost` uses
> `unix_socket` auth and rejects password login — connect over **TCP as the app user**
> (`-h 127.0.0.1 -u bookstack -p"$DB_PASSWORD"`). Used below.

```bash
APPW=$(grep '^DB_PASSWORD' .env | cut -d= -f2-)

# Backup: DB dump + media archive (restore the two TOGETHER — referential integrity spans both)
docker compose exec -T db sh -c "mariadb-dump -h 127.0.0.1 -u bookstack -p\"$APPW\" --single-transaction --add-drop-table bookstackapp" > backup-db.sql
docker run --rm -v ccc-wiki_bookstack_config:/data -v "$PWD":/out alpine \
  tar czf /out/backup-media.tgz -C /data www/files www/uploads/images   # attachments + images

# Restore into a fresh stack
docker compose down -v && docker compose up -d            # wait for db healthy + first-run migrations
docker compose stop bookstack
docker compose exec -T db sh -c "mariadb -h 127.0.0.1 -u bookstack -p\"$APPW\" bookstackapp" < backup-db.sql
docker run --rm -v ccc-wiki_bookstack_config:/data -v "$PWD":/in alpine sh -c 'tar xzf /in/backup-media.tgz -C /data'
docker compose start bookstack
```

> Volume name is `ccc-wiki_bookstack_config` (compose `name:` + volume key). Confirm with
> `docker volume ls | grep bookstack_config`. The `verify.sh` script automates the §4-table
> checks; this drill (and the public-viewing toggle in V1) is a manual pass.

> **Schema note (verified on v26.05):** BookStack no longer has separate `books`/`pages`
> tables — entities live in a unified `entities` + `entity_page_data` schema. Backup/restore
> operates on the whole database, so this is transparent; just don't write ad-hoc queries
> against `books`/`pages`. `page_revisions`, `attachments`, `images`, `users`, `roles` still exist.

## 6. Optional: SAML smoke test

Real Vanderbilt Shibboleth is unreachable from the LAN. To exercise the `SAML2_*` wiring,
run a mock IdP (e.g. `kristophjunge/test-saml-idp`) on connor-server, point the SAML2 vars at
it, set `AUTH_METHOD=saml2` and `SAML2_DUMP_USER_DETAILS=true`, and confirm: the "Vanderbilt SSO"
button appears, a mock user auto-registers, attributes map, and the new user lands in **Viewer**.
Keep group→role sync OFF. This validates *shape only* — real attribute release is an AWS-phase
item with VUIT.

## What this stack does NOT validate (AWS-only)

Real VPN Security-Group enforcement · real Shibboleth attribute release · ACM/ALB TLS ·
RDS Multi-AZ failover · EFS auto-heal. These are covered in the AWS verification section of the
[plan](../../README.md) and [docs/architecture.md](../../docs/architecture.md).
