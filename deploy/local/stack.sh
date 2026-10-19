#!/bin/sh
# Jan Taxi local live stack: the whole platform in Docker on one machine,
# reachable from real Telegram bots through a Cloudflare quick tunnel.
# Development only (docs/DEPLOYMENT.md §10).
#
#   JT_STACK_DIR=<state dir> deploy/local/stack.sh <command>
#
# Commands:
#   env               write <state>/compose.env from the source env file
#   up                start everything (builds missing images), follow the tunnel
#                     URL, point the bots' menu buttons at it, start the watchdog
#   rebuild           rebuild api, bot and web images from the current tree, run
#                     migrations, recreate api, bots and web; the tunnel (and its
#                     URL) is kept. Applied migrations that were edited in the tree
#                     since are only reported (see "migrations" below).
#   retunnel          start a new quick tunnel and move api, bots and menu buttons to it
#   status            containers, tunnel URL and probes, /readyz, bot health JSON,
#                     menu buttons, admin panel, watchdog (exit 1 if anything is off)
#   logs [service]    follow logs (api, passenger-bot, driver-bot, web, db, tunnel,
#                     migrate, db-backup) or the watchdog log ("logs watchdog")
#   down              stop the watchdog and remove the containers (volumes are kept)
#   verify            deeper checks: SPA deep links and WebSocket upgrades through
#                     the tunnel, client IP seen by the API, admin login/logout
#   menus             re-publish the default menu buttons and reset per-chat buttons
#   watchdog start|stop|status
#   import-devdb [--replace]
#                     one-time copy of a PostgreSQL database (default: container
#                     jantaxi-devdb, database jantaxi) and of a files directory
#                     into the stack; refuses to overwrite unless --replace
#   psql              psql shell in the stack database
#
# Environment:
#   JT_STACK_DIR      state directory, outside the repository (required): compose.env,
#                     tunnel.env, logs/, backups/, watchdog.pid
#   JT_SOURCE_ENV     env file that "env" reads (default <repo>/.env)
#   JT_PROJECT        Compose project name (default jantaxi-local)
#   JT_DEVDB_CONTAINER, JT_DEVDB_USER, JT_DEVDB_NAME, JT_IMPORT_FILES_DIR   import-devdb source
#   JT_AUTO_REDO_MIGRATIONS (default 0), JT_REDO_MIN_VERSION (default 7)    see "migrations" below
#   JT_TUNNEL_FAIL_LIMIT  watchdog probes (30 s apart) of Cloudflare 530/1033 before a new tunnel (default 20)
#
# Secrets are read from the env files and never printed. Telegram calls pass the
# bot token to curl on stdin, so it does not appear in the process list.
set -eu
umask 077

