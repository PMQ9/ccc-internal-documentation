#!/usr/bin/env bats
# FN-003 — revision history. Every edit records a revision; old content is
# retained (recoverable), not overwritten. The one-click *restore* UI action is
# the manual V9 check in deploy/local/README.md; here we prove the durable
# property that makes restore possible — history is preserved with old content.
load "../helpers/load"

setup_file() {
  load "../helpers/load"
  BOOK_ID=$(api POST /api/books '{"name":"Revisions Book","description":"rev test"}' | json '.id')
  PAGE_ID=$(api POST /api/pages "{\"book_id\":$BOOK_ID,\"name\":\"Rev Page\",\"markdown\":\"# v1 alpha-rev-marker\\nfirst\"}" | json '.id')
  api PUT "/api/pages/$PAGE_ID" '{"markdown":"# v2 bravo-rev-marker\nsecond"}' >/dev/null
  api PUT "/api/pages/$PAGE_ID" '{"markdown":"# v3 charlie-rev-marker\nthird"}' >/dev/null
  echo "$PAGE_ID" > "$BATS_FILE_TMPDIR/page_id"
}

@test "T-006 a created+twice-edited page records >= 2 revisions" {
  local page_id revs
  page_id=$(cat "$BATS_FILE_TMPDIR/page_id")
  revs=$(dbq "SELECT COUNT(*) FROM page_revisions WHERE page_id=$page_id;")
  assert_ge "${revs:-0}" 2 "history must record at least 2 revisions after create + 2 edits"
}

@test "T-007 history retains the OLDEST content (recoverable, not overwritten)" {
  local page_id n
  page_id=$(cat "$BATS_FILE_TMPDIR/page_id")
  n=$(dbq "SELECT COUNT(*) FROM page_revisions WHERE page_id=$page_id AND html LIKE '%alpha-rev-marker%';")
  assert_ge "${n:-0}" 1 "the v1 content must still exist in revision history"
}

@test "T-007 history contains the NEWEST content" {
  local page_id n
  page_id=$(cat "$BATS_FILE_TMPDIR/page_id")
  n=$(dbq "SELECT COUNT(*) FROM page_revisions WHERE page_id=$page_id AND html LIKE '%charlie-rev-marker%';")
  assert_ge "${n:-0}" 1 "the latest content must be recorded as a revision"
}

@test "T-007 the live page currently renders the newest content" {
  local page_id body
  page_id=$(cat "$BATS_FILE_TMPDIR/page_id")
  body=$(api GET "/api/pages/$page_id")
  assert_contains "$body" "charlie-rev-marker" "live page should reflect the most recent edit"
}
