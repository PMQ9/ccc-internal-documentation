#!/usr/bin/env bats
# T-020 — contact-form service (issue #15). Stands up an ISOLATED contact + MailHog
# stack (own compose project + high host ports), then asserts end-to-end: a valid
# submission delivers a structured email (To = fixed recipient, Reply-To =
# submitter, structured Subject, X-CCC-Contact-Type header), and every guard
# (CSRF, honeypot, approved-domain, required fields) behaves. Torn down on exit;
# never touches the BookStack test stack. Skips cleanly when Docker is absent.
load "../helpers/load"

CONTACT_DIR="$BATS_TEST_DIRNAME/../contact"
CONTACT_PROJECT="ccc-wiki-contact-test"
CONTACT_PORT="${CONTACT_TEST_PORT:-18081}"
MAILHOG_PORT="${MAILHOG_HTTP_PORT:-18025}"
C_BASE="http://localhost:${CONTACT_PORT}"
MH_BASE="http://localhost:${MAILHOG_PORT}"

dcc() { docker compose -p "$CONTACT_PROJECT" -f "$CONTACT_DIR/compose.yaml" "$@"; }

setup_file() {
  command -v docker > /dev/null 2>&1 || return 0
  docker info > /dev/null 2>&1 || return 0
  dcc up -d --build > /dev/null 2>&1 || return 0
  for _ in $(seq 1 60); do
    curl -fsS -o /dev/null "$C_BASE/contact/healthz" 2> /dev/null && break
    sleep 2
  done
}

teardown_file() {
  command -v docker > /dev/null 2>&1 || return 0
  dcc down -v > /dev/null 2>&1 || true
}

setup() {
  curl -fsS -o /dev/null "$C_BASE/contact/healthz" 2> /dev/null \
    || skip "contact test stack not up (Docker unavailable?)"
}

get_token() { # get_token JARFILE -> echoes the CSRF token from a fresh form
  curl -s -c "$1" "$C_BASE/contact" \
    | grep -oE 'name="_csrf"[^>]*value="[^"]+"' | head -1 \
    | sed -E 's/.*value="([^"]+)".*/\1/'
}

mh_total() { curl -s "$MH_BASE/api/v2/messages" | jq -r '.total // 0'; }
mh_clear() { curl -s -X DELETE "$MH_BASE/api/v1/messages" > /dev/null 2>&1 || true; }
mh_hdr() { curl -s "$MH_BASE/api/v2/messages" | jq -r ".items[0].Content.Headers.\"$1\"[0] // empty"; }

@test "T-020 health + readiness" {
  run curl -s -o /dev/null -w '%{http_code}' "$C_BASE/contact/healthz"
  assert_status 200 "$output" "healthz should be 200"
  run curl -s -o /dev/null -w '%{http_code}' "$C_BASE/contact/readyz"
  assert_status 200 "$output" "readyz should be 200 (mail configured at the sink)"
}

@test "T-020 form renders with CSRF token + honeypot" {
  body="$(curl -s "$C_BASE/contact")"
  assert_contains "$body" 'name="_csrf"' "form must carry a CSRF field"
  assert_contains "$body" 'name="website"' "form must carry the honeypot field"
  assert_contains "$body" "Bug report" "form must list the submission types"
  assert_contains "$body" "Back to the wiki" "form must link back to the wiki (CONTACT_WIKI_URL)"
}

@test "T-020 theme cookie bridges the wiki's dark/light choice (issue #39)" {
  # The wiki sets a host-scoped ccc-color-scheme cookie; this cross-port service
  # renders the matching <html> class server-side so there's no light-then-dark
  # flash. Absence => no class (CSS follows the OS, matching wiki guest behavior).
  # The REVERSE direction (the contact toggle writing the cookie for the wiki to
  # read) is client-side JS — out of scope for this server-side suite, and verified
  # manually per the deploy/branding/README.md cross-port checklist item.
  dark="$(curl -s -b 'ccc-color-scheme=dark' "$C_BASE/contact")"
  assert_contains "$dark" '<html lang="en" class="dark-mode">' "dark cookie must render html.dark-mode"
  light="$(curl -s -b 'ccc-color-scheme=light' "$C_BASE/contact")"
  assert_contains "$light" '<html lang="en" class="ccc-light">' "light cookie must render html.ccc-light"
  none="$(curl -s "$C_BASE/contact")"
  assert_contains "$none" '<html lang="en">' "no cookie must render a bare <html> (follow OS)"
}

@test "T-020 valid submission delivers a structured email" {
  mh_clear
  jar="$(mktemp)"
  token="$(get_token "$jar")"
  [ -n "$token" ] || { rm -f "$jar"; flunk "no CSRF token rendered on the form"; }
  run curl -s -b "$jar" -o /dev/null -w '%{http_code}' \
    --data-urlencode "_csrf=$token" \
    --data-urlencode "type=bug" \
    --data-urlencode "name=Jane Doe" \
    --data-urlencode "email=jane@vanderbilt.edu" \
    --data-urlencode "summary=Login 500" \
    --data-urlencode "details=repro steps" \
    "$C_BASE/contact/submit"
  rm -f "$jar"
  assert_status 200 "$output" "valid submit should render the success page"

  delivered=""
  for _ in $(seq 1 15); do
    [ "$(mh_total)" = "1" ] && { delivered=1; break; }
    sleep 1
  done
  [ -n "$delivered" ] || flunk "expected exactly 1 captured email, got $(mh_total)"

  assert_equal "dest@example.org" "$(mh_hdr To)" "To must be the fixed recipient"
  assert_equal "jane@vanderbilt.edu" "$(mh_hdr Reply-To)" "Reply-To must be the submitter"
  assert_equal "bug" "$(mh_hdr X-CCC-Contact-Type)" "type header must be set"
  assert_contains "$(mh_hdr Subject)" "[CCC Wiki] Bug report - Login 500" "structured subject"
  assert_contains "$(mh_hdr From)" "cccwiki.contact@gmail.com" "From must be the configured sender"
}

