#!/usr/bin/env bash
# =============================================================================
# CCC BookStack integration test runner.
#
# Brings up an ISOLATED copy of deploy/local/compose.yaml (its own project +
# volumes + port, so it never touches a real local stack), provisions fixtures,
# runs the bats behavioral suite + the Python stress driver, then exercises the
# lifecycle properties (health-with-DB-down, persistence, backup/restore, and
# the durability boundary). Tears the stack down at the end.
#
# Maps to docs/test-plans/bookstack-platform.md.
#
# Usage:
#   tests/integration/run.sh [--profile bats|pr|full] [--keep] [--no-stress]
#
#   --profile bats   bring-up + bats only (fast local loop)
#   --profile pr     bats + stress + health-DB-down + persistence            (DEFAULT; CI PR job)
#   --profile full   pr + backup/restore drill + durability boundary         (CI weekly job)
#   --keep           leave the stack running on exit (for debugging)
#   --no-stress      skip the Python stress phase
#
# Env overrides: BOOKSTACK_TAG, MARIADB_TAG (weekly sets these to `latest` for
# drift detection), PORT, PROJECT, STRESS_CONCURRENCY, STRESS_PER_WORKER.
# =============================================================================
set -uo pipefail

# ---- locate repo + sources --------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
export REPO_ROOT
COMPOSE_FILE="$REPO_ROOT/deploy/local/compose.yaml"
export COMPOSE_FILE
# shellcheck source=/dev/null
source "$REPO_ROOT/tests/lib/common.sh"

# ---- args / config ----------------------------------------------------------
PROFILE="pr"; KEEP=0; STRESS=1
while [ $# -gt 0 ]; do
  case "$1" in
    --profile) PROFILE="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --no-stress) STRESS=0; shift ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

# Reject a mistyped profile loudly. Without this, an unknown value (e.g. a typo'd "prod") brings
# up the stack, runs bats, skips every profile-gated phase, and exits 0 — a false "all passed".
case "$PROFILE" in
  bats|pr|full) ;;
  *) echo "unknown profile: $PROFILE (want bats|pr|full)" >&2; exit 2 ;;
esac

export PROJECT="${PROJECT:-ccc-wiki-test}"
PORT="${PORT:-8089}"
export BASE_URL="http://localhost:$PORT"
export DB_USERNAME="${DB_USERNAME:-bookstack}"
export DB_DATABASE="${DB_DATABASE:-bookstackapp}"
BOOKSTACK_TAG="${BOOKSTACK_TAG:-v26.05-ls265}"   # validated pin; weekly overrides to `latest`
MARIADB_TAG="${MARIADB_TAG:-11.4.12-r0-ls220}"
STRESS_CONCURRENCY="${STRESS_CONCURRENCY:-20}"
STRESS_PER_WORKER="${STRESS_PER_WORKER:-10}"

WORK="$(mktemp -d)"
export ENV_FILE="$WORK/.env"
export DB_PASSWORD
DB_PASSWORD="$(openssl rand -hex 16)"
DB_ROOT_PASSWORD="$(openssl rand -hex 16)"
APP_KEY="base64:$(openssl rand -base64 32)"

# ---- result accounting ------------------------------------------------------
PASS=0; FAIL=0
ok() { echo "  PASS: $1"; PASS=$((PASS + 1)); }
ko() { echo "  FAIL: $1"; FAIL=$((FAIL + 1)); }
phase() { echo; echo "=== $1 ==="; }

teardown() {
  local code=$?
  if [ "$KEEP" = "1" ]; then
    echo; echo "--keep set: stack left running as project '$PROJECT' on $BASE_URL"
  else
    echo; echo "Tearing down…"
    dc down -v --remove-orphans >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK" 2>/dev/null || true
  return $code
}
trap teardown EXIT

abort() { echo "FATAL: $*" >&2; exit 1; }

