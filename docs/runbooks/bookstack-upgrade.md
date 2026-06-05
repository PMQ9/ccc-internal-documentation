# Runbook: BookStack version upgrade (snapshot-first)

BookStack runs its own Laravel DB migrations on container start. **A version bump IS a schema
migration event.** Treat it with care.

## Rules

- **Snapshot RDS before every upgrade.** Migrations are forward and not trivially reversible.
- **Never point two BookStack versions at one database** (e.g. a half-rolled ASG). This is a
  single-instance ASG(1), so there's no mixed-version window — but don't run a second instance
  against the same RDS during the change.
- **Pin the image tag** (`var.bookstack_image`). Upgrade = change the pin deliberately, not `:latest`.
- Read the BookStack release notes between your current and target version for breaking changes.

## Procedure (AWS)

1. **Announce** a short maintenance window (the single instance restarts).
2. **Manual RDS snapshot**, tagged with the *current* BookStack version:
   ```bash
   aws rds create-db-snapshot --db-instance-identifier ccc-wiki-db \
     --db-snapshot-identifier ccc-wiki-pre-<oldver>-$(date +%Y%m%d)
   ```
   Also confirm a recent AWS Backup recovery point exists for EFS.
3. **Bump the pin in lockstep.** The validated BookStack/MariaDB pin is mirrored across files that
   each serve a different layer; a bump touches all of them (the canonical pin is `run.sh`):
   - `tests/integration/run.sh` — canonical validated `BOOKSTACK_TAG` / `MARIADB_TAG`
   - `terraform/variables.tf` (`bookstack_image`) — prod default, set the new value via `terraform.tfvars`
   - `tests/lib/render_user_data.sh` — shellcheck render fixture (`make pins` enforces this == `variables.tf`)
   - `deploy/local/.env.example` — local-stack default
   - `.github/workflows/weekly.yml` — the CVE-scan target list

   Then `terraform apply` (updates the launch template).
4. **Roll the instance** so user-data pulls the new image and the container runs new migrations:
   ```bash
   aws autoscaling start-instance-refresh --auto-scaling-group-name ccc-wiki-asg
   ```
5. **Watch**: instance boot log (`/var/log/bootstrap.log` via SSM Session Manager), then
   `docker logs ccc-wiki-app` for `Running migrations … DONE`. Confirm `GET /status` = 200 and a
   page renders. (`/status` is a deliberately **DB-backed** check here — unlike the ALB's DB-free
   `/icon.png` health check — precisely because you want to confirm PHP+DB recovered post-migration.)
6. **Verify** a page edit creates a revision and media still loads.
7. **Re-verify the brand theme layer.** `ccc-custom-head.html` couples to upstream markup that can
   drift across versions — the dark/light toggle selectors (`button.icon-item` / the
   `/preferences/toggle-dark-mode` form), the login form structure, and the dark/light icons mirrored
   from BookStack's own `resources/icons/`. Run the
   [branding validation checklist](../../deploy/branding/README.md#validation-checklist-wcag-22-aa),
   paying attention to the light/dark, one-toggle, and login-screen items.

## Rollback

If migrations fail or the app is broken:
1. Set `bookstack_image` back to the previous pin; `apply` + instance refresh (only safe if the new
   version's migrations did NOT already alter the schema — check the logs).
2. If the schema was migrated, **restore RDS from the pre-upgrade snapshot** (see
   [dr-restore-drill.md](dr-restore-drill.md)) and roll back the image together. EFS media is
   version-tolerant; restore EFS only if the upgrade corrupted media.

## Local (connor-server)

Same idea, smaller: `mariadb-dump` first (see deploy/local/README §5), then change `BOOKSTACK_TAG`
in `.env` and `docker compose up -d`.
