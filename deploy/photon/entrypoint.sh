#!/bin/sh
# Photon entry point.
#   serve   serve the index in /photon/photon_data on 0.0.0.0:2322
#   import  download the dump, import it into a staging directory and swap it
#           in only after a successful import (restart the photon service after)
#
# Environment (import): PHOTON_DUMP_URL, PHOTON_COUNTRY_CODES (default kz),
#   PHOTON_LANGUAGES (optional, comma-separated, e.g. en,ru).
# Extra serve flags: PHOTON_SERVE_ARGS.
set -eu

jar=/opt/photon/photon.jar
data=/photon

log() { printf '%s photon: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
fail() { log "ERROR: $*"; exit 1; }

cmd=${1:-serve}
case "$cmd" in
serve)
	[ -d "$data/photon_data" ] || fail "no index in $data/photon_data; run the import first (make geocoder-import)"
	# shellcheck disable=SC2086
	exec java -jar "$jar" serve -data-dir "$data" -listen-ip 0.0.0.0 -listen-port 2322 ${PHOTON_SERVE_ARGS:-}
	;;
import)
	url=${PHOTON_DUMP_URL:?PHOTON_DUMP_URL is required}
	case "$url" in https://*) ;; *) fail "PHOTON_DUMP_URL must be an https URL" ;; esac
	dump="$data/dump.jsonl.zst"
	staging="$data/staging"
	log "downloading $url"
	wget -q -O "$dump.partial" "$url" || fail "download failed"
	zstd -q -t "$dump.partial" || fail "downloaded dump is not a valid zstd file"
	mv "$dump.partial" "$dump"

	rm -rf "$staging"
	mkdir -p "$staging"
	set -- -data-dir "$staging" -import-file - -country-codes "${PHOTON_COUNTRY_CODES:-kz}"
	if [ -n "${PHOTON_LANGUAGES:-}" ]; then
		set -- "$@" -languages "$PHOTON_LANGUAGES"
	fi
	log "importing (country codes ${PHOTON_COUNTRY_CODES:-kz})"
	zstd -q -d --stdout "$dump" | java -jar "$jar" import "$@"

	[ -d "$staging/photon_data" ] || fail "import produced no $staging/photon_data"
	rm -rf "$data/photon_data.previous"
	if [ -d "$data/photon_data" ]; then
		mv "$data/photon_data" "$data/photon_data.previous"
	fi
	mv "$staging/photon_data" "$data/photon_data"
	rm -rf "$staging" "$dump"
	log "index ready; restart the photon service to load it"
	;;
*)
	exec "$@"
	;;
esac
