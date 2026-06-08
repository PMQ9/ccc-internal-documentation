#!/usr/bin/env bats
# Negative / failure-mode inputs: the API must reject bad requests cleanly (4xx),
# never a 5xx, and never silently accept them.
load "../helpers/load"

@test "N-1 malformed JSON body -> clean 4xx (not 500)" {
  run bash -c 'curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Token $ADMIN_TOKEN" -H "Content-Type: application/json" -d "{not valid json" "$BASE_URL/api/books"'
  assert_status_in "400 422" "$output" "malformed JSON must be a clean 4xx"
}

@test "N-2 missing required field (page without book_id) -> 422" {
  run api_status POST /api/pages '{"name":"orphan","markdown":"# x"}'
  assert_status_in "422 400" "$output" "creating a page without a parent must be rejected as a validation error"
}

@test "N-3 GET a non-existent page -> 404" {
  run api_status GET /api/pages/99999999
  assert_status 404 "$output" "reading a missing resource must be 404, not 500"
}

@test "N-4 DELETE a non-existent book -> 404" {
  run api_status DELETE /api/books/99999999
  assert_status 404 "$output" "deleting a missing resource must be 404"
}

@test "N-5 empty-name book is rejected (required-field validation)" {
  run api_status POST /api/books '{"name":""}'
  assert_status_in "422 400" "$output" "empty required name must fail validation"
}

@test "N-6 UPDATE a non-existent page -> 404 (not 500)" {
  run api_status PUT /api/pages/99999999 '{"name":"ghost"}'
  assert_status 404 "$output" "updating a missing resource must be 404, not a server error"
}

@test "N-7 create a page under a non-existent parent chapter -> clean 4xx" {
  # An orphan reference (foreign-key target missing) must be a validation/not-found
  # error, never a 5xx that leaks a stack trace.
  run api_status POST /api/pages '{"chapter_id":99999999,"name":"orphan","markdown":"# x"}'
  assert_status_in "404 422 400" "$output" "a dangling chapter_id must be a clean 4xx"
}

@test "N-8 malformed JSON to /api/pages -> clean 4xx (not 500)" {
  run bash -c 'curl -s -o /dev/null -w "%{http_code}" -H "Authorization: Token $ADMIN_TOKEN" -H "Content-Type: application/json" -d "{\"name\": " "$BASE_URL/api/pages"'
  assert_status_in "400 422" "$output" "malformed JSON to pages must be a clean 4xx"
}