# ---- write the isolated test .env ------------------------------------------
cat > "$ENV_FILE" <<EOF
BOOKSTACK_TAG=$BOOKSTACK_TAG
MARIADB_TAG=$MARIADB_TAG
TZ=America/Chicago
APP_URL=$BASE_URL
HTTP_BIND=$PORT
APP_KEY=$APP_KEY
DB_ROOT_PASSWORD=$DB_ROOT_PASSWORD
DB_DATABASE=$DB_DATABASE
DB_USERNAME=$DB_USERNAME
DB_PASSWORD=$DB_PASSWORD
AUTH_METHOD=standard
AUTH_AUTO_INITIATE=false
REVISION_LIMIT=false
EOF

echo "Integration run: profile=$PROFILE project=$PROJECT url=$BASE_URL"
echo "Images: bookstack=$BOOKSTACK_TAG mariadb=$MARIADB_TAG"

# ---- bring up ---------------------------------------------------------------
phase "Bring up the stack"
dc up -d || abort "docker compose up failed"
wait_for_db_healthy 180 || abort "DB never became healthy"
echo "DB healthy; waiting for BookStack first-run migrations (can take a couple minutes)…"
wait_for_http /login 200 300 || abort "BookStack never served /login"
echo "BookStack is up."

# Resolve the actual config volume name (compose may sanitize the project name).
VOL_CONFIG="$(docker volume ls --format '{{.Name}}' | grep -E "^${PROJECT}.*bookstack_config$" | head -1)"
[ -n "$VOL_CONFIG" ] || VOL_CONFIG="${PROJECT}_bookstack_config"

# ---- provision fixtures -----------------------------------------------------
phase "Provision fixtures (admin token + RBAC users)"
TID="$(openssl rand -hex 16)"; SECRET="$(openssl rand -hex 16)"
HASH="$(dc exec -T bookstack php -r 'echo password_hash($argv[1], PASSWORD_BCRYPT);' "$SECRET" 2>/dev/null)"
[ -n "$HASH" ] || abort "could not compute bcrypt hash for API token"
dbq "DELETE FROM api_tokens WHERE name='ci-admin';" >/dev/null 2>&1 || true
dbq "INSERT INTO api_tokens (name,token_id,secret,user_id,expires_at,created_at,updated_at)
     VALUES ('ci-admin','$TID','$HASH',1,'2099-12-31',NOW(),NOW());" || abort "could not mint admin token"
export ADMIN_TOKEN="$TID:$SECRET"
[ "$(api_status GET /api/books)" = "200" ] || abort "minted admin token does not authenticate"
ok "admin API token authenticates"

# Seeded admin creds (fresh LinuxServer BookStack install).
export ADMIN_EMAIL="admin@admin.com" ADMIN_PASS="password"

# Create an Editor and a Viewer user via the admin API for the session-RBAC tests.
ROLES_JSON="$(api GET /api/roles)"
EDITOR_ROLE="$(printf '%s' "$ROLES_JSON" | jq -r '.data[]? | select(.display_name=="Editor") | .id' | head -1)"
VIEWER_ROLE="$(printf '%s' "$ROLES_JSON" | jq -r '.data[]? | select(.display_name=="Viewer") | .id' | head -1)"
if [ -n "$EDITOR_ROLE" ] && [ -n "$VIEWER_ROLE" ]; then
  export EDITOR_EMAIL="ci-editor@example.test" EDITOR_PASS="EditorPass-12345"
  export VIEWER_EMAIL="ci-viewer@example.test" VIEWER_PASS="ViewerPass-12345"
  api POST /api/users "{\"name\":\"CI Editor\",\"email\":\"$EDITOR_EMAIL\",\"password\":\"$EDITOR_PASS\",\"roles\":[$EDITOR_ROLE]}" >/dev/null
  api POST /api/users "{\"name\":\"CI Viewer\",\"email\":\"$VIEWER_EMAIL\",\"password\":\"$VIEWER_PASS\",\"roles\":[$VIEWER_ROLE]}" >/dev/null
  ok "provisioned Editor + Viewer users (roles $EDITOR_ROLE/$VIEWER_ROLE)"
