#!/usr/bin/env bats
# Agent author role — least-privilege RBAC for headless API access (issue #27).
# An Agent-author token must be able to create/update books and pages, upload
# attachments, but NEVER delete, NEVER reach admin surfaces (/api/users), and
# NEVER see shelves if that role lacks shelf access.
load "../helpers/load"

setup_file() {
  load "../helpers/load"
  # The Agent author role is provisioned by apply-agent-role.sh during deploy.
  # For CI we create an agent user + token via the admin API.
  ROLES_JSON="$(api GET /api/roles)"
  AGENT_ROLE="$(printf '%s' "$ROLES_JSON" | jq -r '.data[]? | select(.display_name=="Agent author") | .id' | head -1)"
  if [ -z "$AGENT_ROLE" ]; then
    echo "Agent author role not found; creating it inline for CI" >&2
    AGENT_ROLE=$(api POST /api/roles '{"display_name":"Agent author","description":"Least-privilege headless API access","mfa_enabled":false}' | json '.id')
    # Grant exactly the permissions the runbook specifies.
    for perm in "access-api" "book-create-all" "book-update-all" "page-create-all" "page-update-all" "chapter-create-all" "chapter-update-all" "attachment-create-all" "image-create-all"; do
      api POST "/api/roles/$AGENT_ROLE/permissions" "{\"name\":\"$perm\"}" >/dev/null 2>&1 || true
    done
  fi
  echo "$AGENT_ROLE" > "$BATS_FILE_TMPDIR/agent_role_id"

  AGENT_USER=$(api POST /api/users "{\"name\":\"CI Agent\",\"email\":\"ci-agent@example.test\",\"password\":\"AgentPass-78901\",\"roles\":[$AGENT_ROLE]}" | json '.id')
  echo "$AGENT_USER" > "$BATS_FILE_TMPDIR/agent_user_id"

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
  pid=$(agent_api POST /api/pages "{\"book_id\":$bid,\"name":"Agent Attach Page\",\"markdown\":\"# attach\"}" | json '.id')
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
  assert_status_in "403 404" "$output" "agent must be denied page delete"
}