# The whole script is one { } block, so the shell reads all of it before it
# runs anything: editing this file can never change a running command midway.
{

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO=${JT_REPO:-$(cd "$SCRIPT_DIR/../.." && pwd)}
PROJECT=${JT_PROJECT:-jantaxi-local}
TG_API=https://api.telegram.org

die() { printf 'stack: %s\n' "$*" >&2; exit 1; }
say() { printf '%s\n' "$*"; }
warn() { printf 'WARNING %s\n' "$*" >&2; }
log() { printf '%s %s\n' "$(date '+%F %T')" "$*"; }

STATE=${JT_STACK_DIR:-}
[ -n "$STATE" ] || die "set JT_STACK_DIR to the state directory (outside the repository; it holds secrets)"
mkdir -p "$STATE/logs"
STATE=$(cd "$STATE" && pwd -P)
case "$STATE/" in "$(cd "$REPO" && pwd -P)"/*) die "JT_STACK_DIR must be outside the repository" ;; esac

ENV_FILE=$STATE/compose.env
TUNNEL_ENV=$STATE/tunnel.env
LOCK=$STATE/.lock
WD_PID=$STATE/watchdog.pid
WD_LOG=$STATE/logs/watchdog.log
CORE="db migrate api passenger-bot driver-bot web db-backup"
MIG_DIR=$REPO/admin/backend/migrations
MIG_SAVED=$STATE/migrations-applied

# ---------------------------------------------------------------- env files

# getv KEY FILE: last value of KEY with one layer of quotes removed.
getv() {
	[ -f "$2" ] || return 0
	sed -n "s/^$1=//p" "$2" | tail -n 1 | sed \
		-e "s/^'\(.*\)'\$/\1/" \
		-e 's/^"\(.*\)"$/\1/'
}

# quote VALUE for a Compose env file: single quotes are literal (no $
# interpolation); values that contain a single quote use double quotes.
quote() {
	case $1 in
	*"'"*) printf '"%s"' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/\$/$$/g')" ;;
	*) printf "'%s'" "$1" ;;
	esac
}

kv() { printf '%s=%s\n' "$1" "$(quote "$2")"; }

# getd KEY DEFAULT: value of KEY in compose.env, or DEFAULT.
getd() {
	v=$(getv "$1" "$ENV_FILE")
	printf '%s' "${v:-$2}"
}

# Values copied from the source env file when present.
COPY_KEYS="PASSENGER_BOT_TOKEN PASSENGER_BOT_USERNAME DRIVER_BOT_TOKEN DRIVER_BOT_USERNAME
PASSENGER_MINIAPP_SHORT_NAME DRIVER_MINIAPP_SHORT_NAME
SESSION_SECRET INTERNAL_BOT_SERVICE_SECRET ADMIN_BOOTSTRAP_EMAIL ADMIN_BOOTSTRAP_PASSWORD
KASPI_PAY_PAYMENT_URL KASPI_PAY_RECIPIENT_NAME KASPI_PAY_PAYMENT_INSTRUCTIONS
MAP_STYLE_URL MAP_API_KEY GEOCODING_PROVIDER GEOCODING_BASE_URL GEOCODING_API_KEY
OSRM_BASE_URL OSRM_TIMEOUT TWOGIS_BASE_URL
DEFAULT_CITY DEFAULT_LANGUAGE MAX_ROUTE_POINTS DEFAULT_COMMISSION_PERCENT
DRIVER_ONLINE_IDLE_TIMEOUT TELEGRAM_INITDATA_MAX_AGE APP_TOKEN_TTL MAX_UPLOAD_BYTES
NOTIFICATION_WORKER_ENABLED JOBS_ENABLED LOG_LEVEL LOG_FORMAT"

cmd_env() {
	src=${JT_SOURCE_ENV:-$REPO/.env}
	[ -f "$src" ] || die "source env file $src not found"
	pg=$(getv POSTGRES_PASSWORD "$ENV_FILE")
	[ -n "$pg" ] || pg=$(openssl rand -hex 24)
	case $(uname -m) in
	arm64 | aarch64) postgis=imresamu/postgis:17-3.5-alpine ;;
	*) postgis=postgis/postgis:17-3.5 ;;
	esac
	tmp=$ENV_FILE.tmp.$$
	{
		say "# Jan Taxi local live stack (compose.yaml + compose.local.yaml)."
		say "# Written by deploy/local/stack.sh from $src on $(date '+%F %T')."
		say "# Holds secrets: keep it outside the repository. Rewrite with: stack.sh env"
		say "# The tunnel URLs live in tunnel.env (rewritten on every tunnel change)."
		say ""
		kv COMPOSE_PROJECT_NAME "$PROJECT"
		kv APP_ENV development
		kv IMAGE_REGISTRY jantaxi
		kv IMAGE_TAG local
		kv POSTGIS_IMAGE "$postgis"
		kv POSTGRES_PASSWORD "$pg"
		say ""
		say "# Edge: plain HTTP inside the machine; HTTPS comes from the tunnel."
		kv APP_DOMAIN :8088
		kv ADMIN_DOMAIN :8089
		kv ACME_EMAIL ops@localhost.invalid
		kv CADDY_GLOBAL_OPTIONS "auto_https off"
		kv CADDY_SERVER_OPTIONS "trusted_proxies static private_ranges"
		kv ADMIN_FRONTEND_URL http://localhost:8089/
		kv ADMIN_COOKIE_SECURE false
		say ""
		say "# Laptop-sized limits and local backups."
		kv DB_MEM_LIMIT 1g
		kv DB_CPUS 1
		kv PG_SHARED_BUFFERS 256MB
		kv PG_EFFECTIVE_CACHE_SIZE 768MB
		kv API_MEM_LIMIT 768m
		kv API_CPUS 1
		kv LOG_MAX_SIZE 10m
		kv LOG_MAX_FILE 3
		kv BACKUP_HOST_DIR "$STATE/backups"
		kv BACKUP_TZ Asia/Aqtobe
		say ""
		say "# From $src"
		for k in $COPY_KEYS; do
			v=$(getv "$k" "$src")
			[ -n "$v" ] || continue
			case $k in
			OSRM_BASE_URL | GEOCODING_BASE_URL)
				# Containers reach the host as host.docker.internal.
				v=$(printf '%s' "$v" | sed -e 's#//localhost#//host.docker.internal#' -e 's#//127\.0\.0\.1#//host.docker.internal#')
				;;
			esac
			kv "$k" "$v"
		done
		# Development fallbacks for routing and geocoding (public demo servers).
		[ -n "$(getv OSRM_BASE_URL "$src")" ] || kv OSRM_BASE_URL https://router.project-osrm.org
		[ -n "$(getv GEOCODING_PROVIDER "$src")" ] || kv GEOCODING_PROVIDER photon
	} >"$tmp"
	chmod 600 "$tmp"
	mv "$tmp" "$ENV_FILE"
	mkdir -p "$STATE/backups"
	say "wrote $ENV_FILE"
}

ensure_env() {
	[ -f "$ENV_FILE" ] || cmd_env
	[ -f "$TUNNEL_ENV" ] || write_tunnel_env ""
}

# The same env check as `make up` (deploy/preflight.sh), quiet when it passes.
preflight() {
	out=$(sh "$REPO/deploy/preflight.sh" "$ENV_FILE" "$TUNNEL_ENV" 2>&1) || {
		printf '%s\n' "$out" >&2
		die "fix $ENV_FILE (or rewrite it: stack.sh env)"
	}
}

# write_tunnel_env URL: the URL-dependent settings (empty URL: localhost only).
write_tunnel_env() {
	url=$1
	base=${url:-http://localhost:8088}
	tmp=$TUNNEL_ENV.tmp.$$
	{
		say "# Current tunnel; rewritten by stack.sh (up, retunnel, watchdog)."
		kv TUNNEL_URL "$url"
		kv PASSENGER_MINIAPP_URL "$base/passenger/"
		kv DRIVER_MINIAPP_URL "$base/driver/"
		kv API_PUBLIC_URL "$base"
		if [ -n "$url" ]; then
			kv CORS_ALLOWED_ORIGINS "$url,http://localhost:8088"
		else
			kv CORS_ALLOWED_ORIGINS http://localhost:8088
		fi
	} >"$tmp"
	chmod 600 "$tmp"
	mv "$tmp" "$TUNNEL_ENV"
}

recorded_url() { getv TUNNEL_URL "$TUNNEL_ENV"; }

dc() {
	docker compose -p "$PROJECT" --project-directory "$REPO" \
		-f "$REPO/compose.yaml" -f "$REPO/compose.local.yaml" \
		--env-file "$ENV_FILE" --env-file "$TUNNEL_ENV" "$@"
}

dbq() {
	dc exec -T db psql -X -v ON_ERROR_STOP=1 -U "$(pg_user)" -d "$(pg_db)" -tAc "$1"
}
pg_user() { u=$(getv POSTGRES_USER "$ENV_FILE"); printf '%s' "${u:-jantaxi}"; }
pg_db() { d=$(getv POSTGRES_DB "$ENV_FILE"); printf '%s' "${d:-jantaxi}"; }

# ---------------------------------------------------------------- locking

# One state-changing command at a time (the watchdog skips a cycle instead).
take_lock() {
	if mkdir "$LOCK" 2>/dev/null; then
		echo $$ >"$LOCK/pid"
		trap 'rm -rf "$LOCK"' EXIT
		trap 'exit 130' INT TERM
		return 0
	fi
	pid=$(cat "$LOCK/pid" 2>/dev/null || true)
	if [ -n "$pid" ] && ! kill -0 "$pid" 2>/dev/null; then
		rm -rf "$LOCK"
	elif [ -z "$pid" ] && [ -n "$(find "$LOCK" -maxdepth 0 -mmin +2 2>/dev/null)" ]; then
		rm -rf "$LOCK"
	fi
	return 1
}

lock() {
	i=0
	until take_lock; do
		i=$((i + 1))
		[ "$i" -eq 1 ] && say "waiting for another stack.sh command to finish..."
		[ "$i" -gt 900 ] && die "another stack.sh command holds $LOCK"
		sleep 1
	done
}

# ---------------------------------------------------------------- tunnel

metrics_port() { getd LOCAL_TUNNEL_METRICS_PORT 20241; }

# URL the running cloudflared serves, from its metrics endpoint (empty if none).
tunnel_url_now() {
	h=$(curl -s -m 3 "http://127.0.0.1:$(metrics_port)/quicktunnel" 2>/dev/null |
		sed -n 's/.*"hostname" *: *"\([a-z0-9.-]*\)".*/\1/p')
	[ -n "$h" ] && printf 'https://%s\n' "$h"
	return 0
}

