#!/usr/bin/env bash
# CFG-002 / SEC-008 (test plan TF-008, TF-016): assert the RENDERED user-data
# script fetches its secrets at boot and bakes none in. This inspects the real
# rendered output (stronger than a mocked terraform-test plan, where user_data
# is unknown). Exits non-zero on any contract violation.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
R="$("$HERE/render_user_data.sh")"
fail=0

need() { case "$R" in *"$1"*) echo "  ok: present  — $1" ;; *) echo "  MISSING     — $1"; fail=1 ;; esac; }
deny() { case "$R" in *"$1"*) echo "  FORBIDDEN   — $1"; fail=1 ;; *) echo "  ok: absent   — $1" ;; esac; }

echo "user-data contract:"
# Secrets are fetched at runtime from Secrets Manager…
need "secretsmanager get-secret-value"
# …and referenced as shell variables (NOT baked as literals).
need 'APP_KEY=$APP_KEY'
need 'DB_PASSWORD=$DB_PASS'
# Config contract.
need "DB_DATABASE=bookstackapp"
need "AUTH_AUTO_INITIATE=false" # break-glass: never auto-redirect to the IdP
need "docker compose up -d"
# No baked secret material: the rendered script must not contain a literal
# Laravel app key or a populated SAML placeholder.
deny "APP_KEY=base64:"
deny "PLACEHOLDER-set-during-VUIT-coordination"

if [ "$fail" -ne 0 ]; then
  echo "user-data contract FAILED"
  exit 1
fi
echo "user-data contract OK"
