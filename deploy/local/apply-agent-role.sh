#!/usr/bin/env bash
# Make the live BookStack "Agent author" role match the repo: "config as code" for the
# least-privilege headless-API role (issue #27).
#
# WHY: agent API tokens map to a BookStack user, and that user's ROLE is the authorization
# boundary. We keep the role's permission set in code (here), not clicked into the UI, so it's
# reviewable, idempotent, and identical on every deploy. The role grants exactly enough to author
# content + upload media headlessly, and nothing else:
#   access-api               <- without this every /api/* call 401/403s for the role's tokens
#   book/chapter/page         view + create + update (scope: -all)
#   image-create / attachment-create   so an agent can upload media onto a page
# DENIED by absence (fail-closed): every *-delete-*, all bookshelf-*, settings-manage, users-manage,
# user-roles-manage, restrictions-manage-*, templates-manage, content-import/export. A token mapped
# to this role therefore cannot delete content, manage a shelf, escalate a role, or reach settings.
#
# It re-applies on every bring-up via sync() (the pivot is made to EXACTLY equal the allowlist), so a
# permission added to the role in the UI is pruned on the next deploy — the repo is the source of
# truth, same contract as apply-brand.sh. Tokens themselves are issued/rotated/revoked MANUALLY per
# agent (see docs/runbooks/agent-api.md); only the role is code.
#
# Called by dev-up.sh after the app is healthy (so both deploy paths get it) and via
# `make apply-agent-role`. Strict mode WITHOUT -e: the tinker round-trip is inspected explicitly (a
# non-match, e.g. a renamed permission slug, is data we act on — fail closed — not a crash).
#
# Permission slugs, the Eloquent class names, and the regenerate command below were verified against
# BookStack v26.05-ls265. If a future upgrade renames a slug, this script reports MISSINGPERMS and
# fails CLOSED (it never applies a partial grant) — re-verify with:
#   docker compose exec -T bookstack php /app/www/artisan tinker  (then list RolePermission names)
set -uo pipefail

cd "$(dirname "$0")" || exit 1 # deploy/local: the compose project context (compose.yaml + .env here)

SVC="${BOOKSTACK_SERVICE:-bookstack}"

# Apply the role THROUGH the app (cache-correct), changing the pivot only when it differs from the
# allowlist. The PHP echoes one sentinel: NOCHANGE / CHANGED / MISSINGPERMS:<slugs>.
out=$(docker compose exec -T "$SVC" php /app/www/artisan tinker 2> /dev/null <<'PHP'
$role = \BookStack\Users\Models\Role::firstOrCreate(
    ["display_name" => "Agent author"],
    ["description"  => "Least-privilege headless API role (issue #27): create/update content + media; no delete, no admin."]
);
// Fail-closed allowlist. Grant ONLY these; everything destructive or administrative is denied by
// its absence here. Keep this list under review like security code — a real-but-too-broad slug is
// the only silent failure (a missing/renamed slug is caught below).
$want = [
    "access-api",
    "book-view-all", "book-create-all", "book-update-all",
    "chapter-view-all", "chapter-create-all", "chapter-update-all",
    "page-view-all", "page-create-all", "page-update-all",
    "image-create-all", "attachment-create-all",
];
$ids     = \BookStack\Permissions\Models\RolePermission::whereIn("name", $want)->pluck("id", "name");
$missing = array_values(array_diff($want, array_keys($ids->all())));
if (!empty($missing)) {
    echo "MISSINGPERMS:" . implode(",", $missing); // a slug we asked for does not exist -> abort, do not sync a partial set
} else {
    $wantIds = $ids->values()->sort()->values()->all();
    $haveIds = $role->permissions->pluck("id")->sort()->values()->all(); // in-memory collection: no join ambiguity
    $changed = ($wantIds !== $haveIds);
    if ($changed) { $role->permissions()->sync($wantIds); } // sync() prunes any extra (over-)grant -> self-healing
    echo $changed ? "CHANGED" : "NOCHANGE";
}
PHP
)

case "$out" in
  *NOCHANGE*)
    echo "apply-agent-role: 'Agent author' role already matches the repo (no change)"
    ;;
  *CHANGED*)
    # The pivot changed; rebuild BookStack's denormalized effective-permissions and bust the cache so
    # the grant takes effect immediately (a raw sync() bypasses the app's own save hooks).
    docker compose exec -T "$SVC" php /app/www/artisan bookstack:regenerate-permissions > /dev/null 2>&1
    docker compose exec -T "$SVC" php /app/www/artisan cache:clear > /dev/null 2>&1
    echo "apply-agent-role: 'Agent author' role created/updated to the least-privilege set"
    ;;
  *MISSINGPERMS:*)
    echo "!! apply-agent-role: permission slug(s) not found in this BookStack: ${out#*MISSINGPERMS:}" >&2
    echo "!! refusing to apply a partial permission set (fail-closed); verify slugs against the running instance" >&2
    exit 1
    ;;
  *)
    echo "!! apply-agent-role: unexpected response from BookStack: ${out:-<empty>}" >&2
    exit 1
    ;;
esac
