#!/usr/bin/env bash
# Fitness function for the cross-origin theme bridge (issue #39). The dark/light
# choice rides a host-scoped `ccc-color-scheme` cookie WRITTEN by the wiki head
# (deploy/branding/ccc-custom-head.html) and READ by the contact service
# (services/contact/templates/layout.html). The two copies legitimately differ in
# how they inject/adopt a toggle button, but the CONTRACT that makes the bridge
# work — the cookie name + write semantics, the resolver regex, and the toggle
# icons/labels — must stay byte-identical across both, or a toggle on one origin
# silently stops reflecting on the other. This asserts that shared contract so the
# "re-verify on upgrade" prose comment can't quietly rot. (issue #43)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
WIKI="$ROOT/deploy/branding/ccc-custom-head.html"
CONTACT="$ROOT/services/contact/templates/layout.html"

fail=0
note() { echo "  ok: $1"; }
bad() {
  echo "  DRIFT: $1"
  fail=1
}

echo "theme-bridge contract check:"

for f in "$WIKI" "$CONTACT"; do
  [ -f "$f" ] || bad "missing file: $f"
done

# Each entry is part of the shared bridge contract and must appear VERBATIM in BOTH
# files. Extend this list whenever you add a shared bit of the bridge.
contract=(
  'var KEY = "ccc-color-scheme";'
  'document.cookie.match(/(?:^|;\s*)ccc-color-scheme=(dark|light)(?:;|$)/)'
  'document.cookie = KEY + "=" + v + "; path=/; max-age=31536000; SameSite=Lax" + secure;'
  'var LABEL = { toDark: "Dark Mode", toLight: "Light Mode" };'
  'M10 2c-1.82 0-3.53.5-5 1.35' # ICON_DARK (moon) path data
  'M20 15.31 23.31 12 20 8.69V4h-4.69' # ICON_LIGHT (sun) path data
)

for s in "${contract[@]}"; do
  if grep -qF -- "$s" "$WIKI" && grep -qF -- "$s" "$CONTACT"; then
    note "shared: ${s:0:48}"
  else
    bad "not identical in both copies: $s"
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "theme-bridge check FAILED — re-sync the shared cookie/icon/label contract between"
  echo "  deploy/branding/ccc-custom-head.html and services/contact/templates/layout.html."
  exit 1
fi
echo "theme-bridge contract OK"
