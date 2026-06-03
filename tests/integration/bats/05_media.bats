#!/usr/bin/env bats
# FN-004 — media durability. Uploaded attachments and images land on the
# persistent volume (/config), addressed by a hashed on-disk name; the display
# name is metadata only. Mirrors deploy/local/verify.sh §4 (validated green).
load "../helpers/load"

setup_file() {
  load "../helpers/load"
  BOOK_ID=$(api POST /api/books '{"name":"Media Book"}' | json '.id')
  PAGE_ID=$(api POST /api/pages "{\"book_id\":$BOOK_ID,\"name\":\"Media Page\",\"markdown\":\"# media\"}" | json '.id')
  echo "$PAGE_ID" > "$BATS_FILE_TMPDIR/page_id"

  printf 'attachment payload for media durability test\n' > "$BATS_FILE_TMPDIR/verify-media.txt"
  # Minimal valid 1x1 PNG (same bytes as verify.sh).
  printf '\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82' \
    > "$BATS_FILE_TMPDIR/verify-media.png"
}

@test "T-008 uploaded attachment is written under the persistent volume /config" {
  local page_id base found
  page_id=$(cat "$BATS_FILE_TMPDIR/page_id")
  run curl -s -o /dev/null -w '%{http_code}' \
    -H "Authorization: Token $ADMIN_TOKEN" \
    -F "uploaded_to=$page_id" -F "name=verify-media.txt" \
    -F "file=@$BATS_FILE_TMPDIR/verify-media.txt;filename=verify-media.txt" \
    "$BASE_URL/api/attachments"
  assert_status_in "200 201" "$output" "attachment upload should succeed"

  base=$(dbq "SELECT path FROM attachments WHERE name='verify-media.txt' ORDER BY id DESC LIMIT 1;" | awk -F/ '{print $NF}')
  [ -n "$base" ] || flunk "no attachment row found in DB"
  found=$(dc exec -T bookstack sh -lc "find /config -type f -name '$base' 2>/dev/null | head -1")
  [ -n "$found" ] || flunk "attachment '$base' not found anywhere under /config"
}

@test "T-009 uploaded image is written under the persistent volume /config" {
  local page_id found
  page_id=$(cat "$BATS_FILE_TMPDIR/page_id")
  run curl -s -o /dev/null -w '%{http_code}' \
    -H "Authorization: Token $ADMIN_TOKEN" \
    -F "type=gallery" -F "uploaded_to=$page_id" \
    -F "image=@$BATS_FILE_TMPDIR/verify-media.png;filename=verify-media.png" \
    "$BASE_URL/api/image-gallery"
  assert_status_in "200 201" "$output" "image upload should succeed"

  found=$(dc exec -T bookstack sh -lc 'find /config -type f -path "*uploads/images*" -name "verify-media*.png" 2>/dev/null | head -1')
  [ -n "$found" ] || flunk "uploaded image not found under /config/.../uploads/images"
}