# wait_tunnel_url [OLD]: wait for cloudflared to report a URL other than OLD.
wait_tunnel_url() {
	old=${1:-}
	i=0
	while [ "$i" -lt 60 ]; do
		u=$(tunnel_url_now)
		if [ -n "$u" ] && [ "$u" != "$old" ]; then
			printf '%s\n' "$u"
			return 0
		fi
		sleep 2
		i=$((i + 1))
	done
	return 1
}

# A new trycloudflare.com name does not resolve for its first seconds. A
# lookup through the local network's resolver at that moment gets cached as
# "no such name" (by mDNSResponder and by home routers, sometimes for many
# minutes), and then nothing on this network - browsers, Telegram Desktop,
# phones on the same Wi-Fi - can open the Mini Apps. Probes therefore never
# use the local resolvers: dig asks public resolvers and curl gets --resolve.
pub_ip() {
	command -v dig >/dev/null 2>&1 || return 0
	for ns in 1.1.1.1 8.8.8.8 1.0.0.1 8.8.4.4 9.9.9.9; do
		ip=$(dig +short +time=2 +tries=1 "$1" A "@$ns" 2>/dev/null |
			grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' | head -n 1 || true)
		if [ -n "$ip" ]; then
			printf '%s\n' "$ip"
			return 0
		fi
	done
}

# pub_curl URL CURL_ARGS...: curl a public tunnel URL (exit 6 "could not
# resolve" while the name does not resolve anywhere yet).
pub_curl() {
	u=$1
	shift
	h=${u#https://}
	h=${h%%/*}
	ip=$(pub_ip "$h")
	if [ -n "$ip" ]; then
		curl --resolve "$h:443:$ip" "$@" "$u"
	elif command -v dig >/dev/null 2>&1; then
		printf '000'
		return 6
	else
		curl "$@" "$u"
	fi
}

pub_code() { pub_curl "$1" -s -o /dev/null -m 15 -w '%{http_code}' 2>/dev/null || true; }

wait_public() {
	i=0
	while [ "$i" -lt 40 ]; do
		[ "$(pub_code "$1/passenger/")" = 200 ] && return 0
		sleep 3
		i=$((i + 1))
	done
	return 1
}

# ---------------------------------------------------------------- Telegram

# tg TOKEN METHOD JSON: Bot API call; the token reaches curl on stdin.
tg() {
	printf 'url = "%s/bot%s/%s"\n' "$TG_API" "$1" "$2" |
		curl -s -m 15 -K - -H 'Content-Type: application/json' -d "$3" 2>/dev/null || true
}

json_str() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p" | sed 's#\\/#/#g' | head -n 1; }

default_menu_url() { tg "$1" getChatMenuButton '{}' | json_str url; }

# do_menus URL: both bots' default menu buttons must open the tunnel's Mini
# Apps. Each bot publishes its button when it takes the polling lease, but
# getChatMenuButton can report the previous value for a few minutes, so a
# button that does not read back as expected within ~15 s is set again here
# (same text, same URL: harmless). Then every known chat's own button is reset
# to the default.
do_menus() {
	url=$1
	[ -n "$url" ] || return 0
	ptok=$(getv PASSENGER_BOT_TOKEN "$ENV_FILE")
	dtok=$(getv DRIVER_BOT_TOKEN "$ENV_FILE")
	for spec in "passenger $ptok" "driver $dtok"; do
		name=${spec%% *}
		tok=${spec#* }
		[ -n "$tok" ] && [ "$tok" != "$name" ] || { warn "$name bot token missing"; continue; }
		want=$url/$name/
		i=0
		cur=""
		while [ "$i" -lt 5 ]; do
			cur=$(default_menu_url "$tok")
			case $cur in "$want"*) break ;; esac
			sleep 3
			i=$((i + 1))
		done
		case $cur in
		"$want"*) say "  $name bot menu button: $cur" ;;
		*)
			text=$(tg "$tok" getChatMenuButton '{}' | json_str text)
			[ -n "$text" ] || text="Jan Taxi"
			res=$(tg "$tok" setChatMenuButton "{\"menu_button\":{\"type\":\"web_app\",\"text\":\"$text\",\"web_app\":{\"url\":\"$want\"}}}")
			case $res in *'"ok":true'*) say "  $name bot menu button set to $want" ;; *) warn "$name bot menu button not set" ;; esac
			;;
		esac
	done
	reset_chat_menus "$ptok" "$dtok"
	printf '%s\n' "$url" >"$STATE/.menus_url"
}

reset_chat_menus() {
	ids=$(dbq "SELECT telegram_user_id FROM users WHERE telegram_user_id IS NOT NULL" 2>/dev/null | grep -E '^[0-9]+$' || true)
	n=0
	ok=0
	for id in $ids; do
		for tok in "$@"; do
			[ -n "$tok" ] || continue
			n=$((n + 1))
			case $(tg "$tok" setChatMenuButton "{\"chat_id\":$id,\"menu_button\":{\"type\":\"default\"}}") in
			*'"ok":true'*) ok=$((ok + 1)) ;;
			esac
		done
	done
	say "  per-chat menu buttons reset to default: $ok of $n (chats that never opened a bot fail)"
}

# ---------------------------------------------------------------- migrations
#
# The stack keeps a copy of every migration file it has applied
# (<state>/migrations-applied) and reports applied versions whose file was
# edited in the tree since ("up", "rebuild", "status"). The database keeps
# real users' data, so nothing is rolled back by default: a schema change goes
# into a new migration. Only with JT_AUTO_REDO_MIGRATIONS=1 does "rebuild"
# redo changed versions from JT_REDO_MIN_VERSION (default 7) on: it first
# writes a pg_dump (custom format) of the database to
# <state>/backups/pre-redo/, then rolls the applied copies back with their own
# "-- +goose Down" sections (newest first, one transaction each) and migrates
# again. Older versions are never rolled back automatically.

# words TEXT: newline-separated items on one line.
words() { printf '%s' "$1" | tr '\n' ' ' | sed 's/ *$//'; }

mig_version() { printf '%s' "$1" | sed -E 's/^0*([0-9]+)_.*/\1/'; }

schema_version() { dbq "SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied" 2>/dev/null || true; }

# Save a copy of every applied migration file not saved yet.
snapshot_migrations() {
	v=$(schema_version)
	[ -n "$v" ] || return 0
	mkdir -p "$MIG_SAVED"
	for f in "$MIG_DIR"/[0-9]*_*.sql; do
		[ -f "$f" ] || continue
		b=$(basename "$f")
		[ "$(mig_version "$b")" -le "$v" ] || continue
		ls "$MIG_SAVED/${b%%_*}"_*.sql >/dev/null 2>&1 || cp "$f" "$MIG_SAVED/$b"
	done
}

# Versions whose applied copy differs from the tree (or left the tree).
changed_migrations() {
	for f in "$MIG_SAVED"/[0-9]*_*.sql; do
		[ -f "$f" ] || continue
		b=$(basename "$f")
		cur=""
		for c in "$MIG_DIR/${b%%_*}"_*.sql; do
			[ -f "$c" ] && cur=$c && break
		done
		if [ -z "$cur" ] || ! cmp -s "$f" "$cur"; then mig_version "$b"; fi
	done
}

# rollback_from N: undo applied versions >= N with the saved copies' Down sections.
rollback_from() {
	for f in "$MIG_SAVED"/[0-9]*_*.sql; do
		[ -f "$f" ] && printf '%s\n' "$f"
	done | sort -r | while IFS= read -r f; do
		b=$(basename "$f")
		v=$(mig_version "$b")
		[ "$v" -ge "$1" ] || continue
		grep -q '^-- +goose Down' "$f" || {
			warn "$b has no Down section"
			return 1
		}
		say "  rolling back $b (the version this stack applied)"
		{
			sed -n '/^-- +goose Down/,$p' "$f"
			printf '\nDELETE FROM goose_db_version WHERE version_id = %s;\n' "$v"
		} | dc exec -T db psql -X -q -v ON_ERROR_STOP=1 --single-transaction \
			-U "$(pg_user)" -d "$(pg_db)" >/dev/null || return 1
		rm -f "$f"
	done
}

# backup_before_redo: pg_dump (custom format) of the stack database into
# <state>/backups/pre-redo/; sets REDO_BACKUP to the file. Fails without
# leaving a partial file.
backup_before_redo() {
	dir=$STATE/backups/pre-redo
	mkdir -p "$dir"
	REDO_BACKUP=$dir/pre-redo-$(date '+%Y%m%d-%H%M%S').dump
	if dc exec -T db pg_dump -Fc -U "$(pg_user)" -d "$(pg_db)" >"$REDO_BACKUP.tmp" && [ -s "$REDO_BACKUP.tmp" ]; then
		mv "$REDO_BACKUP.tmp" "$REDO_BACKUP"
		say "database backup before the rollback: $REDO_BACKUP (restore with pg_restore)"
		return 0
	fi
	rm -f "$REDO_BACKUP.tmp"
	return 1
}

# Called before migrating in rebuild.
redo_changed_migrations() {
	changed=$(changed_migrations)
	[ -n "$changed" ] || return 0
	min=$(printf '%s\n' "$changed" | sort -n | head -n 1)
	floor=${JT_REDO_MIN_VERSION:-7}
	if [ "${JT_AUTO_REDO_MIGRATIONS:-0}" != 1 ]; then
		warn "applied migration(s) $(words "$changed") changed in the tree; the database keeps the applied version (put schema changes into a new migration; JT_AUTO_REDO_MIGRATIONS=1 redoes them after a backup)"
		return 0
	fi
	if [ "$min" -lt "$floor" ]; then
		warn "applied migration(s) $(words "$changed") changed in the tree; versions below $floor are never rolled back automatically"
		return 0
	fi
	backup_before_redo || die "could not back up the database; nothing was rolled back"
	say "== migration(s) $(words "$changed") changed after they were applied; redoing from $min"
	rollback_from "$min" || die "rollback failed (nothing of that step was changed; backup $REDO_BACKUP); fix the database by hand: stack.sh psql"
}

# ---------------------------------------------------------------- watchdog

wd_running() {
	pid=$(cat "$WD_PID" 2>/dev/null || true)
	[ -n "$pid" ] && kill -0 "$pid" 2>/dev/null && ps -p "$pid" -o command= 2>/dev/null | grep -q 'watchdog'
}

wd_start() {
	if wd_running; then
		say "watchdog running (pid $(cat "$WD_PID"))"
		return 0
	fi
	export JT_STACK_DIR="$STATE" JT_REPO="$REPO" JT_PROJECT="$PROJECT"
	[ -z "${JT_SOURCE_ENV:-}" ] || export JT_SOURCE_ENV
	# A new session, so the watchdog outlives the shell that started it.
	if command -v perl >/dev/null 2>&1; then
		nohup perl -MPOSIX -e 'POSIX::setsid(); exec @ARGV or die' \
			/bin/sh "$REPO/deploy/local/watchdog.sh" >>"$WD_LOG" 2>&1 </dev/null &
	else
		nohup /bin/sh "$REPO/deploy/local/watchdog.sh" >>"$WD_LOG" 2>&1 </dev/null &
	fi
	echo $! >"$WD_PID"
	sleep 1
	if wd_running; then say "watchdog started (pid $(cat "$WD_PID"), log $WD_LOG)"; else warn "watchdog did not start; see $WD_LOG"; fi
}

wd_stop() {
	if wd_running; then
		kill "$(cat "$WD_PID")" 2>/dev/null || true
		say "watchdog stopped"
	fi
	rm -f "$WD_PID"
}

# ---------------------------------------------------------------- commands

summary() {
	url=$(recorded_url)
	pu=$(getv PASSENGER_BOT_USERNAME "$ENV_FILE")
	du=$(getv DRIVER_BOT_USERNAME "$ENV_FILE")
	say ""
	say "Passenger Mini App  ${url:-<no tunnel>}/passenger/"
	say "Driver Mini App     ${url:-<no tunnel>}/driver/"
	say "Admin panel         http://localhost:$(getd LOCAL_ADMIN_PORT 8089)/"
	[ -z "$pu" ] || say "Passenger bot       https://t.me/$pu"
	[ -z "$du" ] || say "Driver bot          https://t.me/$du"
}

# apply_url URL: move api and bots to URL and refresh the menu buttons. The
# bots publish the URL only once it answers (see pub_ip).
apply_url() {
	wait_public "$1" || warn "$1/passenger/ is not answering 200 yet"
	write_tunnel_env "$1"
	dc up -d --wait --wait-timeout 300 --no-deps api
	dc up -d --wait --wait-timeout 300 --no-deps passenger-bot driver-bot
	do_menus "$1"
}

cmd_up() {
	ensure_env
	preflight
	lock
	rm -f "$STATE/.down"
	say "== database"
	dc up -d --wait --wait-timeout 180 db
	say "== web and tunnel (images are built when missing)"
	dc up -d --no-deps web tunnel
	url=$(wait_tunnel_url) || die "cloudflared did not report a tunnel URL; see: stack.sh logs tunnel"
	say "tunnel: $url"
	wait_public "$url" || warn "$url/passenger/ is not answering 200 yet"
	write_tunnel_env "$url"
	say "== migrate, api, bots, web, backups"
	# The tunnel is not listed: recreating it would change the URL.
	rc=0
	# shellcheck disable=SC2086
	dc up -d --wait --wait-timeout 420 $CORE || {
		warn "not every service is healthy yet; check: stack.sh status"
		rc=1
	}
	snapshot_migrations
	ch=$(changed_migrations)
	[ -z "$ch" ] || warn "applied migration(s) $(words "$ch") changed in the tree since they were applied; the database keeps the applied version"
	if [ "$(cat "$STATE/.menus_url" 2>/dev/null || true)" != "$url" ]; then
		say "== menu buttons"
		do_menus "$url"
	fi
	wd_start
	summary
	return $rc
}

# image SERVICE: the image name compose.yaml gives the service.
image() { printf '%s/%s:%s' "$(getd IMAGE_REGISTRY jantaxi)" "$1" "$(getd IMAGE_TAG local)"; }

# Keep the running images as <image>-previous before a build.
save_images() {
	for s in api passenger-bot driver-bot web; do
		if docker image inspect "$(image "$s")" >/dev/null 2>&1; then
			docker image tag "$(image "$s")" "$(image "$s")-previous"
		fi
	done
}

# restore_images SERVICE...: put the saved images back and recreate the services.
restore_images() {
	for s in "$@"; do
		if docker image inspect "$(image "$s")-previous" >/dev/null 2>&1; then
			docker image tag "$(image "$s")-previous" "$(image "$s")"
		fi
	done
	dc up -d --wait --wait-timeout 300 --no-deps --force-recreate "$@" || true
}

cmd_rebuild() {
	ensure_env
	preflight
	lock
	url=$(recorded_url)
	[ -n "$url" ] || die "the stack is not up; run: stack.sh up"
	save_images
	say "== building images from $REPO"
	dc build api passenger-bot driver-bot web || die "build failed; the running containers were not touched"
	redo_changed_migrations
	say "== migrations"
	dc run --rm -T migrate || die "migration failed; api, bots and web still run the previous images"
	snapshot_migrations
	say "== api"
	if ! dc up -d --wait --wait-timeout 300 --no-deps --force-recreate api; then
		dc logs --tail 40 --no-log-prefix api >&2 || true
		say "== the new api is not healthy (log above); starting the previous image again"
		restore_images api
		die "rebuild failed at the api (the previous api image runs again; it may not accept a schema migrated by the new one)"
	fi
	say "== bots and web"
	if ! dc up -d --wait --wait-timeout 300 --no-deps --force-recreate passenger-bot driver-bot web; then
		for s in passenger-bot driver-bot web; do dc logs --tail 15 --no-log-prefix "$s" >&2 || true; done
		say "== bots or web not healthy (logs above); starting the previous images again"
		restore_images passenger-bot driver-bot web
		die "rebuild failed at the bots or web (previous images run again; the new api stays)"
	fi
	now=$(tunnel_url_now)
	if [ -n "$now" ] && [ "$now" != "$url" ]; then
		warn "the tunnel URL changed meanwhile ($now); following it"
		apply_url "$now"
	fi
	say "rebuilt at $(date '+%F %T'); tunnel unchanged: $(recorded_url)"
}

do_retunnel() {
	old=$(recorded_url)
	dc up -d --no-deps --force-recreate tunnel
	url=$(wait_tunnel_url "$old") || die "cloudflared did not report a new tunnel URL; see: stack.sh logs tunnel"
	say "tunnel: $url"
	apply_url "$url"
}

cmd_retunnel() {
	ensure_env
	lock
	rm -f "$STATE/.down"
	do_retunnel
	summary
}

cmd_status() {
	ensure_env
	rc=0
	say "== containers ($PROJECT)"
	{
		printf 'SERVICE\tSTATE\tHEALTH\tSTATUS\n'
		dc ps -a --format '{{.Service}}\t{{.State}}\t{{if .Health}}{{.Health}}{{else}}-{{end}}\t{{.Status}}' 2>/dev/null
	} | column -t -s "$(printf '\t')"
	for svc in db api passenger-bot driver-bot web tunnel db-backup; do
		st=$(svc_state "$svc")
		case $st in
		running/healthy | running/) ;;
		*) say "PROBLEM $svc: $st" && rc=1 ;;
		esac
	done

	url=$(recorded_url)
	now=$(tunnel_url_now)
	say ""
	say "== tunnel"
	say "url: ${url:-<none>}"
	if [ -z "$now" ]; then
		say "PROBLEM cloudflared reports no tunnel"
		rc=1
	elif [ "$now" != "$url" ]; then
		say "PROBLEM cloudflared serves $now (run: stack.sh up)"
		rc=1
	fi
	say "cloudflared /ready: $(curl -s -m 3 "http://127.0.0.1:$(metrics_port)/ready" 2>/dev/null || echo unreachable)"
	if [ -n "$url" ]; then
		for p in /passenger/ /driver/ /driver/orders "/api/v1/public/config?app=passenger" "/api/v1/public/config?app=driver"; do
			code=$(pub_code "$url$p")
			say "  $code  $url$p"
			[ "$code" = 200 ] || rc=1
		done
		# The admin API is served on the local admin port only, never through the tunnel.
		for p in /api/v1/admin/auth/me /api/v1/admin/auth/login; do
			code=$(pub_code "$url$p")
			say "  $code  $url$p (want 404: admin API only on the admin port)"
			[ "$code" = 404 ] || rc=1
		done
	fi

	say ""
	say "== api /readyz"
	if body=$(dc exec -T web wget -q -T 5 -O - http://api:8080/readyz 2>/dev/null); then
		say "$body"
	else
		say "PROBLEM api is not ready"
		rc=1
	fi

	ch=$(changed_migrations)
	[ -z "$ch" ] || say "NOTE applied migration(s) $(words "$ch") changed in the tree since they were applied (the database keeps the applied version; put changes into a new migration)"

	say ""
	say "== bots (health JSON)"
	for spec in "passenger $(getd LOCAL_PASSENGER_BOT_HEALTH_PORT 18091)" \
		"driver $(getd LOCAL_DRIVER_BOT_HEALTH_PORT 18092)"; do
		name=${spec%% *}
		port=${spec#* }
		h=$(curl -s -m 5 "http://127.0.0.1:$port/healthz" 2>/dev/null || true)
		say "$name: ${h:-unreachable}"
		case $h in *'"healthy":true'*) ;; *) rc=1 ;; esac
	done

	say ""
	say "== menu buttons (bot default)"
	for spec in "passenger PASSENGER_BOT_TOKEN" "driver DRIVER_BOT_TOKEN"; do
		name=${spec%% *}
		tok=$(getv "${spec#* }" "$ENV_FILE")
		cur=$(default_menu_url "$tok")
		case $cur in
		"$url/$name/"*) say "$name: $cur" ;;
		*) say "PROBLEM $name: ${cur:-<none>} (expected $url/$name/)" && rc=1 ;;
		esac
	done

	say ""
	say "== admin panel (127.0.0.1 only)"
	ap=$(getd LOCAL_ADMIN_PORT 8089)
	for p in / /drivers "/api/v1/public/config?app=admin"; do
		code=$(curl -s -o /dev/null -m 5 -w '%{http_code}' "http://127.0.0.1:$ap$p" 2>/dev/null || true)
		say "  $code  http://localhost:$ap$p"
		[ "$code" = 200 ] || rc=1
	done

	say ""
	say "== watchdog"
	if wd_running; then say "running (pid $(cat "$WD_PID"))"; else say "PROBLEM not running (stack.sh watchdog start)" && rc=1; fi
	[ ! -f "$STATE/.down" ] || say "stack marked down (stack.sh up clears it)"
	return $rc
}

