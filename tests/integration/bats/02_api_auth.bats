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
