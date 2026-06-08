#!/usr/bin/env bats
# ccc-wiki CLI end-to-end (issue #28). Builds the real CLI binary and drives it against
# the live test stack as an Agent-author token holder: create a book + page, update the
# page (which must add a page_revisions row — the #27 revision invariant), and confirm
# the HTTP-mapped exit codes (auth, not-found).
#
# The CLI runs INSIDE a throwaway container attached to the test stack's compose network,
# so the linux binary executes on any host (only Docker needed, matching the repo's
# promise) and reaches BookStack by its compose service name rather than the published
# host port. The token is passed via WIKI_API_TOKEN env — never a flag.
load "../helpers/load"

CLI_RUNNER_IMAGE="alpine:3.20"

setup_file() {
  load "../helpers/load"
  command -v docker >/dev/null 2>&1 || return 0

  # Build the CLI once (the same recipe as `make wiki-cli-build`); a static linux binary.
  docker run --rm -v "$REPO_ROOT":/work -w /work/services/wiki-cli -e CGO_ENABLED=0 \
    golang:1.23-alpine go build -trimpath -ldflags="-s -w" -o /work/services/wiki-cli/bin/ccc-wiki . \
    >/dev/null 2>&1 || skip "could not build the ccc-wiki binary"

  # Pre-pull the CLI runner image so a per-test `docker run` never emits pull progress
  # to stderr — bats merges stderr into $output, and on a cold runner that noise would
  # corrupt the JSON the tests parse (the CI failure that this guards against).
  docker pull "$CLI_RUNNER_IMAGE" >/dev/null 2>&1 || skip "could not pull the CLI runner image $CLI_RUNNER_IMAGE"

  # Resolve the test stack's compose network (mirrors run.sh's volume-name resolution).
  local net
  net="$(docker network ls --format '{{.Name}}' | grep -E "^${PROJECT}_default$" | head -1)"
  [ -n "$net" ] || net="${PROJECT}_default"
  echo "$net" > "$BATS_FILE_TMPDIR/net"

  # Provision the least-privilege Agent author role + user + token (mirrors
  # 09_agent_role.bats and deploy/local/apply-agent-role.sh) so the CLI authors content
  # under the same role #27 defines.
  AGENT_ROLE=$(dc exec -T bookstack php /app/www/artisan tinker 2>/dev/null <<'PHP'
$role = \BookStack\Users\Models\Role::firstOrCreate(
    ["display_name" => "Agent author"],
    ["description"  => "Least-privilege headless API role (issue #27)."]
);
$want = [
    "access-api",
    "book-view-all", "book-create-all", "book-update-all",
    "chapter-view-all", "chapter-create-all", "chapter-update-all",
    "page-view-all", "page-create-all", "page-update-all",
    "image-create-all", "attachment-create-all",
];
$ids = \BookStack\Permissions\Models\RolePermission::whereIn("name", $want)->pluck("id");
$role->permissions()->sync($ids->all());
echo "ROLEID:" . $role->id . ":";
PHP
)
  AGENT_ROLE=$(printf '%s' "$AGENT_ROLE" | sed -nE 's/.*ROLEID:([0-9]+):.*/\1/p' | head -1)
  [ -n "$AGENT_ROLE" ] || skip "could not provision the Agent author role via tinker"

  # Idempotency for --keep / repeated runs: drop any stale agent user + token first, so
  # the create below doesn't collide on a duplicate email left by a prior run.
  dbq "DELETE FROM api_tokens WHERE name='cli-agent-token';" >/dev/null 2>&1 || true
  dbq "DELETE FROM users WHERE email='cli-agent@example.test';" >/dev/null 2>&1 || true

  AGENT_USER=$(api POST /api/users "{\"name\":\"CLI Agent\",\"email\":\"cli-agent@example.test\",\"password\":\"AgentPass-78901\",\"roles\":[$AGENT_ROLE]}" | json '.id')

  dc exec -T bookstack php /app/www/artisan bookstack:regenerate-permissions >/dev/null 2>&1
  dc exec -T bookstack php /app/www/artisan cache:clear >/dev/null 2>&1

  TID="$(openssl rand -hex 16)"; SECRET="$(openssl rand -hex 16)"
  HASH="$(dc exec -T bookstack php -r 'echo password_hash($argv[1], PASSWORD_BCRYPT);' "$SECRET" 2>/dev/null)"
  dbq "DELETE FROM api_tokens WHERE name='cli-agent-token';" >/dev/null 2>&1 || true
  dbq "INSERT INTO api_tokens (name,token_id,secret,user_id,expires_at,created_at,updated_at)
       VALUES ('cli-agent-token','$TID','$HASH',$AGENT_USER,'2099-12-31',NOW(),NOW());" || skip "could not mint agent token"
  echo "$TID:$SECRET" > "$BATS_FILE_TMPDIR/agent_token"
}

