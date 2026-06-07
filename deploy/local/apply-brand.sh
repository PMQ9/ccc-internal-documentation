#!/usr/bin/env bash
# Make the live BookStack brand match the repo: "config as code" for the whole CCC brand layer.
#
# WHY: BookStack stores branding as runtime DB settings + uploaded images, not files it reads, so
# rsync'ing the repo never changes the live look on its own. This applies, idempotently:
#   app-custom-head        <- deploy/branding/ccc-custom-head.html      (CSS/JS theme + 4 UX features)
#   app-logo               <- deploy/branding/assets/ccc-logo-reversed.svg  (staged into the volume)
#   app-icon (+ 180/128/64/32) <- deploy/branding/assets/ccc-favicon.svg
#   app-color (+ tints)    <- CCC black #1C1C1C (the header bar). BookStack paints the header with
#                             --color-primary AND strips our custom head on /settings/{category}
#                             pages, so the header is black there only if the DB primary is (issue #40)
#   app-name               <- APP_NAME from the environment, so the compose .env stays the source
# It writes (and busts the cache) ONLY when something differs, so it is safe on every bring-up. The
# repo is the source of truth: a brand edit made in the BookStack UI is reverted on the next run.
#
# Called by dev-up.sh after the app is healthy (so both deploy paths get it) and via `make apply-theme`.
# Strict mode WITHOUT -e: the tinker round-trip is inspected explicitly (a non-match is data).
set -uo pipefail

cd "$(dirname "$0")" || exit 1 # deploy/local: the compose project context (compose.yaml + .env here)

SVC="${BOOKSTACK_SERVICE:-bookstack}"
HEAD_FILE="${HEAD_FILE:-../branding/ccc-custom-head.html}"
LOGO_FILE="${LOGO_FILE:-../branding/assets/ccc-logo-reversed.svg}"   # white mark, for the dark header
FAVICON_FILE="${FAVICON_FILE:-../branding/assets/ccc-favicon.svg}"
IMG_DIR="/config/www/uploads/images/system" # persistent (bookstack_config volume); served at /uploads/...

for f in "$HEAD_FILE" "$LOGO_FILE" "$FAVICON_FILE"; do
  if [ ! -f "$f" ]; then
    echo "!! apply-brand: $f not found" >&2
    exit 1
  fi
done

# Resolve the contact-form URL (config-as-code): an env override wins, else read
# it from the compose .env, else empty. The head carries a __CCC_CONTACT_URL__
# placeholder we substitute so the "Contact / Feedback" header link points at the
# live service (and is omitted entirely when the URL is unset).
CONTACT_URL="${CONTACT_URL:-}"
if [ -z "$CONTACT_URL" ] && [ -f .env ]; then
  CONTACT_URL="$(grep -E '^CONTACT_URL=' .env 2> /dev/null | head -1 | cut -d= -f2- || true)"
fi

# 1. Stage files INTO the container: the head to a transient per-run path ($$ avoids a shared-file
#    race), the logo/favicon into the persistent uploads volume (they are served as static files, and
#    placing them here bypasses the UI's SVG-upload block). -T so exec doesn't eat our stdin.
stage="/tmp/ccc-head.$$.html"
head_rendered="$(mktemp)"
cleanup() {
  docker compose exec -T "$SVC" rm -f "$stage" > /dev/null 2>&1 || true
  rm -f "$head_rendered" 2> /dev/null || true
}
trap cleanup EXIT

# Render a local copy of the head with the contact URL substituted, then stage THAT.
url_repl="$(printf '%s' "$CONTACT_URL" | sed -e 's/[&|\\]/\\&/g')"
sed "s|__CCC_CONTACT_URL__|${url_repl}|g" "$HEAD_FILE" > "$head_rendered"

stage_into() { docker compose exec -T "$SVC" sh -c "cat > '$2'" < "$1"; } # <local-file> <container-path>

