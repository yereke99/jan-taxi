#!/bin/sh
# Non-destructive restore. Nothing that is live is overwritten:
#   *.dump     is restored into a NEW database (default <db>_restore_<timestamp>)
#   *.tar.gz   (files archive) is unpacked into BACKUP_DIR/restore/<name>/
# The script then prints the exact steps to switch over. It asks for an
# explicit confirmation; for scripted restore drills set RESTORE_CONFIRM to
# the target database name (or to "unpack" for a files archive).
#
#   make restore FILE=backups/db/jantaxi-20261016T223000Z.dump
#   docker compose run --rm --entrypoint /bin/sh db-backup \
#       /opt/jantaxi/backup/restore.sh /backups/db/jantaxi-20261016T223000Z.dump
set -eu

BACKUP_DIR=${BACKUP_DIR:-/backups}
# The same directory as seen from the host (for the printed instructions).
HOST_BACKUP_DIR=${BACKUP_HOST_DIR:-./backups}
HOST_BACKUP_DIR=${HOST_BACKUP_DIR%/}
: "${PGDATABASE:?PGDATABASE is required}"
: "${PGUSER:?PGUSER is required}"

log() { printf '%s restore: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
fail() { log "ERROR: $*"; exit 1; }

[ $# -eq 1 ] || fail "usage: restore.sh <file.dump | files-*.tar.gz>"
src=$1
[ -f "$src" ] || fail "no such file: $src"

verify_checksum() {
	sum="$src.sha256"
	if [ -f "$sum" ]; then
		(cd "$(dirname "$src")" && sha256sum -c "$(basename "$sum")" >/dev/null) ||
			fail "checksum mismatch for $src"
		log "checksum OK"
	else
		log "WARNING: no checksum file next to $src; integrity not verified"
	fi
}

confirm() {
	expected=$1
	prompt=$2
	if [ -n "${RESTORE_CONFIRM:-}" ]; then
		[ "$RESTORE_CONFIRM" = "$expected" ] || fail "RESTORE_CONFIRM does not match '$expected'"
		return 0
	fi
	[ -t 0 ] || fail "confirmation needed: run interactively or set RESTORE_CONFIRM=$expected"
	printf '%s\nType %s to continue: ' "$prompt" "$expected"
	read -r answer
	[ "$answer" = "$expected" ] || fail "not confirmed; nothing was changed"
}

psql_admin() { psql -X -v ON_ERROR_STOP=1 -d postgres "$@"; }

restore_database() {
	stamp=$(date -u +%Y%m%d%H%M%S)
	target=${RESTORE_DB:-${PGDATABASE}_restore_$stamp}
	echo "$target" | grep -Eq '^[a-z_][a-z0-9_]{0,62}$' ||
		fail "invalid target database name '$target' (lowercase letters, digits, underscores)"
	[ "$target" != "$PGDATABASE" ] || fail "refusing to restore over the live database $PGDATABASE"
	exists=$(psql_admin -Atc "SELECT 1 FROM pg_database WHERE datname = '$target'")
	[ -z "$exists" ] || fail "database $target already exists; choose another RESTORE_DB"

	pg_restore --list "$src" >/dev/null || fail "$src is not a pg_dump custom-format archive"
	verify_checksum
	confirm "$target" "Restore $(basename "$src") into the NEW database '$target' on $PGHOST? The live database '$PGDATABASE' is not touched."

	log "creating database $target"
	createdb --maintenance-db=postgres --template=template0 --encoding=UTF8 "$target"
	log "restoring (this can take a while)"
	pg_restore --exit-on-error --no-owner --role="$PGUSER" --dbname="$target" "$src"

	log "restored database summary:"
	psql -X -v ON_ERROR_STOP=1 -d "$target" -P pager=off <<'SQL'
SELECT max(version_id) AS schema_version FROM goose_db_version WHERE is_applied;
SELECT 'users' AS table_name, count(*) AS rows FROM users
UNION ALL SELECT 'driver_profiles', count(*) FROM driver_profiles
UNION ALL SELECT 'orders', count(*) FROM orders
UNION ALL SELECT 'wallet_ledger', count(*) FROM wallet_ledger
UNION ALL SELECT 'top_up_requests', count(*) FROM top_up_requests
UNION ALL SELECT 'files', count(*) FROM files;
-- Wallets whose balance/reserved differ from the sum of their ledger rows (expect 0).
SELECT count(*) AS wallet_ledger_mismatches
FROM wallets w
LEFT JOIN (
    SELECT wallet_id, sum(balance_delta) AS b, sum(reserved_delta) AS r
    FROM wallet_ledger GROUP BY wallet_id
) l ON l.wallet_id = w.id
WHERE w.balance <> coalesce(l.b, 0) OR w.reserved <> coalesce(l.r, 0);
SELECT max(created_at) AS newest_order FROM orders;
SQL

	cat <<EOF

Restore finished. The live database '$PGDATABASE' is unchanged.
To switch the application to the restored copy (short downtime):

  1. Stop everything that writes to the database:
       docker compose stop passenger-bot driver-bot api db-backup
  2. Swap the databases (keeps the current one as ${PGDATABASE}_before_$stamp):
       docker compose exec db psql -U $PGUSER -d postgres \\
         -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '$PGDATABASE' AND pid <> pg_backend_pid();" \\
         -c "ALTER DATABASE $PGDATABASE RENAME TO ${PGDATABASE}_before_$stamp;" \\
         -c "ALTER DATABASE $target RENAME TO $PGDATABASE;"
  3. Restore the matching files archive too (make restore FILE=$HOST_BACKUP_DIR/files/files-<same time>.tar.gz).
  4. Start the stack again:
       make up && make health
  5. After verifying, drop the old copy:
       docker compose exec db psql -U $PGUSER -d postgres -c "DROP DATABASE ${PGDATABASE}_before_$stamp;"

For a restore drill only, drop the copy when done:
  docker compose exec db psql -U $PGUSER -d postgres -c "DROP DATABASE $target;"
EOF
}

restore_files() {
	name=$(basename "$src" .tar.gz)
	staging="$BACKUP_DIR/restore/$name"
	[ ! -e "$staging" ] || fail "$staging already exists; remove it first"
	tar -tzf "$src" >/dev/null || fail "$src is not a readable tar.gz archive"
	verify_checksum
	confirm unpack "Unpack $(basename "$src") into the staging directory $staging? The live file volume is not touched."

	umask 077
	mkdir -p "$staging"
	tar -xzf "$src" -C "$staging"
	count=$(find "$staging" -type f | wc -l | tr -d ' ')
	log "unpacked $count files into $staging"
	# docker run -v needs an absolute host path.
	case "$HOST_BACKUP_DIR" in
		/*) host_staging="$HOST_BACKUP_DIR/restore/$name" ;;
		*) host_staging="\$PWD/${HOST_BACKUP_DIR#./}/restore/$name" ;;
	esac

	cat <<EOF

Files unpacked to $staging (host: $HOST_BACKUP_DIR/restore/$name).
Stored files have unique random names, so copying them back only adds missing
files and never removes newer ones. To copy them into the live volume:

  docker compose stop api
  docker run --rm \\
    -v "\${COMPOSE_PROJECT_NAME:-jantaxi}_files:/data/files" \\
    -v "$host_staging/files:/restore:ro" \\
    alpine:3.22 sh -c 'cp -a /restore/. /data/files/ && chown -R 65532:65532 /data/files && chmod -R go-rwx /data/files'
  docker compose up -d api && make health

Delete the staging copy afterwards (it contains personal data):
  rm -rf $HOST_BACKUP_DIR/restore/$name
EOF
}

case "$src" in
	*.dump) restore_database ;;
	*.tar.gz) restore_files ;;
	*) fail "unsupported file type: $src (expected .dump or .tar.gz)" ;;
esac
