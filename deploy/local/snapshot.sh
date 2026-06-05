#!/usr/bin/env bash
# Pre-deploy rollback point: dump the DB + archive media TOGETHER (referential integrity
# spans both). Runs ON the server in deploy/local; called by deploy-remote.sh and deploy.yml
# before a bring-up. Encodes the backup half of the drill in deploy/local/README.md §5.
#
#   bash snapshot.sh                 # → ~/ccc-wiki-backups/backup-{db,media}-<ts>.{sql,tgz}
#   BACKUP_DIR=/path bash snapshot.sh
set -euo pipefail

cd "$(dirname "$0")"
BACKUP_DIR="${BACKUP_DIR:-$HOME/ccc-wiki-backups}"
# Volume name = compose `name: ccc-wiki` + the volume key (independent of the dir path).
VOLUME="${VOLUME:-ccc-wiki_bookstack_config}"

mkdir -p "$BACKUP_DIR"
ts=$(date +%Y%m%d-%H%M%S)

# root@localhost is unix_socket auth in the LinuxServer image → dump as the app user over TCP.
# </dev/null so `docker compose exec -T` never reads the caller's stdin (it would otherwise eat
# a heredoc-supplied script). Read DB_PASSWORD from the live .env beside this script.
appw=$(grep '^DB_PASSWORD=' .env | cut -d= -f2-)
docker compose exec -T db sh -c \
  "mariadb-dump -h 127.0.0.1 -u bookstack -p\"$appw\" --single-transaction --add-drop-table bookstackapp" \
  < /dev/null > "$BACKUP_DIR/backup-db-$ts.sql"

# Media (attachments + images) live on the persistent volume under www/files + www/uploads/images.
docker run --rm -v "$VOLUME":/data -v "$BACKUP_DIR":/out alpine \
  tar czf "/out/backup-media-$ts.tgz" -C /data www/files www/uploads/images

echo "snapshot: $BACKUP_DIR/backup-db-$ts.sql + backup-media-$ts.tgz"