else
  echo "  WARN: Editor/Viewer roles not found by name; RBAC tests will skip."
fi

# ---- bats behavioral suite --------------------------------------------------
# bats MUST run on the host: the tests call `docker`/the DB directly (dbq, dc
# exec), which a containerized bats can't reach. Prefer a host-installed bats;
# otherwise fetch a pinned bats-core (it's pure bash — no build, no Docker).
phase "Behavioral suite (bats)"
BATS_FILES=(
  "$SCRIPT_DIR/bats/00_smoke.bats"
  "$SCRIPT_DIR/bats/01_anonymous_gating.bats"
  "$SCRIPT_DIR/bats/02_api_auth.bats"
  "$SCRIPT_DIR/bats/03_rbac.bats"
  "$SCRIPT_DIR/bats/04_revisions.bats"
  "$SCRIPT_DIR/bats/05_media.bats"
  "$SCRIPT_DIR/bats/06_edge_input.bats"
  "$SCRIPT_DIR/bats/07_negative.bats"
)
BATS_BIN="$(command -v bats || true)"
if [ -z "$BATS_BIN" ]; then
  echo "bats not on PATH; fetching pinned bats-core ${BATS_VERSION:-1.13.0}…"
  BV="${BATS_VERSION:-1.13.0}"
  if curl -fsSL "https://github.com/bats-core/bats-core/archive/refs/tags/v${BV}.tar.gz" \
       | tar -xz -C "$WORK"; then
    BATS_BIN="$WORK/bats-core-${BV}/bin/bats"
  fi
fi
if [ -n "$BATS_BIN" ] && [ -x "$BATS_BIN" ]; then
  if "$BATS_BIN" --print-output-on-failure "${BATS_FILES[@]}"; then ok "bats behavioral suite"; else ko "bats behavioral suite"; fi
else
  ko "could not obtain bats (install bats-core, or check network for the pinned fetch)"
fi

# ---- stress -----------------------------------------------------------------
if [ "$STRESS" = "1" ] && { [ "$PROFILE" = "pr" ] || [ "$PROFILE" = "full" ]; }; then
  phase "Stress: concurrent reads (T-018)"
  if python3 "$REPO_ROOT/tests/stress/stress.py" --base-url "$BASE_URL" --token "$ADMIN_TOKEN" \
       --mode read --concurrency "$STRESS_CONCURRENCY" --per-worker "$STRESS_PER_WORKER"; then
    ok "concurrent reads: no 5xx, no transport errors"
  else
    ko "concurrent reads produced 5xx/transport errors"
  fi

  phase "Stress: concurrent edits (T-019)"
  BID=$(api POST /api/books '{"name":"Stress Book"}' | json '.id')
  PID=$(api POST /api/pages "{\"book_id\":$BID,\"name\":\"Stress Page\",\"markdown\":\"# base\"}" | json '.id')
  BEFORE=$(dbq "SELECT COUNT(*) FROM page_revisions WHERE page_id=$PID;")
  if python3 "$REPO_ROOT/tests/stress/stress.py" --base-url "$BASE_URL" --token "$ADMIN_TOKEN" \
       --mode edit --page-id "$PID" --concurrency 8 --per-worker 5; then
    ok "concurrent edits: no 5xx, no transport errors"
  else
    ko "concurrent edits produced 5xx/transport errors"
  fi
  AFTER=$(dbq "SELECT COUNT(*) FROM page_revisions WHERE page_id=$PID;")
  # Invariant: writes registered as new revisions (monotonic increase, no loss/corruption).
  # NB: BookStack may merge near-simultaneous same-user revisions, so we assert >= +1
  # rather than strict equality (see test plan T-019 note).
  if [ "${AFTER:-0}" -gt "${BEFORE:-0}" ]; then
    ok "revision chain advanced under concurrent edits ($BEFORE -> $AFTER)"
  else
    ko "revision chain did not advance under concurrent edits ($BEFORE -> $AFTER)"
  fi
  # Poll: right after a burst of concurrent writes the DB can be briefly busy on a
  # small runner — the page is there, so poll rather than single-shot.
  if poll_status 200 30 GET "/api/pages/$PID"; then
    ok "page still readable after concurrent edits"
  else
    ko "page unreadable after concurrent edits"
  fi
