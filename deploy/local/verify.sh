#!/usr/bin/env bash
# Automated portion of the Phase-0 verification (run ON connor-server, in deploy/local/).
# Proves the deployment-specific behaviors: auth gating, content + revision history,
# attachment/image storage landing on the persistent volume. The UI-level role-button
# checks and the public-viewing toggle are in README.md §4 (a quick human pass).
#
# Usage:  cd deploy/local && ./verify.sh
set -euo pipefail

B="${BASE_URL:-http://127.0.0.1}"
cd "$(dirname "$0")"
APPW="$(grep '^DB_PASSWORD' .env | cut -d= -f2-)"

dbq(){ docker compose exec -T db mariadb -h 127.0.0.1 -u bookstack -p"$APPW" bookstackapp -N -B -e "$1"; }
# Pull the first numeric "id" out of a BookStack create response (jq-free so this stays
# self-contained on connor-server; do NOT source tests/lib here — verify.sh ships with deploy/local).
first_id(){ grep -oE '"id":[0-9]+' | head -1 | cut -d: -f2; }
pass(){ echo "  PASS: $1"; }
fail(){ echo "  FAIL: $1"; FAILED=1; }
FAILED=0
TMP=

cleanup(){
  [ -n "$TMP" ] && rm -f "$TMP" "$TMP.png"
  [ -n "${BOOK_ID:-}" ] && curl -s -o /dev/null -X DELETE -H "${AUTH:-}" "$B/api/books/$BOOK_ID" || true
}
trap cleanup EXIT

echo "=== 1. Anonymous gating (read-without-public-toggle is blocked; edit/admin require login) ==="
for path in / /settings/users /books/create; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$B$path")
  [ "$code" = "302" ] && pass "anon $path -> 302 (login)" || fail "anon $path -> $code (expected 302)"
done

echo "=== 2. Mint an admin API token (deterministic, via DB) ==="
TID=$(openssl rand -hex 16); SECRET=$(openssl rand -hex 16)
HASH=$(docker compose exec -T bookstack php -r 'echo password_hash($argv[1], PASSWORD_BCRYPT);' "$SECRET")
dbq "DELETE FROM api_tokens WHERE name='verify-script';"
dbq "INSERT INTO api_tokens (name,token_id,secret,user_id,expires_at,created_at,updated_at)
     VALUES ('verify-script','$TID','$HASH',1,'2099-12-31',NOW(),NOW());"
AUTH="Authorization: Token $TID:$SECRET"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "$AUTH" "$B/api/books")
[ "$code" = "200" ] && pass "API token authenticates (GET /api/books -> 200)" || fail "API token -> $code"

echo "=== 3. Create content + edit -> revision history ==="
BOOK_ID=$(curl -s -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"name":"Verify Book","description":"created by verify.sh"}' "$B/api/books" | first_id)
PAGE_ID=$(curl -s -H "$AUTH" -H 'Content-Type: application/json' \
  -d "{\"book_id\":$BOOK_ID,\"name\":\"Verify Page\",\"markdown\":\"# v1\\nfirst version\"}" "$B/api/pages" | first_id)
echo "  created book=$BOOK_ID page=$PAGE_ID"
curl -s -o /dev/null -X PUT -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"markdown":"# v2\nsecond version, edited"}' "$B/api/pages/$PAGE_ID"
curl -s -o /dev/null -X PUT -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"markdown":"# v3\nthird version, edited again"}' "$B/api/pages/$PAGE_ID"
REVS=$(dbq "SELECT COUNT(*) FROM page_revisions WHERE page_id=$PAGE_ID;")
[ "${REVS:-0}" -ge 2 ] && pass "page has $REVS revisions (history is recorded)" || fail "page has $REVS revisions (expected >=2)"

echo "=== 4. Attachment + image upload land on the persistent volume ==="
TMP=$(mktemp); echo "attachment payload $(date +%s)" > "$TMP"
ATTACH_RESP=$(curl -s -H "$AUTH" -F "uploaded_to=$PAGE_ID" -F "name=verify.txt" -F "file=@$TMP;filename=verify.txt" "$B/api/attachments")
echo "  attachment resp: $(echo "$ATTACH_RESP" | cut -c1-120)"
# tiny 1x1 PNG
printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82' > "$TMP.png"
IMG_RESP=$(curl -s -H "$AUTH" -F "type=gallery" -F "uploaded_to=$PAGE_ID" -F "image=@$TMP.png;filename=verify.png" "$B/api/image-gallery")
echo "  image resp: $(echo "$IMG_RESP" | cut -c1-120)"
# Attachments + images are stored with hashed names under /config (the persistent volume);
# the display name (verify.txt) is metadata only. Confirm via the real on-disk basename.
ATT_BASE=$(dbq "SELECT path FROM attachments WHERE name='verify.txt' ORDER BY id DESC LIMIT 1;" | awk -F/ '{print $NF}')
ATT_FOUND=$(docker compose exec -T bookstack sh -c "find /config -type f -name '$ATT_BASE' 2>/dev/null | head -1")
IMG_FOUND=$(docker compose exec -T bookstack sh -c 'find /config -type f -path "*uploads/images*" -name "verify.png" 2>/dev/null | head -1')
[ -n "$ATT_FOUND" ] && pass "attachment on persistent volume: $ATT_FOUND" || fail "attachment not found under /config"
[ -n "$IMG_FOUND" ] && pass "image on persistent volume: $IMG_FOUND" || fail "image not found under /config"

rm -f "$TMP" "$TMP.png"
echo "=== RESULT ==="
[ "$FAILED" = "0" ] && echo "ALL AUTOMATED CHECKS PASSED" || { echo "SOME CHECKS FAILED"; exit 1; }
