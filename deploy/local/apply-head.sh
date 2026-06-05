#!/usr/bin/env bash
# Make the live theme match the repo: apply deploy/branding/ccc-custom-head.html to BookStack's
# `app-custom-head` DB setting. This is "config as code" for the brand layer.
#
# WHY this exists: BookStack stores the custom head as a runtime DB setting (not a file or env var),
# so rsync'ing the file to the box never changes the live theme on its own. This bridges that gap.
# IDEMPOTENT: it writes (and busts the cache) ONLY when the file differs from what's stored, so it is
# safe to run on every bring-up. The FILE is the source of truth: editing the custom head in the
# BookStack UI is reverted on the next run.
#
# Called by dev-up.sh after the app is healthy (so both the on-demand deploy-remote.sh and the
# auto-deploy deploy.yml paths get it), and standalone via `make apply-theme`.
#
#   bash apply-head.sh
#   HEAD_FILE=../branding/ccc-custom-head.html BOOKSTACK_SERVICE=bookstack bash apply-head.sh
#
# Strict mode WITHOUT -e: the tinker round-trip and grep are inspected explicitly (a non-match is
# data, not a fatal error), matching the option-safe style of tests/lib/common.sh.
set -uo pipefail

cd "$(dirname "$0")" || exit 1 # deploy/local: the compose project context (compose.yaml + .env here)

HEAD_FILE="${HEAD_FILE:-../branding/ccc-custom-head.html}"
SVC="${BOOKSTACK_SERVICE:-bookstack}"

if [ ! -f "$HEAD_FILE" ]; then
  echo "!! apply-head: $HEAD_FILE not found" >&2
  exit 1
fi

# Copy the file INTO the app container (it isn't on a mounted volume), then apply it THROUGH the app
# so BookStack's own setting/content caches stay correct. Reading the blob with file_get_contents
# sidesteps any SQL/shell escaping of the HTML+JS. -T so `docker compose exec` doesn't eat this
# script's stdin, and we feed the file on stdin. A per-run staging path ($$) keeps two overlapping
# deploys from clobbering one shared file; we remove it on exit.
stage="/tmp/ccc-head.$$.html"
cleanup() { docker compose exec -T "$SVC" rm -f "$stage" > /dev/null 2>&1 || true; }
trap cleanup EXIT

if ! docker compose exec -T "$SVC" sh -c "cat > '$stage'" < "$HEAD_FILE"; then
  echo "!! apply-head: could not copy the head file into the '$SVC' container (is the stack up?)" >&2
  exit 1
fi

# Compare-then-write inside one app-context call; prints exactly one marker. The staged path is
# passed via env so the PHP body stays a literal (single-quoted) heredoc. rtrim() on both sides
# makes a trailing-whitespace-only diff (e.g. a UI save that drops the file's final newline) a
# no-op rather than endless re-apply churn.
out=$(docker compose exec -T -e "CCC_HEAD=$stage" "$SVC" php /app/www/artisan tinker 2> /dev/null <<'PHP'
$new = file_get_contents(getenv("CCC_HEAD"));
$cur = (string) setting("app-custom-head");
if ($new === false)                  { echo "READFAIL"; }
elseif (rtrim($new) !== rtrim($cur)) { setting()->put("app-custom-head", $new); echo "APPLIED"; }
else                                 { echo "UNCHANGED"; }
PHP
)

case "$out" in
  *APPLIED*)
    docker compose exec -T "$SVC" php /app/www/artisan cache:clear > /dev/null 2>&1
    # This overwrote whatever was stored, including a live UI edit if one had been made. The prior
    # value is in the pre-deploy snapshot (snapshot.sh); to deliberately stop re-applying a theme,
    # freeze deploys (SKIP_HEAD=1 on-demand, DEPLOY_CONNOR_ENABLED=false for auto) and fix the file.
    echo "apply-head: theme updated (stored app-custom-head differed from the repo file; overwritten)"
    ;;
  *UNCHANGED*)
    echo "apply-head: theme already matches the repo (no change)"
    ;;
  *READFAIL*)
    echo "!! apply-head: container could not read the staged head file" >&2
    exit 1
    ;;
  *)
    echo "!! apply-head: unexpected response from BookStack: ${out:-<empty>}" >&2
    exit 1
    ;;
esac
