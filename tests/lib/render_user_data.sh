#!/usr/bin/env bash
# Render terraform/user-data.sh.tftpl to a runnable bash script so shellcheck can
# lint it. Substitutes the Terraform templatefile() ${...} interpolations with
# safe placeholders and converts the $${...} escape to a literal ${...} (exactly
# what Terraform does at apply time). Prints the rendered script to stdout.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TPL="${1:-$HERE/../../terraform/user-data.sh.tftpl}"
[ -f "$TPL" ] || { echo "template not found: $TPL" >&2; exit 1; }

perl -pe '
  s/\$\{region\}/us-east-1/g;
  s/\$\{name_prefix\}/ccc-wiki/g;
  s{\$\{efs_id\}}{fs-0123456789abcdef0}g;
  s{\$\{efs_ap_id\}}{fsap-0123456789abcdef0}g;
  s{\$\{app_key_arn\}}{arn:aws:secretsmanager:us-east-1:123456789012:secret:ccc-wiki/app-key}g;
  s{\$\{db_secret_arn\}}{arn:aws:secretsmanager:us-east-1:123456789012:secret:rds-mock}g;
  s{\$\{saml_idp_x509_arn\}}{arn:aws:secretsmanager:us-east-1:123456789012:secret:ccc-wiki/saml/idp_x509}g;
  s{\$\{timezone\}}{America/Chicago}g;
  s{\$\{alb_proxy_cidrs\}}{10.20.0.0/24,10.20.1.0/24}g;
  s{\$\{rds_address\}}{ccc-wiki-db.example.rds.amazonaws.com}g;
  s/\$\{db_name\}/bookstackapp/g;
  s/\$\{db_username\}/bookstack/g;
  s{\$\{bookstack_image\}}{lscr.io/linuxserver/bookstack:v26.05-ls265}g;
  s{\$\{log_group\}}{/ccc-wiki/bookstack}g;
  s{\$\{cw_namespace\}}{ccc-wiki/host}g;
  s/\$\$\{/\$\{/g;   # $${...} escape -> literal ${...}
' "$TPL"
