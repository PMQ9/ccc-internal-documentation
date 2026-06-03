#!/usr/bin/env bats
# FN-002 — RBAC enforced at the true UI/session surface (the documented V3/V4/V5
# checks). Editor users and Viewer users are provisioned by run.sh via the admin
# API before this file runs; the seeded admin is admin@admin.com/password on a
# fresh install.
#
# Property under test: Viewer < Editor < Admin, and the boundaries hold —
#   - Admin reaches /settings.
#   - Editor can reach the create surface but NOT /settings.
#   - Viewer can read but cannot reach create or settings.
load "../helpers/load"

# NB: v26.05 routes — the create-book form is /create-book (not /books/create),
# and bare /settings 302-redirects to a default tab, so admin checks hit
# /settings/users (user management) for a definitive 200.

@test "T-005 admin can log in and reach user management" {
  run login_session "$ADMIN_EMAIL" "$ADMIN_PASS" "$BATS_TEST_TMPDIR/admin.jar"
  assert_status 302 "$output" "admin login should succeed (302 redirect)"
  run session_status "$BATS_TEST_TMPDIR/admin.jar" /settings/users
  assert_status 200 "$output" "admin must reach user management"
}

@test "T-003 editor can reach the create surface" {
  run login_session "$EDITOR_EMAIL" "$EDITOR_PASS" "$BATS_TEST_TMPDIR/editor.jar"
  assert_status 302 "$output" "editor login should succeed"
  run session_status "$BATS_TEST_TMPDIR/editor.jar" /create-book
  assert_status 200 "$output" "editor must be able to open the create-book form"
}

@test "T-003 editor is denied user management (cannot administer)" {
  login_session "$EDITOR_EMAIL" "$EDITOR_PASS" "$BATS_TEST_TMPDIR/editor.jar" >/dev/null
  run session_status "$BATS_TEST_TMPDIR/editor.jar" /settings/users
  assert_status_in "403 302" "$output" "editor must NOT be served admin user management (not 200)"
}

@test "T-004 viewer can read content" {
  run login_session "$VIEWER_EMAIL" "$VIEWER_PASS" "$BATS_TEST_TMPDIR/viewer.jar"
  assert_status 302 "$output" "viewer login should succeed"
  run session_status "$BATS_TEST_TMPDIR/viewer.jar" /
  assert_status 200 "$output" "viewer must be able to read the home/shelf view"
}

@test "T-004 viewer is denied the create surface (view-only)" {
  login_session "$VIEWER_EMAIL" "$VIEWER_PASS" "$BATS_TEST_TMPDIR/viewer.jar" >/dev/null
  run session_status "$BATS_TEST_TMPDIR/viewer.jar" /create-book
  assert_status_in "403 302" "$output" "viewer must NOT reach an edit surface (not 200)"
}

@test "T-004 viewer is denied user management" {
  login_session "$VIEWER_EMAIL" "$VIEWER_PASS" "$BATS_TEST_TMPDIR/viewer.jar" >/dev/null
  run session_status "$BATS_TEST_TMPDIR/viewer.jar" /settings/users
  assert_status_in "403 302" "$output" "viewer must NOT reach admin user management (not 200)"
}
