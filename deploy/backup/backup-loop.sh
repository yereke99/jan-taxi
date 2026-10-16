#!/bin/sh
# Entry point of the db-backup service: runs backup.sh once a day at
# BACKUP_TIME (HH:MM in the container time zone, TZ). If no database dump is
# younger than 24 hours when the service starts (first deploy, long outage),
# a backup runs immediately. A failed run is retried after
# BACKUP_RETRY_MINUTES (default 60) instead of waiting for the next day.
set -eu

BACKUP_TIME=${BACKUP_TIME:-03:30}
BACKUP_DIR=${BACKUP_DIR:-/backups}
BACKUP_RETRY_MINUTES=${BACKUP_RETRY_MINUTES:-60}
here=$(dirname "$0")
last_failed=0

log() { printf '%s backup-loop: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

case "$BACKUP_TIME" in
	[01][0-9]:[0-5][0-9] | 2[0-3]:[0-5][0-9]) ;;
	*)
		log "invalid BACKUP_TIME '$BACKUP_TIME' (expected HH:MM)"
		exit 2
		;;
esac
case "$BACKUP_RETRY_MINUTES" in
	'' | *[!0-9]* | 0)
		log "invalid BACKUP_RETRY_MINUTES '$BACKUP_RETRY_MINUTES' (expected a positive number)"
		exit 2
		;;
esac

trap 'log "stopping"; exit 0' TERM INT

run_backup() {
	if sh "$here/backup.sh"; then
		last_failed=0
		log "backup succeeded"
	else
		status=$?
		last_failed=1
		log "BACKUP FAILED (exit $status); the previous backups were kept"
	fi
}

seconds_until() {
	now=$(date +%s)
	target=$(date -d "$1" +%s)
	if [ "$target" -le "$now" ]; then
		target=$((target + 86400))
	fi
	echo $((target - now))
}

log "daily backup at $BACKUP_TIME (${TZ:-UTC}), retention ${BACKUP_RETENTION_DAYS:-14} days, target $BACKUP_DIR"

mkdir -p "$BACKUP_DIR/db"
if [ -z "$(find "$BACKUP_DIR/db" -type f -name '*.dump' -mmin -1440 2>/dev/null | head -n 1)" ]; then
	log "no dump from the last 24 hours; running a backup now"
	run_backup
fi

while :; do
	wait_s=$(seconds_until "$BACKUP_TIME")
	retry_s=$((BACKUP_RETRY_MINUTES * 60))
	if [ "$last_failed" = 1 ] && [ "$wait_s" -gt "$retry_s" ]; then
		wait_s=$retry_s
		log "retrying the failed backup in ${wait_s}s"
	else
		log "next backup in ${wait_s}s"
	fi
	sleep "$wait_s" &
	wait $!
	run_backup
	sleep 61 &
	wait $!
done
