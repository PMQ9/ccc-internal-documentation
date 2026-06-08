#!/usr/bin/env bats
# Agent author role — least-privilege RBAC for headless API access (issue #27).
# An Agent-author token must be able to create/update books and pages, upload
# attachments, but NEVER delete, NEVER reach admin surfaces (/api/users), and
# NEVER see shelves if that role lacks shelf access.
load "../helpers/load"

setup_file() {
  load "../helpers/load"
  # Provision the least-privilege "Agent author" role THROUGH the app (artisan
  # tinker), mirroring deploy/local/apply-agent-role.sh. The BookStack REST API has
  # NO role-permission sub-resource, so the role's permission pivot must be sync()'d
  # in-app and the denormalized effective-permission cache regenerated — that is what
  # actually lets the agent token author content (AGENT-001..004). firstOrCreate keeps
  # this idempotent whether or not a prior deploy already created the role. Slugs are
  # the same allowlist apply-agent-role.sh verified against v26.05-ls265.
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
  echo "$AGENT_ROLE" > "$BATS_FILE_TMPDIR/agent_role_id"

  AGENT_USER=$(api POST /api/users "{\"name\":\"CI Agent\",\"email\":\"ci-agent@example.test\",\"password\":\"AgentPass-78901\",\"roles\":[$AGENT_ROLE]}" | json '.id')
  echo "$AGENT_USER" > "$BATS_FILE_TMPDIR/agent_user_id"

  # Rebuild the denormalized effective-permissions (the synced role pivot + the new
  # user's role assignment) and bust the cache so the grant is live before the tests.
  dc exec -T bookstack php /app/www/artisan bookstack:regenerate-permissions >/dev/null 2>&1
  dc exec -T bookstack php /app/www/artisan cache:clear >/dev/null 2>&1

  TID="$(openssl rand -hex 16)"; SECRET="$(openssl rand -hex 16)"
  HASH="$(dc exec -T bookstack php -r 'echo password_hash($argv[1], PASSWORD_BCRYPT);' "$SECRET" 2>/dev/null)"
  dbq "DELETE FROM api_tokens WHERE name='ci-agent-token';" >/dev/null 2>&1 || true
  dbq "INSERT INTO api_tokens (name,token_id,secret,user_id,expires_at,created_at,updated_at)
       VALUES ('ci-agent-token','$TID','$HASH',$AGENT_USER,'2099-12-31',NOW(),NOW());" || skip "could not mint agent token"
  echo "$TID:$SECRET" > "$BATS_FILE_TMPDIR/agent_token"
}

agent_api() { req_body "$(cat "$BATS_FILE_TMPDIR/agent_token")" "$1" "$2" "${3:-}"; }
agent_api_status() { req_status "$(cat "$BATS_FILE_TMPDIR/agent_token")" "$1" "$2" "${3:-}"; }

@test "AGENT-001 agent can create a book (2xx)" {
  run agent_api_status POST /api/books '{"name":"Agent Book","description":"created by agent"}'
  assert_status_in "200 201" "$output" "agent must be able to create a book"
}

@test "AGENT-002 agent can update a book (2xx)" {
  bid=$(agent_api POST /api/books '{"name":"Agent Book 2"}' | json '.id')
  run agent_api_status PUT "/api/books/$bid" '{"name":"Agent Book 2 Updated"}'
  assert_status_in "200 201" "$output" "agent must be able to update a book"
}

@test "AGENT-003 agent can create and update a page (2xx)" {
  bid=$(agent_api POST /api/books '{"name":"Agent Book 3"}' | json '.id')
  run agent_api_status POST /api/pages "{\"book_id\":$bid,\"name\":\"Agent Page\",\"markdown\":\"# from agent\"}"
  assert_status_in "200 201" "$output" "agent must be able to create a page"
  pid=$(agent_api POST /api/pages "{\"book_id\":$bid,\"name\":\"Agent Page 2\",\"markdown\":\"# v1\"}" | json '.id')
  run agent_api_status PUT "/api/pages/$pid" '{"markdown":"# v2"}'
  assert_status_in "200 201" "$output" "agent must be able to update a page"
}

@test "AGENT-004 agent can upload an attachment (2xx)" {
  bid=$(agent_api POST /api/books '{"name":"Agent Book 4"}' | json '.id')
  pid=$(agent_api POST /api/pages "{\"book_id\":$bid,\"name\":\"Agent Attach Page\",\"markdown\":\"# attach\"}" | json '.id')
  printf 'agent attachment payload\n' > "$BATS_FILE_TMPDIR/agent-attach.txt"
  run curl -s -o /dev/null -w '%{http_code}' \
    -H "Authorization: Token $(cat "$BATS_FILE_TMPDIR/agent_token")" \
    -F "uploaded_to=$pid" -F "name=agent-attach.txt" \
    -F "file=@$BATS_FILE_TMPDIR/agent-attach.txt;filename=agent-attach.txt" \
    "$BASE_URL/api/attachments"
  assert_status_in "200 201" "$output" "agent must be able to upload an attachment"
}

@test "AGENT-005 agent CANNOT delete a book (403)" {
  bid=$(agent_api POST /api/books '{"name":"Agent Delete Test"}' | json '.id')
  run agent_api_status DELETE "/api/books/$bid"
  assert_status 403 "$output" "agent must be denied delete (least-privilege invariant)"
}

@test "AGENT-006 agent CANNOT access admin surfaces (403)" {
  run agent_api_status GET /api/users
  assert_status 403 "$output" "agent must be denied /api/users (admin surface)"
}

@test "AGENT-007 agent CANNOT access settings (403)" {
  run agent_api_status GET /api/roles
  assert_status 403 "$output" "agent must be denied /api/roles (admin surface)"
}

@test "AGENT-008 agent CANNOT delete a page (403)" {
  bid=$(agent_api POST /api/books '{"name":"Agent Page Del"}' | json '.id')
  pid=$(agent_api POST /api/pages "{\"book_id\":$bid,\"name\":\"To Delete\",\"markdown\":\"# x\"}" | json '.id')
  run agent_api_status DELETE "/api/pages/$pid"
  # 403, not 404: the role has page-view-all, so the agent CAN see the page it just
  # created — the denial is on the missing delete permission, not on visibility
  # (mirrors AGENT-005's strict 403 for book delete).
  assert_status 403 "$output" "agent must be denied page delete (sees the page, lacks delete)"
}

@test "AGENT-009 agent CANNOT create a user (403)" {
  # Distinct from AGENT-006's GET: a least-privilege role must not be able to mint
  # accounts (privilege-escalation guard), not merely be blind to the list.
  run agent_api_status POST /api/users '{"name":"Sneaky","email":"sneaky@example.test","password":"SneakyPass-123"}'
  assert_status 403 "$output" "agent must be denied user creation (no privilege escalation)"
}

@test "AGENT-010 a tampered agent token is rejected (401/403)" {
  # Flip the secret half of the token; the id is real but the secret won't verify,
  # so the same least-privilege identity can't be impersonated by guessing.
  tid="$(cut -d: -f1 < "$BATS_FILE_TMPDIR/agent_token")"
  run req_status "$tid:totally-wrong-secret" GET /api/books
  assert_status_in "401 403" "$output" "a token with a wrong secret must be rejected"
}
