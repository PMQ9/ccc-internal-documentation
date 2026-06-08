#!/usr/bin/env bats
# FN-008 — API authentication. A valid token authenticates; an absent or invalid
# token is rejected (not silently accepted, not a 500).
load "../helpers/load"

@test "T-013 valid token: GET /api/books -> 200" {
  run api_status GET /api/books
  assert_status 200 "$output"
}

@test "T-014 no token: GET /api/books is rejected (401/403, never 200)" {
  run req_status "" GET /api/books
  assert_status_in "401 403" "$output" "unauthenticated API call must be rejected"
}

@test "T-014 garbage token: GET /api/books is rejected (401/403, never 200/500)" {
  run req_status "deadbeef:not-a-real-secret" GET /api/books
  assert_status_in "401 403" "$output" "invalid token must be rejected without a server error"
}

@test "T-014 malformed Authorization header is rejected, not a 500" {
  run bash -c 'curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Token only-one-part" "$BASE_URL/api/books"'
  assert_status_in "401 403" "$output" "malformed token header must be a clean 4xx, not a 5xx"
}

@test "T-022 create a user via API then read/update/delete" {
  local uid nu nu2
  uid=$(api POST /api/users '{"name":"CI Temp User","email":"ci-temp@example.test","password":"TempPass-99999"}' | json '.id')
  [ -n "$uid" ] && [ "$uid" != "null" ] || flunk "create user returned no id"

  # Read the user back.
  nu=$(api GET "/api/users/$uid" | json '.name')
  assert_equal "CI Temp User" "$nu" "created user name should round-trip"

  # Update the user name.
  nu2=$(api PUT "/api/users/$uid" '{"name":"CI Temp Updated"}' | json '.name')
  assert_equal "CI Temp Updated" "$nu2" "updated user name should persist"

  # Delete the user.
  run api_status DELETE "/api/users/$uid"
  assert_status_in "200 204" "$output" "admin must be able to delete a user"

  # Verify deletion.
  run api_status GET "/api/users/$uid"
  assert_status 404 "$output" "deleted user should return 404"
}

@test "T-022 create a shelf via API then read/update/delete" {
  local sid ns
  sid=$(api POST /api/shelves '{"name":"CI Test Shelf","description":"shelf api test"}' | json '.id')
  [ -n "$sid" ] && [ "$sid" != "null" ] || flunk "create shelf returned no id"

  ns=$(api GET "/api/shelves/$sid" | json '.name')
  assert_equal "CI Test Shelf" "$ns" "created shelf name should round-trip"

  ns=$(api PUT "/api/shelves/$sid" '{"name":"CI Shelf Updated"}' | json '.name')
  assert_equal "CI Shelf Updated" "$ns" "updated shelf name should persist"

  run api_status DELETE "/api/shelves/$sid"
  assert_status_in "200 204" "$output" "admin must be able to delete a shelf"

  run api_status GET "/api/shelves/$sid"
  assert_status 404 "$output" "deleted shelf should return 404"
}

@test "T-022 create a chapter via API then read/update/delete" {
  local bid cid nc
  bid=$(api POST /api/books '{"name":"Chapter Test Book"}' | json '.id')
  cid=$(api POST /api/chapters "{\"book_id\":$bid,\"name\":\"CI Test Chapter\"}" | json '.id')
  [ -n "$cid" ] && [ "$cid" != "null" ] || flunk "create chapter returned no id"

  nc=$(api GET "/api/chapters/$cid" | json '.name')
  assert_equal "CI Test Chapter" "$nc" "created chapter name should round-trip"

  nc=$(api PUT "/api/chapters/$cid" '{"name":"CI Chapter Updated"}' | json '.name')
  assert_equal "CI Chapter Updated" "$nc" "updated chapter name should persist"

  run api_status DELETE "/api/chapters/$cid"
  assert_status_in "200 204" "$output" "admin must be able to delete a chapter"

  run api_status GET "/api/chapters/$cid"
  assert_status 404 "$output" "deleted chapter should return 404"
}

@test "T-022 list all users (admin only)" {
  run api_status GET /api/users
  assert_status 200 "$output" "admin must be able to list users"
}

@test "T-022 list all shelves" {
  run api_status GET /api/shelves
  assert_status 200 "$output" "list shelves should succeed"
}

@test "T-022 list all chapters for a book" {
  local bid
  bid=$(api POST /api/books '{"name":"Book For Chapters"}' | json '.id')
  run api_status GET "/api/books/$bid"
  assert_status 200 "$output" "listing chapters via book should succeed"
}

@test "T-023 expired token is rejected (401/403)" {
  local tid secret hash
  tid="expired-$(openssl rand -hex 8)"
  secret="expired-secret"
  hash=$(dc exec -T bookstack php -r 'echo password_hash($argv[1], PASSWORD_BCRYPT);' "$secret" 2>/dev/null)
  dbq "DELETE FROM api_tokens WHERE name='ci-expired-test';" >/dev/null 2>&1 || true
  dbq "INSERT INTO api_tokens (name,token_id,secret,user_id,expires_at,created_at,updated_at)
       VALUES ('ci-expired-test','$tid','$hash',1,'2020-01-01',NOW(),NOW());" || skip "could not mint expired token"
  run req_status "$tid:$secret" GET /api/books
  assert_status_in "401 403" "$output" "expired token must be rejected"
}
