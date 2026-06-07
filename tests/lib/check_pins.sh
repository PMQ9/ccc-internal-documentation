#!/usr/bin/env bash
# Fitness function for version pins — the single source of truth for two promises this repo makes:
# "nothing floats in committed prod config" and "the versions you run via `make` are the versions
# CI runs". Invoked by BOTH `make pins` and the CI lint job, so the rule lives in exactly one place
# (mirrors tests/lib/check_user_data_contract.sh). Asserts four properties, exits non-zero on any:
#   1. No floating ':latest' in committed prod config (image tags etc.).
#   2. No floating 'releases/latest' download in the EC2 bootstrap.
#   3. Every Makefile tool/scanner pin (*_IMG) is mirrored in the workflow(s) that use it.
#   4. The shellcheck render fixture's BookStack image matches the Terraform default.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
MK="$ROOT/Makefile"
CI="$ROOT/.github/workflows/ci.yml"
WEEKLY="$ROOT/.github/workflows/weekly.yml"
TFPLAN="$ROOT/.github/workflows/terraform-plan.yml"
RENDER="$ROOT/tests/lib/render_user_data.sh"
VARS="$ROOT/terraform/variables.tf"
TFTPL="$ROOT/terraform/user-data.sh.tftpl"

fail=0
note() { echo "  ok: $1"; }
bad() {
  echo "  DRIFT: $1"
  fail=1
}

echo "pin check:"

# 1. No floating ':latest' in committed prod config (comment lines excluded; the documented
#    appkey-gen command in a comment legitimately uses :latest).
m="$(grep -hE ':latest' "$ROOT/deploy/local/.env.example" "$VARS" "$ROOT/services/contact/Dockerfile" | grep -vE '^[[:space:]]*#' || true)"
if [ -n "$m" ]; then bad "floating :latest in committed config: $m"; else note "no :latest in .env.example/variables.tf/contact Dockerfile"; fi

# 2. No floating 'releases/latest' in the EC2 bootstrap — pin docker_compose_version instead.
#    Comment lines excluded (the rationale comment legitimately names the pattern it forbids).
l="$(grep -nE 'releases/latest' "$TFTPL" | grep -vE '^[0-9]+:[[:space:]]*#' || true)"
if [ -n "$l" ]; then bad "floating releases/latest in user-data: $l"; else note "no releases/latest in user-data bootstrap"; fi

# 3. Makefile tool pins mirrored in the workflows. The Makefile is canonical (the documented
#    entrypoint); CI keeps inline literals for readable annotations, this gate forbids divergence.
mk_img() { grep -E "^$1[[:space:]]*:?=" "$MK" | head -1 | sed -E 's/^[^=]*=[[:space:]]*//'; }
have() { # have LABEL IMAGE_OR_VERSION FILE
  if [ -z "$2" ]; then bad "$1 pin missing from Makefile"; return; fi
  if grep -qF -- "$2" "$3"; then note "$1 pin $2 mirrored in ${3##*/}"; else bad "$1 pin $2 NOT in ${3##*/}"; fi
}

# Six tools pinned as a full image:tag in both the Makefile and the workflows.
have trivy "$(mk_img TRIVY_IMG)" "$CI"
have trivy "$(mk_img TRIVY_IMG)" "$WEEKLY"
have checkov "$(mk_img CHECKOV_IMG)" "$CI"
have shellcheck "$(mk_img SHELLCHECK_IMG)" "$CI"
have actionlint "$(mk_img ACTIONLINT_IMG)" "$CI"
have gitleaks "$(mk_img GITLEAKS_IMG)" "$CI"
have lychee "$(mk_img LYCHEE_IMG)" "$CI"
have lychee "$(mk_img LYCHEE_IMG)" "$WEEKLY"

# terraform + tflint install via setup-* actions with a BARE version token (not image:tag), so
# compare the version, not the whole image string. (terraform is triplicated: Makefile, ci, tf-plan.)
tf_img="$(mk_img TF_IMG)"
tflint_img="$(mk_img TFLINT_IMG)"
have terraform "${tf_img##*:}" "$CI"
have terraform "${tf_img##*:}" "$TFPLAN"
have tflint "${tflint_img##*:}" "$CI"

# 4. The shellcheck render fixture hardcodes the prod BookStack image to mirror variables.tf; if one
#    is bumped without the other, shellcheck lints a stale render and no other gate notices.
var_img="$(grep -oE 'lscr\.io/linuxserver/bookstack:[^"]+' "$VARS" | head -1)"
if [ -n "$var_img" ] && grep -qF -- "$var_img" "$RENDER"; then
  note "bookstack image $var_img consistent (variables.tf == render_user_data.sh)"
else
  bad "render_user_data.sh image != variables.tf default ($var_img)"
fi

if [ "$fail" -ne 0 ]; then
  echo "pin check FAILED — align the divergent pin so committed config never floats and local == CI."
  exit 1
fi
echo "pin check OK"
