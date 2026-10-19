#!/bin/sh
# Tests of deploy/local/stack.sh that need no Docker: a stub "docker" on PATH
# records every call, and a throwaway repository and state directory hold the
# migration files. Run: sh deploy/local/stack_test.sh (or make local-test).
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d "${TMPDIR:-/tmp}/jt-stack-test.XXXXXX")
trap 'rm -rf "$T"' EXIT INT TERM
failures=0

fail() {
	printf 'FAIL %s\n' "$*" >&2
	failures=$((failures + 1))
}

mkdir -p "$T/bin"
cat >"$T/bin/docker" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$JT_TEST_LOG"
case "$*" in
*pg_dump*)
	[ -z "${JT_TEST_DUMP_FAIL:-}" ] || exit 1
	printf 'PGDMP test dump'
	;;
*psql*) cat >>"$JT_TEST_LOG.sql" ;;
esac
exit 0
EOF
chmod +x "$T/bin/docker"

# setup: version 9 was applied from one file and edited in the tree afterwards.
setup() {
	rm -rf "$T/repo" "$T/state" "$T/docker.log" "$T/docker.log.sql"
	mkdir -p "$T/repo/admin/backend/migrations" "$T/state/migrations-applied"
	printf -- '-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 2;\n' >"$T/state/migrations-applied/00009_region.sql"
	printf -- '-- +goose Up\nSELECT 1; -- edited\n-- +goose Down\nSELECT 2;\n' >"$T/repo/admin/backend/migrations/00009_region.sql"
	: >"$T/state/compose.env"
	: >"$T/state/tunnel.env"
	: >"$T/docker.log"
}

# redo [VAR=value...]: rebuild's migration step alone; output in $T/out.
redo() {
	env JT_REPO="$T/repo" JT_STACK_DIR="$T/state" JT_TEST_LOG="$T/docker.log" PATH="$T/bin:$PATH" "$@" \
		sh "$HERE/stack.sh" _redo-migrations >"$T/out" 2>&1
}

# 1. By default a changed applied migration is only reported: the database
#    (real users' data) is not touched and the applied copy stays.
setup
if ! redo; then fail "default: exit status $?: $(cat "$T/out")"; fi
grep -q 'changed in the tree' "$T/out" || fail "default: no warning: $(cat "$T/out")"
[ ! -s "$T/docker.log" ] || fail "default: docker was called: $(cat "$T/docker.log")"
[ -f "$T/state/migrations-applied/00009_region.sql" ] || fail "default: applied copy removed"

# 2. Opt-in redo takes a backup first, then rolls back with the applied Down.
setup
if ! redo JT_AUTO_REDO_MIGRATIONS=1; then fail "opt-in: exit status: $(cat "$T/out")"; fi
first=$(head -n 1 "$T/docker.log")
case $first in *pg_dump*-Fc*) ;; *) fail "opt-in: first docker call is not pg_dump -Fc: $first" ;; esac
grep -q 'psql' "$T/docker.log" || fail "opt-in: no rollback"
dump=$(find "$T/state/backups/pre-redo" -name 'pre-redo-*.dump' 2>/dev/null | head -n 1)
[ -n "$dump" ] && [ "$(cat "$dump")" = 'PGDMP test dump' ] || fail "opt-in: no backup file"
grep -q "pre-redo/$(basename "$dump")" "$T/out" || fail "opt-in: backup path not printed: $(cat "$T/out")"
grep -q 'SELECT 2;' "$T/docker.log.sql" && grep -q 'DELETE FROM goose_db_version WHERE version_id = 9;' "$T/docker.log.sql" ||
	fail "opt-in: rollback SQL: $(cat "$T/docker.log.sql" 2>/dev/null)"
[ ! -f "$T/state/migrations-applied/00009_region.sql" ] || fail "opt-in: applied copy kept after rollback"

# 3. No backup, no rollback.
setup
if redo JT_AUTO_REDO_MIGRATIONS=1 JT_TEST_DUMP_FAIL=1; then fail "failed backup: exit status 0"; fi
grep -q 'nothing was rolled back' "$T/out" || fail "failed backup: message: $(cat "$T/out")"
! grep -q 'psql' "$T/docker.log" || fail "failed backup: rollback ran anyway"
[ -f "$T/state/migrations-applied/00009_region.sql" ] || fail "failed backup: applied copy removed"
[ -z "$(find "$T/state/backups/pre-redo" -type f 2>/dev/null)" ] || fail "failed backup: partial file left"

# 4. Versions below JT_REDO_MIN_VERSION are never rolled back.
setup
if ! redo JT_AUTO_REDO_MIGRATIONS=1 JT_REDO_MIN_VERSION=10; then fail "floor: exit status: $(cat "$T/out")"; fi
[ ! -s "$T/docker.log" ] || fail "floor: docker was called: $(cat "$T/docker.log")"

# 5. Nothing changed: nothing to say, nothing called.
setup
cp "$T/state/migrations-applied/00009_region.sql" "$T/repo/admin/backend/migrations/00009_region.sql"
if ! redo JT_AUTO_REDO_MIGRATIONS=1; then fail "unchanged: exit status: $(cat "$T/out")"; fi
[ ! -s "$T/docker.log" ] && [ ! -s "$T/out" ] || fail "unchanged: $(cat "$T/out") $(cat "$T/docker.log")"

if [ "$failures" -ne 0 ]; then
	printf '%s stack.sh test(s) failed\n' "$failures" >&2
	exit 1
fi
echo "stack.sh: migration redo tests passed"