teardown_file() {
  # Remove the agent token + user so a --keep stack doesn't accumulate orphans across
  # runs. The shared "Agent author" role is left in place (config-as-code, used by 09 +
  # the deploy script). Best-effort: the stack is torn down with `down -v` regardless.
  dbq "DELETE FROM api_tokens WHERE name='cli-agent-token';" >/dev/null 2>&1 || true
  dbq "DELETE FROM users WHERE email='cli-agent@example.test';" >/dev/null 2>&1 || true
  rm -rf "$REPO_ROOT/services/wiki-cli/bin" 2>/dev/null || true
}

# ccc TOKEN ARGS... — run the built CLI in a container on the stack network, token via env.
ccc() {
  local token="$1"; shift
  docker run --rm --network "$(cat "$BATS_FILE_TMPDIR/net")" \
    -v "$REPO_ROOT/services/wiki-cli/bin/ccc-wiki:/ccc-wiki:ro" \
    -e WIKI_BASE_URL="http://bookstack" \
    -e WIKI_API_TOKEN="$token" \
    -e WIKI_MAX_RETRIES="2" \
    --entrypoint /ccc-wiki \
    "$CLI_RUNNER_IMAGE" "$@"
}

agent_token() { cat "$BATS_FILE_TMPDIR/agent_token"; }

@test "CLI-001 create a book and a page (exit 0, --json id parses)" {
  run ccc "$(agent_token)" --json book create --name "CLI Book"
  assert_status 0 "$status" "book create must exit 0 (output: $output)"
  bid="$(printf '%s' "$output" | json '.id')"
  { [ -n "$bid" ] && [ "$bid" != "null" ]; } || flunk "no book id in --json output: $output"

  run ccc "$(agent_token)" --json page create --book "$bid" --name "CLI Page" --markdown "# v1"
  assert_status 0 "$status" "page create must exit 0 (output: $output)"
  pid="$(printf '%s' "$output" | json '.id')"
  { [ -n "$pid" ] && [ "$pid" != "null" ]; } || flunk "no page id in --json output: $output"
  echo "$pid" > "$BATS_FILE_TMPDIR/page_id"
}

@test "CLI-002 page update advances the revision chain (#27 invariant, via dbq)" {
  pid="$(cat "$BATS_FILE_TMPDIR/page_id")"
  before="$(dbq "SELECT COUNT(*) FROM page_revisions WHERE page_id=$pid;")"
  run ccc "$(agent_token)" --json page update --id "$pid" --markdown "# v2 updated"
  assert_status 0 "$status" "page update must exit 0 (output: $output)"
  after="$(dbq "SELECT COUNT(*) FROM page_revisions WHERE page_id=$pid;")"
  assert_ge "${after:-0}" "$(( ${before:-0} + 1 ))" "update must add >=1 page_revisions row ($before -> $after)"
}

@test "CLI-003 a tampered token is rejected with an auth-class exit code (3 or 4)" {
  tid="$(cut -d: -f1 < "$BATS_FILE_TMPDIR/agent_token")"
  run ccc "$tid:totally-wrong-secret" --json book create --name "Nope"
  assert_status_in "3 4" "$status" "a wrong-secret token must map to auth(3)/forbidden(4), got $status"
}

@test "CLI-004 page get of a nonexistent id maps to the not-found exit code (4)" {
  run ccc "$(agent_token)" --json page get --id 999999999
  assert_status 4 "$status" "a missing page must map to the not-found exit code (4), got $status"
}