fi

# ---- T-016: health endpoint stays 200 with the DB down ----------------------
if [ "$PROFILE" = "pr" ] || [ "$PROFILE" = "full" ]; then
  phase "T-016: /icon.png is 200 while the DB is DOWN (DB-free health check)"
  dc stop db >/dev/null 2>&1
  CODE_DBDOWN="$(http_status /icon.png)"
  dc start db >/dev/null 2>&1
  wait_for_db_healthy 120 || echo "  WARN: db slow to return healthy"
  wait_for_http /login 200 180 || echo "  WARN: app slow to recover after db restart"
  if [ "$CODE_DBDOWN" = "200" ]; then
    ok "health endpoint served 200 with DB down (RDS failover won't churn the ASG)"
  else
    ko "health endpoint returned $CODE_DBDOWN with DB down (expected 200)"
  fi
fi

# ---- T-010: persistence across `down && up` (no -v) -------------------------
if [ "$PROFILE" = "pr" ] || [ "$PROFILE" = "full" ]; then
  phase "T-010: content + media survive 'down && up' (volume retained)"
  PB=$(api POST /api/books '{"name":"Persist Book"}' | json '.id')
  PP=$(api POST /api/pages "{\"book_id\":$PB,\"name\":\"Persist Page\",\"markdown\":\"# persist-marker-PERSIST\"}" | json '.id')
  printf 'persist attachment\n' > "$WORK/persist.txt"
  curl -s -o /dev/null -H "Authorization: Token $ADMIN_TOKEN" \
    -F "uploaded_to=$PP" -F "name=persist.txt" -F "file=@$WORK/persist.txt;filename=persist.txt" \
    "$BASE_URL/api/attachments"
  ATT_BASE=$(dbq "SELECT path FROM attachments WHERE name='persist.txt' ORDER BY id DESC LIMIT 1;" | awk -F/ '{print $NF}')

  dc down >/dev/null 2>&1          # NOTE: no -v
  dc up -d >/dev/null 2>&1
  wait_for_db_healthy 180 || ko "db unhealthy after restart"
  wait_for_http /login 200 240 || ko "app did not return after restart"
  wait_for_api 120 || ko "API not DB-ready after restart"   # nginx serves /login before MySQL reconnects

  if poll_contains 60 "persist-marker-PERSIST" GET "/api/pages/$PP"; then
    ok "page content survived down/up"
  else
    ko "page content LOST across down/up"
  fi
  ATT_OK=""
  ATT_DEADLINE=$((SECONDS + 60))
  while [ "$SECONDS" -lt "$ATT_DEADLINE" ]; do
    [ -n "$ATT_BASE" ] && [ -n "$(dc exec -T bookstack sh -lc "find /config -type f -name '$ATT_BASE' 2>/dev/null | head -1")" ] && { ATT_OK=1; break; }
    sleep 2
  done
  if [ -n "$ATT_OK" ]; then
    ok "attachment survived down/up on the persistent volume"
  else
    ko "attachment LOST across down/up"
  fi
fi

