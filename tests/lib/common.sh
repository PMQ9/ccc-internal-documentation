#!/usr/bin/env bash
# Shared helpers for the CCC BookStack integration + stress suites.
# Sourced by tests/integration/run.sh and (via helpers/load.bash) by every .bats file.
#
# Contract — the runner (run.sh) exports these before invoking bats; everything
# downstream reads them from the environment:
#   BASE_URL      e.g. http://localhost:8089  (matches APP_URL the stack was started with)
#   PROJECT       docker compose project name (isolates test volumes from a real local stack)
#   COMPOSE_FILE  absolute path to deploy/local/compose.yaml
#   ENV_FILE      absolute path to the generated test .env
#   DB_PASSWORD   BookStack app DB password (for direct DB assertions)
#   ADMIN_TOKEN   "<token_id>:<secret>" for the seeded admin (minted via DB)
#   EDITOR_EMAIL / EDITOR_PASS / VIEWER_EMAIL / VIEWER_PASS  (session-login RBAC)
#   REPO_ROOT     absolute repo root
#
# All functions are side-effect-free w.r.t. shell options so they're safe under
# `set -euo pipefail` (a non-2xx HTTP code is data, not a fatal error).

# ---- docker compose, scoped to the isolated test project --------------------
dc() { docker compose -p "$PROJECT" -f "$COMPOSE_FILE" --env-file "$ENV_FILE" "$@"; }

# ---- DB query as the app user over TCP (root@localhost uses unix_socket; see local README §5)
dbq() {
  dc exec -T db sh -lc 'exec mariadb -h 127.0.0.1 -u "'"${DB_USERNAME:-bookstack}"'" -p"'"$DB_PASSWORD"'" "'"${DB_DATABASE:-bookstackapp}"'" -N -B -e "$0"' "$1"
}

# ---- HTTP helpers (status code only; body; with/without auth) ---------------
# Anonymous GET status (no redirects followed — we assert on the 302 itself).
http_status() { curl -s -o /dev/null -w '%{http_code}' "$BASE_URL$1"; }

# Status with a path under a method + optional token.
req_status() { # req_status TOKEN METHOD PATH [BODY]
  local token="$1" method="$2" path="$3" body="${4:-}"
  local args=(-s -o /dev/null -w '%{http_code}' -X "$method")
  [ -n "$token" ] && args+=(-H "Authorization: Token $token")
  [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
  curl "${args[@]}" "$BASE_URL$path"
}

req_body() { # req_body TOKEN METHOD PATH [BODY]
  local token="$1" method="$2" path="$3" body="${4:-}"
  local args=(-s -X "$method")
  [ -n "$token" ] && args+=(-H "Authorization: Token $token")
  [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
  curl "${args[@]}" "$BASE_URL$path"
}

# Admin-token convenience wrappers.
api()        { req_body   "$ADMIN_TOKEN" "$1" "$2" "${3:-}"; }
api_status() { req_status "$ADMIN_TOKEN" "$1" "$2" "${3:-}"; }

# Extract a top-level/path JSON value (jq is present on ubuntu-latest and macOS dev boxes).
json() { jq -r "$1"; }

# Pull the first numeric "id" out of a BookStack create response.
first_id() { grep -oE '"id":[0-9]+' | head -1 | cut -d: -f2; }

# ---- Readiness: poll, never sleep-and-pray --------------------------------
wait_for_http() { # wait_for_http PATH EXPECTED_CODE TIMEOUT_SECONDS
  local path="$1" want="${2:-200}" timeout="${3:-240}" code
  local deadline=$((SECONDS + timeout))
  while [ "$SECONDS" -lt "$deadline" ]; do
    code=$(http_status "$path" || echo 000)
    [ "$code" = "$want" ] && return 0
    sleep 3
  done
  echo "wait_for_http: $path never returned $want within ${timeout}s (last=$code)" >&2
  return 1
}

wait_for_db_healthy() { # wait_for_db_healthy TIMEOUT
  local timeout="${1:-120}" state
  local deadline=$((SECONDS + timeout))
  while [ "$SECONDS" -lt "$deadline" ]; do
    state=$(dc ps --format '{{.Health}}' db 2>/dev/null | head -1 || true)
    [ "$state" = "healthy" ] && return 0
    sleep 3
  done
  echo "wait_for_db_healthy: db not healthy within ${timeout}s (last=$state)" >&2
  return 1
}

# ---- Session login (CSRF dance) for true UI-surface RBAC checks -------------
# Logs in via the local /login form, persisting cookies to JAR. Echoes the POST
# status (302 == success). Use the JAR with `curl -b "$JAR"` afterwards.
login_session() { # login_session EMAIL PASSWORD JAR
  local email="$1" pass="$2" jar="$3" page token
  rm -f "$jar"
  page=$(curl -s -c "$jar" "$BASE_URL/login")
  token=$(printf '%s' "$page" | grep -oE 'name="_token"[^>]*value="[^"]+"' | head -1 | sed -E 's/.*value="([^"]+)".*/\1/')
  if [ -z "$token" ]; then echo "login_session: no CSRF _token on /login" >&2; return 1; fi
  curl -s -b "$jar" -c "$jar" -o /dev/null -w '%{http_code}' \
    --data-urlencode "email=$email" \
    --data-urlencode "password=$pass" \
    --data-urlencode "_token=$token" \
    "$BASE_URL/login"
}

# Status of an authenticated-session GET (follows no redirects).
session_status() { # session_status JAR PATH
  curl -s -b "$1" -o /dev/null -w '%{http_code}' "$BASE_URL$2"
}
