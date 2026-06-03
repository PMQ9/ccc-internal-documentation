#!/usr/bin/env bats
# Smoke: the stack is up and the DB-free health surface answers.
load "../helpers/load"

@test "S-1 /icon.png returns 200 (the ALB health-check target)" {
  run http_status /icon.png
  assert_status 200 "$output" "/icon.png must be 200 — it is the health-check contract (OPS-002/PERF-001)"
}

@test "S-2 /login form is reachable (break-glass surface always present)" {
  run http_status /login
  assert_status 200 "$output" "local /login must be reachable"
}

@test "S-3 admin API token authenticates (GET /api/books -> 200)" {
  run api_status GET /api/books
  assert_status 200 "$output" "seeded-admin API token should authenticate"
}