@test "T-020 missing CSRF is rejected (403), nothing sent" {
  mh_clear
  run curl -s -o /dev/null -w '%{http_code}' \
    --data-urlencode "type=bug" --data-urlencode "name=J" \
    --data-urlencode "email=jane@vanderbilt.edu" --data-urlencode "summary=x" \
    "$C_BASE/contact/submit"
  assert_status 403 "$output" "missing CSRF must be 403"
  assert_equal "0" "$(mh_total)" "no mail should be sent"
}

@test "T-020 non-approved sender domain is rejected (400), nothing sent" {
  mh_clear
  jar="$(mktemp)"
  token="$(get_token "$jar")"
  run curl -s -b "$jar" -o /dev/null -w '%{http_code}' \
    --data-urlencode "_csrf=$token" --data-urlencode "type=bug" \
    --data-urlencode "name=Jane" --data-urlencode "email=jane@gmail.com" \
    --data-urlencode "summary=x" \
    "$C_BASE/contact/submit"
  rm -f "$jar"
  assert_status 400 "$output" "off-domain sender must be 400"
  assert_equal "0" "$(mh_total)" "no mail should be sent for a rejected sender"
}

@test "T-020 honeypot is silently dropped (200), nothing sent" {
  mh_clear
  jar="$(mktemp)"
  token="$(get_token "$jar")"
  run curl -s -b "$jar" -o /dev/null -w '%{http_code}' \
    --data-urlencode "_csrf=$token" --data-urlencode "type=bug" \
    --data-urlencode "name=Bot" --data-urlencode "email=bot@vanderbilt.edu" \
    --data-urlencode "summary=spam" --data-urlencode "website=http://spam.example" \
    "$C_BASE/contact/submit"
  rm -f "$jar"
  assert_status 200 "$output" "honeypot must return a silent 200"
  sleep 2
  assert_equal "0" "$(mh_total)" "honeypot submission must not be sent"
}

@test "T-020 missing required field is rejected (400)" {
  jar="$(mktemp)"
  token="$(get_token "$jar")"
  run curl -s -b "$jar" -o /dev/null -w '%{http_code}' \
    --data-urlencode "_csrf=$token" --data-urlencode "type=bug" \
    --data-urlencode "name=Jane" --data-urlencode "email=jane@vanderbilt.edu" \
    "$C_BASE/contact/submit"
  rm -f "$jar"
  assert_status 400 "$output" "missing summary must be 400"
}

@test "T-020 oversized body is rejected (413), nothing sent (issue #41)" {
  mh_clear
  jar="$(mktemp)"
  token="$(get_token "$jar")"
  big="$(head -c 70000 /dev/zero | tr '\0' A)" # > the 64 KiB body cap
  run curl -s -b "$jar" -o /dev/null -w '%{http_code}' \
    --data-urlencode "_csrf=$token" --data-urlencode "type=bug" \
    --data-urlencode "name=Jane" --data-urlencode "email=jane@vanderbilt.edu" \
    --data-urlencode "summary=big" --data-urlencode "details=$big" \
    "$C_BASE/contact/submit"
  rm -f "$jar"
  assert_status 413 "$output" "oversized body must be 413"
  sleep 1
  assert_equal "0" "$(mh_total)" "oversized submission must not be sent"
}

@test "T-020 responses carry defense-in-depth security headers (issue #41)" {
  run curl -s -D - -o /dev/null "$C_BASE/contact"
  assert_contains "$output" "Content-Security-Policy:" "CSP header must be set"
  assert_contains "$output" "X-Content-Type-Options: nosniff" "nosniff must be set"
  assert_contains "$output" "frame-ancestors 'none'" "CSP must block framing"
}

@test "T-020 a submission with @ and # still delivers (tracker safety is unit-tested)" {
  # The submitter can legitimately type @ and #; that must not be blocked. The
  # GitHub-issue Markdown neutralization (fencing) is covered by the Go unit tests
  # (the issue channel isn't wired in this MailHog-only stack).
  mh_clear
  jar="$(mktemp)"
  token="$(get_token "$jar")"
  run curl -s -b "$jar" -o /dev/null -w '%{http_code}' \
    --data-urlencode "_csrf=$token" --data-urlencode "type=feedback" \
    --data-urlencode "name=@everyone" --data-urlencode "email=jane@vanderbilt.edu" \
    --data-urlencode "summary=ping @team" --data-urlencode "details=see #1" \
    "$C_BASE/contact/submit"
  rm -f "$jar"
  assert_status 200 "$output" "a submission containing @ and # must still be accepted"
  delivered=""
  for _ in $(seq 1 15); do
    [ "$(mh_total)" = "1" ] && { delivered=1; break; }
    sleep 1
  done
  [ -n "$delivered" ] || flunk "expected the @mention submission to deliver one email"
}
