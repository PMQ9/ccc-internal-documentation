#!/usr/bin/env bats
# Edge cases — input size, Unicode, HTML/JS escaping, and bulk aggregates.
# Covers T-020 (large + Unicode + XSS-safe page) and T-021 (bulk entities stay
# responsive).
load "../helpers/load"

# Large-content size. The true ceiling is nginx client_max_body_size in the
# image (an infra setting); default here is ~1 MB, comfortably "large" while
# under the common 1m limit. Tunable via LARGE_PAGE_BYTES.
LARGE_PAGE_BYTES="${LARGE_PAGE_BYTES:-1000000}"
BULK_COUNT="${BULK_COUNT:-50}"

setup_file() {
  load "../helpers/load"
  BOOK_ID=$(api POST /api/books '{"name":"Edge Book"}' | json '.id')
  echo "$BOOK_ID" > "$BATS_FILE_TMPDIR/book_id"
}

@test "T-020 large + Unicode + special-char page is accepted and round-trips" {
  local book_id content_file body_file page_id md html
  book_id=$(cat "$BATS_FILE_TMPDIR/book_id")
  content_file="$BATS_FILE_TMPDIR/large.md"
  body_file="$BATS_FILE_TMPDIR/large.json"

  {
    printf '# Large Page edge-marker-LARGE\n\n'
    # Unicode: emoji, RTL (Hebrew), a combining accent, plus shell/HTML/JS-special chars.
    printf 'unicode: 😀 emoji | מבחן RTL | e\xcc\x81 combining | special <script>alert(1)</script> & "q" %s\n\n' '\backslash'
    head -c "$LARGE_PAGE_BYTES" /dev/zero | tr '\0' 'A'
    printf '\n'
  } > "$content_file"

  # jq builds valid JSON regardless of the payload's bytes (handles all escaping).
  jq -n --rawfile md "$content_file" --argjson bid "$book_id" \
    '{book_id:$bid, name:"Large Edge Page", markdown:$md}' > "$body_file"

  run bash -c 'curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Token $ADMIN_TOKEN" -H "Content-Type: application/json" --data @"'"$body_file"'" "$BASE_URL/api/pages"'
  assert_status_in "200 201" "$output" "a ~${LARGE_PAGE_BYTES}-byte page should be accepted (see nginx client_max_body_size if 413)"

  page_id=$(jq -n --rawfile md "$content_file" --argjson bid "$book_id" '{book_id:$bid,name:"Large Edge Page",markdown:$md}' \
    | curl -s -H "Authorization: Token $ADMIN_TOKEN" -H 'Content-Type: application/json' --data @- "$BASE_URL/api/pages" | json '.id')

  md=$(api GET "/api/pages/$page_id" | json '.markdown')
  assert_contains "$md" "edge-marker-LARGE" "stored markdown must round-trip the content marker"
  assert_contains "$md" "😀" "stored markdown must round-trip Unicode (emoji)"

  # The rendered HTML must be XSS-safe: the raw <script> must NOT survive into html.
  html=$(api GET "/api/pages/$page_id" | json '.html')
  refute_contains "$html" "<script>alert(1)</script>" "rendered HTML must sanitize/escape injected <script>"
}

@test "T-021 bulk-create ${BULK_COUNT} books; list + read stay responsive" {
  local ok=0 i code
  for i in $(seq 1 "$BULK_COUNT"); do
    code=$(api_status POST /api/books "{\"name\":\"Bulk Book $i\"}")
    case "$code" in 200|201) ok=$((ok + 1)) ;; esac
  done
  assert_ge "$ok" "$BULK_COUNT" "all $BULK_COUNT bulk creates should succeed (got $ok)"

  # Responsiveness: the list must answer within a hard wall-clock bound (curl --max-time).
  run bash -c 'curl -s --max-time 15 -o /dev/null -w "%{http_code}" -H "Authorization: Token $ADMIN_TOKEN" "$BASE_URL/api/books?count=100"'
  assert_status 200 "$output" "the books list must return 200 within 15s even with many entities"
}