# Deeper one-off checks (each run creates and ends one admin session).
cmd_verify() {
	ensure_env
	rc=0
	url=$(recorded_url)
	[ -n "$url" ] || die "the stack is not up; run: stack.sh up"
	ap=$(getd LOCAL_ADMIN_PORT 8089)
	key=$(openssl rand -base64 16)

	say "== Mini App deep links through the tunnel (index.html for unknown paths)"
	for p in /passenger/order_00000000-0000-0000-0000-000000000000 /driver/topup /passenger/assets/missing.js; do
		res=$(pub_curl "$url$p" -s -o /dev/null -m 15 -w '%{http_code} %{content_type}' 2>/dev/null || true)
		say "  $res  $p"
	done

	say "== WebSocket /api/v1/ws through the tunnel"
	for origin in "$url" https://other.example; do
		code=$(pub_curl "$url/api/v1/ws" -s -o /dev/null -m 6 --http1.1 -w '%{http_code}' \
			-H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
			-H "Sec-WebSocket-Key: $key" -H "Origin: $origin" 2>/dev/null || true)
		want=101
		[ "$origin" = "$url" ] || want=403
		say "  $code (want $want)  Origin: $origin"
		[ "$code" = "$want" ] || rc=1
	done
	sleep 1
	say "== client address seen by the API for the last tunnel request (must be public)"
	ip=$(dc logs --since 10m --no-log-prefix api 2>/dev/null | grep 'path=/api/v1/ws' | tail -n 1 | sed -n 's/.* ip=\([^ ]*\).*/\1/p')
	case $ip in
	10.* | 172.1[6-9].* | 172.2[0-9].* | 172.3[01].* | 192.168.* | 127.* | "") say "  PROBLEM api sees ${ip:-nothing}" && rc=1 ;;
	*) say "  ok (a public address)" ;;
	esac

	code=$(curl -s -o /dev/null -m 6 --http1.1 -w '%{http_code}' \
		-H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' \
		-H "Sec-WebSocket-Key: $key" -H "Origin: http://localhost:$ap" "http://127.0.0.1:$ap/api/v1/ws" 2>/dev/null || true)
	say "  $code (want 101)  admin panel http://localhost:$ap/api/v1/ws"
	[ "$code" = 101 ] || rc=1

	say "== admin login on http://localhost:$ap (ADMIN_BOOTSTRAP_* from compose.env, not shown)"
	jar=$STATE/.verify-cookies
	rm -f "$jar"
	login=$(JT_E=$(getv ADMIN_BOOTSTRAP_EMAIL "$ENV_FILE") JT_P=$(getv ADMIN_BOOTSTRAP_PASSWORD "$ENV_FILE") \
		python3 -c 'import json,os; print(json.dumps({"email": os.environ["JT_E"], "password": os.environ["JT_P"]}))' |
		curl -s -m 10 -c "$jar" -H 'Content-Type: application/json' -H "Origin: http://localhost:$ap" \
			--data-binary @- -w '\n%{http_code}' "http://127.0.0.1:$ap/api/v1/admin/auth/login" 2>/dev/null || true)
	code=$(printf '%s' "$login" | tail -n 1)
	csrf=$(printf '%s' "$login" | sed -n 's/.*"csrf_token":"\([^"]*\)".*/\1/p')
	say "  login: $code"
	if [ "$code" = 200 ]; then
		me=$(curl -s -m 10 -b "$jar" -w '\n%{http_code}' "http://127.0.0.1:$ap/api/v1/admin/auth/me" 2>/dev/null || true)
		say "  me: $(printf '%s' "$me" | tail -n 1) role=$(printf '%s' "$me" | sed -n 's/.*"role":"\([^"]*\)".*/\1/p' | head -n 1)"
		out=$(curl -s -o /dev/null -m 10 -b "$jar" -X POST -H "X-CSRF-Token: $csrf" -H "Origin: http://localhost:$ap" \
			-w '%{http_code}' "http://127.0.0.1:$ap/api/v1/admin/auth/logout" 2>/dev/null || true)
		say "  logout: $out"
	else
		rc=1
	fi
	rm -f "$jar"
	return $rc
}

svc_state() {
	id=$(dc ps -a -q "$1" 2>/dev/null | head -n 1)
	[ -n "$id" ] || {
		echo missing
		return 0
	}
	docker inspect -f '{{.State.Status}}/{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$id" 2>/dev/null || echo missing
}

cmd_logs() {
	ensure_env
	case ${1:-} in
	watchdog) tail -n 200 -f "$WD_LOG" ;;
	"") dc logs -f --tail=100 ;;
	*) dc logs -f --tail=200 "$@" ;;
	esac
}

