#!/usr/bin/env bash
# Loaded by every .bats file via `load helpers/load`. Wires the shared library
# and a tiny set of assertion helpers (we deliberately avoid bats-support/assert
# submodules — these few helpers keep the suite dependency-free).

# Resolve the repo's tests/ dir regardless of where bats is invoked from.
_THIS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)" # -> tests/
# shellcheck source=/dev/null
source "$_THIS_DIR/lib/common.sh"

# Fail with a message (prints to bats' diagnostic stream).
flunk() {
  { echo "$@"; } >&2
  return 1
}

assert_equal() { # assert_equal EXPECTED ACTUAL [MSG]
  if [ "$1" != "$2" ]; then
    flunk "${3:-assertion failed}: expected '$1' but got '$2'"
  fi
}

assert_status() { # assert_status EXPECTED ACTUAL [MSG]  (HTTP-code-friendly alias)
  assert_equal "$1" "$2" "${3:-unexpected HTTP status}"
}

# Assert the actual status is one of a space-separated set (e.g. "401 403").
assert_status_in() { # assert_status_in "CODE CODE..." ACTUAL [MSG]
  local want code="$2"
  for want in $1; do [ "$code" = "$want" ] && return 0; done
  flunk "${3:-unexpected HTTP status}: '$code' not in [$1]"
}

assert_contains() { # assert_contains HAYSTACK NEEDLE [MSG]
  case "$1" in
    *"$2"*) return 0 ;;
    *) flunk "${3:-substring not found}: '$2' not in output" ;;
  esac
}

refute_contains() { # refute_contains HAYSTACK NEEDLE [MSG]
  case "$1" in
    *"$2"*) flunk "${3:-forbidden substring present}: '$2' found in output" ;;
    *) return 0 ;;
  esac
}

assert_ge() { # assert_ge ACTUAL MIN [MSG]  (numeric >=)
  if [ "$(( $1 < $2 ? 1 : 0 ))" -eq 1 ]; then
    flunk "${3:-value too small}: $1 < $2"
  fi
}
