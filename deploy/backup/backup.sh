#!/bin/sh
# One backup run: a PostgreSQL custom-format dump and a tar.gz of the private
# file volume (driver documents, vehicle photos, receipts), each with a
# SHA-256 checksum. Old backups are pruned only after this run succeeded, so a
# failing backup never deletes the last good copy.
#
# Runs inside the db-backup service (postgres client image):
#   docker compose run --rm --entrypoint /bin/sh db-backup /opt/jantaxi/backup/backup.sh
#
# Environment: PGHOST PGPORT PGUSER PGPASSWORD PGDATABASE (libpq),
#   BACKUP_DIR (default /backups), FILES_DIR (default /data/files),
#   BACKUP_RETENTION_DAYS (default 14), BACKUP_OWNER (optional uid:gid that
#   owns the written files, e.g. the host user that copies them off-site).
set -eu
umask 077

BACKUP_DIR=${BACKUP_DIR:-/backups}
FILES_DIR=${FILES_DIR:-/data/files}
BACKUP_RETENTION_DAYS=${BACKUP_RETENTION_DAYS:-14}
BACKUP_OWNER=${BACKUP_OWNER:-}
: "${PGDATABASE:?PGDATABASE is required}"

log() { printf '%s backup: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
fail() { log "ERROR: $*"; exit 1; }

case "$BACKUP_RETENTION_DAYS" in
	'' | *[!0-9]*) fail "BACKUP_RETENTION_DAYS must be a whole number of days" ;;
esac
[ "$BACKUP_RETENTION_DAYS" -ge 1 ] || fail "BACKUP_RETENTION_DAYS must be at least 1"
if [ -n "$BACKUP_OWNER" ] && ! echo "$BACKUP_OWNER" | grep -Eq '^[0-9]+:[0-9]+$'; then
	fail "BACKUP_OWNER must be a numeric uid:gid such as 1000:1000"
fi

# Gives the listed paths to BACKUP_OWNER, if set.
own() {
	[ -z "$BACKUP_OWNER" ] || chown "$BACKUP_OWNER" "$@"
}

ts=$(date -u +%Y%m%dT%H%M%SZ)
db_dir="$BACKUP_DIR/db"
files_dir="$BACKUP_DIR/files"
mkdir -p "$db_dir" "$files_dir"
own "$BACKUP_DIR" "$db_dir" "$files_dir"

# 1. Database. Written to a .partial file and verified before it gets its
#    final name, so a crash never leaves a truncated file that looks valid.
dump_name="$PGDATABASE-$ts.dump"
dump="$db_dir/$dump_name"
log "dumping database $PGDATABASE from $PGHOST"
pg_dump --format=custom --compress=6 --file="$dump.partial"
pg_restore --list "$dump.partial" >/dev/null || fail "dump is not a readable archive"
mv "$dump.partial" "$dump"
(cd "$db_dir" && sha256sum "$dump_name" >"$dump_name.sha256")
own "$dump" "$dump.sha256"
log "database dump $dump ($(du -h "$dump" | cut -f1))"

# 2. Private files.
archive_name="files-$ts.tar.gz"
archive="$files_dir/$archive_name"
if [ -d "$FILES_DIR" ]; then
	tar -C "$(dirname "$FILES_DIR")" -czf "$archive.partial" "$(basename "$FILES_DIR")"
	tar -tzf "$archive.partial" >/dev/null || fail "files archive is not readable"
	mv "$archive.partial" "$archive"
	(cd "$files_dir" && sha256sum "$archive_name" >"$archive_name.sha256")
	own "$archive" "$archive.sha256"
	log "files archive $archive ($(du -h "$archive" | cut -f1))"
else
	fail "files directory $FILES_DIR is missing"
fi

# 3. Retention (only reached when both parts succeeded): delete backups older
#    than BACKUP_RETENTION_DAYS x 24 hours, and stale partial files.
max_age_min=$((BACKUP_RETENTION_DAYS * 1440))
find "$db_dir" -type f \( -name "$PGDATABASE-*.dump" -o -name "$PGDATABASE-*.dump.sha256" \) \
	-mmin +"$max_age_min" -print -delete | sed 's/^/pruned /'
find "$files_dir" -type f \( -name 'files-*.tar.gz' -o -name 'files-*.tar.gz.sha256' \) \
	-mmin +"$max_age_min" -print -delete | sed 's/^/pruned /'
find "$db_dir" "$files_dir" -type f -name '*.partial' -mmin +1440 -print -delete | sed 's/^/pruned /'

log "done (retention $BACKUP_RETENTION_DAYS days)"
