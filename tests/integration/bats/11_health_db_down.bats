#!/usr/bin/env bats
# T-016 / OPS-002 / PERF-001 — health endpoint stays 200 with the DB down, and
# the app recovers when the DB comes back. Runs only on `pr` and `full` profiles.
load "../helpers/load"

@test "T-016 health endpoint (/icon.png) is 200 in normal operation" {
  run http_status /icon.png
  assert_status 200 "$output" "/icon.png must return 200 under normal operation"
}

@test "T-016 /icon.png is 200 while the DB is DOWN (DB-free health check)" {
  dc stop db >/dev/null 2>&1
  local code
  code=$(http_status /icon.png)
  dc start db >/dev/null 2>&1
  # Wait for the DB to recover so later tests aren't affected.
  wait_for_db_healthy 120 || echo "  WARN: db slow to return healthy"
  wait_for_http /login 200 180 || echo "  WARN: app slow to recover after db restart"
  if [ "$code" != "200" ]; then
    flunk "health endpoint returned $code with DB down (expected 200 — RDS failover would churn the ASG)"
  fi
}

@test "T-016 /login is 200 after DB comes back (app recovers)" {
  run http_status /login
  assert_status 200 "$output" "login must be reachable after DB restart"
}

@test "T-016 API is DB-ready after DB restart" {
  run api_status GET /api/books
  assert_status 200 "$output" "API must be DB-ready after DB restart"
}
