#!/usr/bin/env bash
# One-command remote deploy: push THIS laptop's working tree to connor-server and
# (re)launch the BookStack validation stack there, reusing the dev-up.sh seam.
# Wired to the VSCode "Deploy to connor-server (remote)" task/launch button and `make deploy`.
#
# Safe by construction: the server's live deploy/local/.env (real APP_KEY + DB creds) is
# NEVER shipped or deleted — rsync excludes *.env, and --delete protects excluded files.
# Each run snapshots the DB+media first (a rollback point) and runs verify.sh after.
#
#   ./deploy-remote.sh              # snapshot → rsync → PULL=1 NO_OPEN=1 dev-up.sh → verify.sh
#   SKIP_SNAPSHOT=1 ./deploy-remote.sh   # skip the pre-deploy DB+media backup
#   SKIP_VERIFY=1   ./deploy-remote.sh   # skip the post-deploy verify.sh smoke test
#   NO_PULL=1       ./deploy-remote.sh   # don't `docker compose pull` (faster; no pin bump)
#   REMOTE_HOST=other REMOTE_DIR=path ./deploy-remote.sh   # target a different host/dir
set -euo pipefail

cd "$(dirname "$0")"            # deploy/local on the laptop
REPO_ROOT="$(cd ../.. && pwd)"  # rsync mirrors the FULL working tree

REMOTE_HOST="${REMOTE_HOST:-connor-server}"
REMOTE_DIR="${REMOTE_DIR:-ccc-wiki}"   # relative to the remote $HOME (tilde expands remotely)
BACKUP_DIR="ccc-wiki-backups"          # remote, under $HOME — matches the prior drill's naming
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=10)

# --- 1. preflight: SSH reachable (non-interactive) + docker present -----------
echo "==> preflight: ssh ${REMOTE_HOST} (docker compose present?)"
if ! ssh "${SSH_OPTS[@]}" "$REMOTE_HOST" 'docker compose version >/dev/null 2>&1'; then
  echo "!! cannot reach ${REMOTE_HOST} over ssh (BatchMode), or docker compose is missing" >&2
  exit 1
fi

# --- 2. rsync the working tree (excludes protect the server .env + secrets) ---
# rsync only updates files — the running stack, the named volume, and the DB are untouched
# until step 4 — so snapshotting AFTER this (using the freshly-synced snapshot.sh) still
# captures the pre-bring-up state. --delete makes the server mirror the laptop; excluded files
# (.env, backups, state) are deletion-protected. The *.env.example include precedes the *.env
# exclude (first-match-wins) so the example ships while a real .env never does (mirrors .gitignore).
echo "==> rsync working tree → ${REMOTE_HOST}:~/${REMOTE_DIR}"
rsync -az --delete --human-readable \
  --include='*.env.example' --exclude='.env' --exclude='*.env' \
  --exclude='.git/' --exclude='.claude/' \
  --exclude='backup-*' --exclude='*.sql' --exclude='*.tgz' --exclude='*.tar.gz' \
  --exclude='.terraform/' --exclude='*.tfstate' --exclude='*.tfstate.*' \
  --exclude='_user_data.rendered.sh' --exclude='.DS_Store' \
  "$REPO_ROOT"/ "${REMOTE_HOST}:${REMOTE_DIR}/"

# --- 3. pre-deploy DB + media snapshot (rollback point) -----------------------
# Reuse the synced snapshot.sh (script from a file, not stdin — no heredoc to be eaten by
# `docker compose exec -T`). BACKUP_DIR is relative to the remote $HOME.
if [ "${SKIP_SNAPSHOT:-0}" != "1" ]; then
  echo "==> snapshot remote DB + media into ~/${BACKUP_DIR}"
  ssh "${SSH_OPTS[@]}" "$REMOTE_HOST" \
    "cd ~/${REMOTE_DIR}/deploy/local && BACKUP_DIR=~/${BACKUP_DIR} bash snapshot.sh"
fi

# --- 4. bring the stack up on the server, REUSING dev-up.sh -------------------
pull_val=1
[ "${NO_PULL:-0}" = "1" ] && pull_val=0
echo "==> remote bring-up (PULL=${pull_val} NO_OPEN=1 ./dev-up.sh)"
ssh "${SSH_OPTS[@]}" "$REMOTE_HOST" \
  "cd ~/${REMOTE_DIR}/deploy/local && PULL=${pull_val} NO_OPEN=1 ./dev-up.sh"

# --- 5. post-deploy smoke test ------------------------------------------------
if [ "${SKIP_VERIFY:-0}" != "1" ]; then
  echo "==> remote verify.sh"
  # verify.sh is committed 644 (README runs it as `bash verify.sh`) — invoke via bash, not the +x bit.
  ssh "${SSH_OPTS[@]}" "$REMOTE_HOST" "cd ~/${REMOTE_DIR}/deploy/local && bash verify.sh"
fi

# --- 6. report ----------------------------------------------------------------
url=$(ssh "${SSH_OPTS[@]}" "$REMOTE_HOST" "grep '^APP_URL=' ~/${REMOTE_DIR}/deploy/local/.env | cut -d= -f2-")
echo "==> deployed to ${REMOTE_HOST}: ${url}"
ssh "${SSH_OPTS[@]}" "$REMOTE_HOST" 'docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"'