cmd_down() {
	ensure_env
	lock
	touch "$STATE/.down"
	wd_stop
	dc down
	write_tunnel_env ""
	rm -f "$STATE/.menus_url"
	say "stack stopped (volumes kept; start again with: stack.sh up)"
}

cmd_menus() {
	ensure_env
	lock
	do_menus "$(recorded_url)"
}

# quietly CMD...: run CMD in a subshell with its output kept out of the
# watchdog log; on failure log its own error lines (stack:/WARNING).
quietly() {
	out=$STATE/.tick-out
	if ("$@") >"$out" 2>&1; then
		rm -f "$out"
		return 0
	fi
	grep -E '^(stack:|WARNING)' "$out" | tail -n 3 | while IFS= read -r line; do log "  $line"; done
	rm -f "$out"
	return 1
}

# One watchdog cycle (deploy/local/watchdog.sh runs it every 30 s):
#  - a stopped api/bot/web/db container is started again; an unhealthy one is
#    restarted (at most once per 3 minutes per service);
#  - when cloudflared serves a different URL (it restarted), api, bots and menu
#    buttons follow it;
#  - the tunnel is recreated only after JT_TUNNEL_FAIL_LIMIT (default 20, about
#    10 minutes) consecutive probes answered by Cloudflare with 530 / error 1033;
#    other errors (e.g. no network) neither count nor reset.
cmd_tick() {
	[ -f "$ENV_FILE" ] || return 0
	[ ! -f "$STATE/.down" ] || return 0
	[ -f "$TUNNEL_ENV" ] || return 0
	docker info >/dev/null 2>&1 || {
		log "docker is not reachable"
		return 0
	}
	take_lock || return 0

	for svc in db api passenger-bot driver-bot web tunnel; do
		st=$(svc_state "$svc")
		case $st in
		running/healthy | running/ | running/starting | restarting/*) ;;
		running/unhealthy)
			[ "$svc" = tunnel ] && continue
			stamp=$STATE/.restarted-$svc
			if [ -z "$(find "$stamp" -mmin -3 2>/dev/null)" ]; then
				log "$svc is unhealthy, restarting"
				quietly dc restart "$svc" || log "restart of $svc failed"
				touch "$stamp"
			fi
			;;
		*)
			if [ "$svc" = tunnel ]; then
				log "tunnel container is $st, starting a new tunnel"
				if quietly do_retunnel; then log "new tunnel $(recorded_url)"; else log "retunnel failed"; fi
				echo 0 >"$STATE/.tunnel_failures"
				return 0
			fi
			log "$svc is $st, starting it"
			quietly dc up -d --no-deps "$svc" || log "start of $svc failed"
			;;
		esac
	done

	url=$(recorded_url)
	now=$(tunnel_url_now)
	if [ -n "$now" ] && [ "$now" != "$url" ]; then
		log "cloudflared now serves $now (was ${url:-none}); moving api, bots and menus"
		if quietly apply_url "$now"; then log "moved to $now"; else log "moving to $now failed"; fi
		echo 0 >"$STATE/.tunnel_failures"
		return 0
	fi
	[ -n "$url" ] || return 0

	body=$STATE/.probe
	code=$(pub_curl "$url/api/v1/public/config?app=passenger" -s -o "$body" -m 15 -w '%{http_code}' 2>/dev/null || true)
	fails=$(cat "$STATE/.tunnel_failures" 2>/dev/null || echo 0)
	case $code in
	530) fails=$((fails + 1)) ;;
	000 | "") ;;
	*)
		if [ "$code" != 200 ] && grep -q '1033' "$body" 2>/dev/null; then
			fails=$((fails + 1))
		else
			fails=0
		fi
		;;
	esac
	rm -f "$body"
	[ "$fails" -eq 0 ] || log "tunnel probe $code ($fails in a row)"
	if [ "$fails" -ge "${JT_TUNNEL_FAIL_LIMIT:-20}" ]; then
		log "tunnel unreachable for about $((fails / 2)) minutes, starting a new tunnel"
		if quietly do_retunnel; then log "new tunnel $(recorded_url)"; else log "retunnel failed"; fi
		fails=0
	fi
	echo "$fails" >"$STATE/.tunnel_failures"
}

cmd_import_devdb() {
	replace=0
	[ "${1:-}" != --replace ] || replace=1
	src=${JT_DEVDB_CONTAINER:-jantaxi-devdb}
	suser=${JT_DEVDB_USER:-jantaxi}
	sdb=${JT_DEVDB_NAME:-jantaxi}
	files=${JT_IMPORT_FILES_DIR:-}
	docker inspect "$src" >/dev/null 2>&1 || die "container $src not found"
	ensure_env
	lock
	dc up -d --wait --wait-timeout 180 db
	if [ "$(dbq "SELECT to_regclass('public.goose_db_version') IS NOT NULL")" = t ] && [ "$replace" = 0 ]; then
		die "the stack database already has data; rerun with --replace to drop it and import again"
	fi
	say "== stopping database clients"
	dc stop api passenger-bot driver-bot db-backup >/dev/null 2>&1 || true
	say "== recreating database $(pg_db)"
	dc exec -T db psql -X -v ON_ERROR_STOP=1 -U "$(pg_user)" -d postgres \
		-c "DROP DATABASE IF EXISTS \"$(pg_db)\" WITH (FORCE)" \
		-c "CREATE DATABASE \"$(pg_db)\" TEMPLATE template0"
	say "== copying $src/$sdb (read-only dump, restored in one transaction)"
	docker exec "$src" pg_dump -U "$suser" -d "$sdb" --no-owner --no-privileges |
		dc exec -T db psql -X -q -v ON_ERROR_STOP=1 --single-transaction -U "$(pg_user)" -d "$(pg_db)" >/dev/null ||
		die "restore failed and was rolled back; the stack database is empty"
	say "schema version: $(dbq 'SELECT max(version_id) FROM goose_db_version WHERE is_applied')"
	say "users: $(dbq 'SELECT count(*) FROM users')"
	if [ -n "$files" ]; then
		[ -d "$files" ] || die "files directory $files not found"
		say "== copying files from $files"
		# The labels make Compose treat the volume as its own "files" volume.
		docker volume inspect "${PROJECT}_files" >/dev/null 2>&1 ||
			docker volume create --label "com.docker.compose.project=$PROJECT" \
				--label com.docker.compose.volume=files "${PROJECT}_files" >/dev/null
		img=$(getv BACKUP_IMAGE "$ENV_FILE")
		# COPYFILE_DISABLE: no macOS "._*" metadata files in the archive.
		COPYFILE_DISABLE=1 tar -C "$files" -cf - . | docker run --rm -i -v "${PROJECT}_files:/data/files" \
			--entrypoint /bin/sh "${img:-postgres:17-alpine}" \
			-c 'tar -xf - -C /data/files && chown -R 65532:65532 /data/files && chmod 0700 /data/files && find /data/files -type f | wc -l'
	fi
	say "imported; start the stack with: stack.sh up"
}

cmd_psql() {
	ensure_env
	dc exec db psql -U "$(pg_user)" -d "$(pg_db)"
}

cmd=${1:-help}
[ $# -eq 0 ] || shift
case $cmd in
env) cmd_env ;;
up) cmd_up ;;
rebuild) cmd_rebuild ;;
retunnel) cmd_retunnel ;;
status) cmd_status ;;
logs) cmd_logs "$@" ;;
down) cmd_down ;;
menus) cmd_menus ;;
watchdog)
	case ${1:-status} in
	start) ensure_env && wd_start ;;
	stop) wd_stop ;;
	status) if wd_running; then say "running (pid $(cat "$WD_PID"))"; else say "not running" && exit 1; fi ;;
	*) die "usage: stack.sh watchdog start|stop|status" ;;
	esac
	;;
verify) cmd_verify ;;
_tick) cmd_tick ;;
_redo-migrations) redo_changed_migrations ;; # rebuild's migration step alone (deploy/local/stack_test.sh)
import-devdb) cmd_import_devdb "$@" ;;
psql) cmd_psql ;;
help | -h | --help) sed -n '2,/^set -eu/p' "$0" | sed -e '/^set -eu/d' -e 's/^# \{0,1\}//' ;;
*) die "unknown command $cmd (stack.sh help)" ;;
esac
exit
}
