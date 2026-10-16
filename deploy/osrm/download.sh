#!/bin/sh
# Downloads the OpenStreetMap extract for OSRM into the osrm-data volume and
# verifies it against the MD5 checksum Geofabrik publishes next to the file.
# Runs in the osrm-download service (profile osrm-prepare).
#
# Environment:
#   OSRM_PBF_URL    default https://download.geofabrik.de/asia/kazakhstan-latest.osm.pbf
#   OSRM_SKIP_MD5   set to 1 only for a mirror that publishes no .md5 file
set -eu

url=${OSRM_PBF_URL:-https://download.geofabrik.de/asia/kazakhstan-latest.osm.pbf}
out=/data/kazakhstan-latest.osm.pbf

log() { printf '%s osrm-download: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
fail() { log "ERROR: $*"; exit 1; }

case "$url" in
	https://*) ;;
	*) fail "OSRM_PBF_URL must be an https URL" ;;
esac

ua="JanTaxi-osrm-prepare/1 (+https://download.geofabrik.de)"
log "downloading $url"
rm -f "$out.partial" "$out.md5"
wget -q -U "$ua" -O "$out.partial" "$url" || fail "download failed"

if [ "${OSRM_SKIP_MD5:-0}" = "1" ]; then
	log "WARNING: MD5 verification skipped (OSRM_SKIP_MD5=1)"
else
	wget -q -U "$ua" -O "$out.md5" "$url.md5" || fail "could not download $url.md5 (set OSRM_SKIP_MD5=1 for mirrors without checksums)"
	expected=$(cut -d ' ' -f 1 "$out.md5")
	actual=$(md5sum "$out.partial" | cut -d ' ' -f 1)
	[ "$expected" = "$actual" ] || fail "MD5 mismatch (expected $expected, got $actual); a new extract may have been published meanwhile, run again"
	rm -f "$out.md5"
	log "MD5 verified"
fi

size=$(wc -c <"$out.partial" | tr -d ' ')
[ "$size" -gt 50000000 ] || fail "file is only $size bytes; expected a full country extract"
mv "$out.partial" "$out"
log "saved $out ($size bytes)"
