#!/bin/sh
# Watchdog for the local live stack: runs "stack.sh _tick" every 30 seconds
# (restarts stopped or unhealthy api/bot/web/db containers, follows a changed
# tunnel URL, recreates the quick tunnel only after about 10 minutes of
# Cloudflare 530 / error 1033 answers). Started and stopped by stack.sh
# (up, down, watchdog start|stop); needs JT_STACK_DIR like stack.sh.
#
# The loop lives in one function that is parsed before it runs, so editing
# this file does not disturb a running watchdog. Each cycle runs a private copy
# of stack.sh (<state>/run/stack.sh), refreshed only from a version of the
# repository file that passes `sh -n`, so a half-saved edit is never executed.
set -u

main() {
	dir=$(cd "$(dirname "$0")" && pwd)
	state=${JT_STACK_DIR:?set JT_STACK_DIR}
	log=$state/logs/watchdog.log
	interval=${JT_WATCHDOG_INTERVAL:-30}
	run=$state/run/stack.sh
	mkdir -p "$state/run"
	note() { echo "$(date '+%F %T') $*" >>"$log"; }
	trap 'note "watchdog stopped"; exit 0' INT TERM
	note "watchdog started (pid $$, every ${interval}s)"
	while :; do
		sleep "$interval" &
		wait $!
		# Keep the log small: one previous generation of about 2 MB.
		if [ -f "$log" ] && [ "$(wc -c <"$log" 2>/dev/null || echo 0)" -gt 2000000 ]; then
			mv -f "$log" "$log.1"
			: >"$log"
		fi
		if [ ! -f "$run" ] || [ "$dir/stack.sh" -nt "$run" ]; then
			if cp "$dir/stack.sh" "$run.tmp" 2>/dev/null && sh -n "$run.tmp" 2>/dev/null; then
				mv -f "$run.tmp" "$run"
			else
				rm -f "$run.tmp"
			fi
		fi
		[ -f "$run" ] || continue
		JT_REPO=${JT_REPO:-$(cd "$dir/../.." && pwd)} /bin/sh "$run" _tick >>"$log" 2>&1 ||
			note "cycle failed with status $?"
	done
}

main "$@"; exit