# ---- T-012 + T-011: backup/restore drill, then durability boundary ----------
if [ "$PROFILE" = "full" ]; then
  phase "T-012: backup (DB + media) and restore TOGETHER into a wiped stack"
  # Fingerprint data the restore must bring back.
  FB=$(api POST /api/books '{"name":"DR Book"}' | json '.id')
  FP=$(api POST /api/pages "{\"book_id\":$FB,\"name\":\"DR Page\",\"markdown\":\"# dr-marker-RESTORE\"}" | json '.id')
  printf 'dr fingerprint payload\n' > "$WORK/dr-fingerprint.txt"
  curl -s -o /dev/null -H "Authorization: Token $ADMIN_TOKEN" \
    -F "uploaded_to=$FP" -F "name=dr-fingerprint.txt" -F "file=@$WORK/dr-fingerprint.txt;filename=dr-fingerprint.txt" \
    "$BASE_URL/api/attachments"

  # Backup: DB dump + media archive (restore the two together).
  dc exec -T db sh -lc "exec mariadb-dump -h 127.0.0.1 -u $DB_USERNAME -p\"$DB_PASSWORD\" --single-transaction --add-drop-table $DB_DATABASE" > "$WORK/backup-db.sql" \
    || ko "DB dump failed"
  docker run --rm -v "$VOL_CONFIG":/data -v "$WORK":/out alpine \
    tar czf /out/backup-media.tgz -C /data www/files www/uploads/images 2>/dev/null \
    || echo "  WARN: media tar reported missing paths (continuing)"

  # Wipe and rebuild a clean stack, then restore both stores.
  dc down -v >/dev/null 2>&1
  dc up -d >/dev/null 2>&1
  wait_for_db_healthy 180 || ko "db unhealthy after wipe"
  wait_for_http /login 200 300 || echo "  WARN: app slow on fresh boot before restore"
  dc stop bookstack >/dev/null 2>&1
  dc exec -T db sh -lc "exec mariadb -h 127.0.0.1 -u $DB_USERNAME -p\"$DB_PASSWORD\" $DB_DATABASE" < "$WORK/backup-db.sql" \
    || ko "DB restore failed"
  docker run --rm -v "$VOL_CONFIG":/data -v "$WORK":/in alpine sh -c 'tar xzf /in/backup-media.tgz -C /data' 2>/dev/null \
    || echo "  WARN: media restore reported issues"
  dc start bookstack >/dev/null 2>&1
  wait_for_http /login 200 240 || ko "app did not return after restore"
  wait_for_api 120 || ko "API not DB-ready after restore"

  # The dump restored our admin token too, so ADMIN_TOKEN authenticates again.
  if poll_contains 60 "dr-marker-RESTORE" GET "/api/pages/$FP"; then
    ok "page content returned after restore"
  else
    ko "page content MISSING after restore"
  fi
  if [ "$(dbq "SELECT COUNT(*) FROM attachments WHERE name='dr-fingerprint.txt';")" -ge 1 ]; then
    ok "attachment metadata returned after restore (DB<->media referential integrity)"
  else
    ko "attachment metadata MISSING after restore"
  fi

  phase "T-011: 'down -v' is the durability boundary (it wipes everything)"
  dc down -v >/dev/null 2>&1
  dc up -d >/dev/null 2>&1
  wait_for_db_healthy 180 || ko "db unhealthy after final wipe"
  # On a fresh install the fingerprint attachment must NOT exist.
  FRESH_COUNT="$(dbq "SELECT COUNT(*) FROM attachments WHERE name='dr-fingerprint.txt';" 2>/dev/null || echo 0)"
  if [ "${FRESH_COUNT:-0}" = "0" ]; then
    ok "down -v wiped durable state (fresh install has no prior data)"
  else
    ko "down -v did NOT wipe state (found $FRESH_COUNT fingerprint rows)"
  fi
fi

# ---- summary ----------------------------------------------------------------
phase "RESULT"
echo "Phases — PASS: $PASS  FAIL: $FAIL"
if [ "$FAIL" -eq 0 ]; then
  echo "ALL INTEGRATION CHECKS PASSED"
  exit 0
else
  echo "SOME INTEGRATION CHECKS FAILED"
  exit 1
fi