if ! docker compose exec -T "$SVC" sh -c "mkdir -p '$IMG_DIR'" \
  || ! stage_into "$head_rendered" "$stage" \
  || ! stage_into "$LOGO_FILE" "$IMG_DIR/ccc-logo.svg" \
  || ! stage_into "$FAVICON_FILE" "$IMG_DIR/ccc-favicon.svg"; then
  echo "!! apply-brand: could not copy brand files into the '$SVC' container (is the stack up?)" >&2
  exit 1
fi
docker compose exec -T "$SVC" sh -c "chown -R 1000:1000 '$IMG_DIR' && chmod 644 '$IMG_DIR'/*.svg" > /dev/null 2>&1

# 2. Apply settings THROUGH the app (cache-correct), writing each only when it differs. The head path
#    is passed via env so the PHP body stays a literal heredoc; rtrim() tolerates a trailing-newline
#    diff. app-name tracks config("app.name") (= the APP_NAME env) so compose stays the source of truth.
out=$(docker compose exec -T -e "CCC_HEAD=$stage" "$SVC" php /app/www/artisan tinker 2> /dev/null <<'PHP'
$head = file_get_contents(getenv("CCC_HEAD"));
if ($head === false) {
    echo "READFAIL";          // fail loudly rather than report success having skipped the head
} else {
    $changed = false;
    if (rtrim($head) !== rtrim((string) setting("app-custom-head"))) {
        setting()->put("app-custom-head", $head); $changed = true;
    }
    $want = [
        "app-logo"             => "/uploads/images/system/ccc-logo.svg",
        "app-icon"             => "/uploads/images/system/ccc-favicon.svg",
        "app-icon-180"         => "/uploads/images/system/ccc-favicon.svg",
        "app-icon-128"         => "/uploads/images/system/ccc-favicon.svg",
        "app-icon-64"          => "/uploads/images/system/ccc-favicon.svg",
        "app-icon-32"          => "/uploads/images/system/ccc-favicon.svg",
        // --color-primary = CCC black: BookStack paints the header (.primary-background) with it, and
        // strips our custom head on /settings/{category} pages (an upstream lockout guard), so the
        // header is black THERE only if the DB primary is. The head re-points --color-primary back to
        // Oak as the white-label button/accent surface on every other page (issue #40). Do NOT revert
        // to Oak. The *-light tints stay the Oak accent tint (subtle selected/hover fills).
        "app-color"            => "#1C1C1C",
        "app-color-light"      => "rgba(148,110,36,0.15)",
        "app-color-dark"       => "#1C1C1C",
        "app-color-light-dark" => "rgba(148,110,36,0.15)",
    ];
    $name = (string) config("app.name");
    if ($name !== "") { $want["app-name"] = $name; }
    foreach ($want as $k => $v) {
        if ((string) setting($k) !== $v) { setting()->put($k, $v); $changed = true; }
    }
    setting()->flushCache();
    echo $changed ? "CHANGED" : "NOCHANGE";
}
PHP
)

case "$out" in
  *NOCHANGE*)
    echo "apply-brand: brand already matches the repo (no change)"
    ;;
  *CHANGED*)
    docker compose exec -T "$SVC" php /app/www/artisan cache:clear > /dev/null 2>&1
    # This overwrote whatever was stored, including a live UI brand edit if one had been made. The
    # prior values are in the pre-deploy snapshot; to deliberately stop re-applying, freeze deploys
    # (SKIP_BRAND=1 on-demand, DEPLOY_CONNOR_ENABLED=false for auto) and fix the repo.
    echo "apply-brand: brand updated (repo differed from the live settings; overwritten)"
    ;;
  *READFAIL*)
    echo "!! apply-brand: container could not read the staged head file" >&2
    exit 1
    ;;
  *)
    echo "!! apply-brand: unexpected response from BookStack: ${out:-<empty>}" >&2
    exit 1
    ;;
esac
