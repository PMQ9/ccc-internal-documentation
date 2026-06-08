#!/usr/bin/env bats
# FN-005 / T-010 — persistence across `down && up` (no -v). Content and media
# survive a process restart because they live on Docker named volumes, not in
# the container layer. Runs only on the `pr` and `full` profiles.
load "../helpers/load"

setup_file() {
  load "../helpers/load"
  # Guard the admin credential up front: invoked outside run.sh (which exports it),
  # the api/dbq helpers would send an empty token and fail with opaque errors.
  [ -n "${ADMIN_TOKEN:-}" ] || skip "ADMIN_TOKEN not set (run via tests/integration/run.sh)"
  # We only run this test file when the stack goes through down/up; the
  # orchestrator (run.sh) signals this via PERSIST_BOOK_ID / PERSIST_PAGE_ID.
  if [ -z "${PERSIST_BOOK_ID:-}" ]; then
    PB=$(api POST /api/books '{"name":"Persist Book"}' | json '.id')
    PP=$(api POST /api/pages "{\"book_id\":$PB,\"name\":\"Persist Page\",\"markdown\":\"# persist-marker-PERSIST\"}" | json '.id')
    printf 'persist attachment\n' > "$BATS_FILE_TMPDIR/persist.txt"
    curl -s -o /dev/null -H "Authorization: Token $ADMIN_TOKEN" \
      -F "uploaded_to=$PP" -F "name=persist.txt" -F "file=@$BATS_FILE_TMPDIR/persist.txt;filename=persist.txt" \
      "$BASE_URL/api/attachments"
    ATT_BASE=$(dbq "SELECT path FROM attachments WHERE name='persist.txt' ORDER BY id DESC LIMIT 1;" | awk -F/ '{print $NF}')
    echo "$PB" > "$BATS_FILE_TMPDIR/persist_book_id"
    echo "$PP" > "$BATS_FILE_TMPDIR/persist_page_id"
    echo "$ATT_BASE" > "$BATS_FILE_TMPDIR/persist_att_base"
  fi
}

# These tests run AFTER the orchestrator has done down+up. The runner.sh
# exports PERSIST_BOOK_ID and PERSIST_PAGE_ID for us.
@test "T-010 page content survived down/up" {
  if [ -z "${PERSIST_PAGE_ID:-}" ]; then
    skip "PERSIST_PAGE_ID not set (not in persistence profile)"
  fi
  run poll_contains 60 "persist-marker-PERSIST" GET "/api/pages/$PERSIST_PAGE_ID"
  assert_status 0 "$status" "page content must survive docker compose down && up"
}

@test "T-010 page is readable after down/up" {
  if [ -z "${PERSIST_PAGE_ID:-}" ]; then
    skip "PERSIST_PAGE_ID not set (not in persistence profile)"
  fi
  run poll_status 200 30 GET "/api/pages/$PERSIST_PAGE_ID"
  assert_status 0 "$status" "page must be readable via API after restart"
}

@test "T-010 book list includes the persist book after down/up" {
  if [ -z "${PERSIST_BOOK_ID:-}" ]; then
    skip "PERSIST_BOOK_ID not set (not in persistence profile)"
  fi
  run poll_contains 30 "\"id\":$PERSIST_BOOK_ID" GET /api/books
  assert_status 0 "$status" "book must reappear in listing after restart"
}

@test "T-010 attachment metadata survived down/up" {
  if [ -z "${PERSIST_ATT_BASE:-}" ]; then
    skip "PERSIST_ATT_BASE not set (not in persistence profile)"
  fi
  # Poll: after restart it may take a moment for the API to fully come online.
  local found="" deadline=$((SECONDS + 60))
  while [ "$SECONDS" -lt "$deadline" ]; do
    found=$(dc exec -T bookstack sh -lc "find /config -type f -name '$PERSIST_ATT_BASE' 2>/dev/null | head -1" 2>/dev/null || true)
    [ -n "$found" ] && break
    sleep 3
  done
  [ -n "$found" ] || flunk "attachment '$PERSIST_ATT_BASE' not found after down/up"
}

@test "T-010 admin token still authenticates after restart" {
  if [ -z "${ADMIN_TOKEN:-}" ]; then
    skip "no ADMIN_TOKEN"
  fi
  run poll_status 200 30 GET /api/books
  assert_status 0 "$status" "admin API token must still authenticate after restart"
}
