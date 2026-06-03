#!/usr/bin/env bats
# FN-001 — anonymous gating. With public viewing off (default), unauthenticated
# requests to read/edit/admin surfaces redirect to login; edit/admin are never
# served to anon. Also the security invariant: enabling public *read* must never
# open public *write* (T-002).
load "../helpers/load"

@test "T-001 anonymous GET / redirects to login (public viewing off)" {
  run http_status /
  assert_status 302 "$output" "anon home should 302 to /login when public viewing is off"
}

@test "T-001 anonymous GET /settings/users redirects to login" {
  run http_status /settings/users
  assert_status 302 "$output" "anon admin surface must 302, never render"
}

@test "T-001 anonymous GET /books/create redirects to login" {
  run http_status /books/create
  assert_status 302 "$output" "anon edit surface must 302, never render"
}

@test "T-002 public viewing: anon can READ, but still cannot WRITE or administer" {
  # Turn the 'app-public' setting on at the DB layer and clear BookStack's
  # settings cache so it takes effect. Then assert the full property: public
  # READ is allowed, but public WRITE/admin is NOT (the bug that matters is
  # public-read leaking write).
  dbq "INSERT INTO settings (setting_key, type, value, created_at, updated_at)
       VALUES ('app-public','string','true',NOW(),NOW())
       ON DUPLICATE KEY UPDATE value='true', updated_at=NOW();" || skip "settings schema differs on this image"
  dc exec -T bookstack sh -lc 'cd /app/www 2>/dev/null && php artisan cache:clear >/dev/null 2>&1 || true'

  run http_status /
  assert_status 200 "$output" "with public viewing ON, anonymous read of the home view is allowed"

  run http_status /create-book
  assert_status_in "302 403 404" "$output" "public viewing must NOT let anon reach a create surface (not 200)"

  run http_status /settings/users
  assert_status_in "302 403" "$output" "public viewing must NOT let anon reach admin user management (not 200)"

  # Restore default (public off) so later tests see the documented default posture.
  dbq "UPDATE settings SET value='false', updated_at=NOW() WHERE setting_key='app-public';" || true
  dc exec -T bookstack sh -lc 'cd /app/www 2>/dev/null && php artisan cache:clear >/dev/null 2>&1 || true'
}
