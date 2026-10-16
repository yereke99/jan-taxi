#!/bin/bash
# Builds the OSRM multi-level Dijkstra (MLD) dataset for Kazakhstan from the
# extract downloaded by download.sh. Runs in the osrm-prepare service (OSRM
# image, no network). Output is built in /data/build and moved into place only
# after all three steps succeed; restart the osrm service afterwards:
#   docker compose --profile osrm restart osrm
#
# Peak memory for the Kazakhstan extract is a few GB (osrm-extract); the
# service limit is OSRM_PREPARE_MEM_LIMIT (default 6g).
set -euo pipefail

profile=${OSRM_PROFILE:-/opt/car.lua}
data=/data
name=kazakhstan-latest
build="$data/build"

log() { printf '%s osrm-prepare: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

[[ -f "$data/$name.osm.pbf" ]] || { log "ERROR: $data/$name.osm.pbf is missing (run osrm-download first)"; exit 1; }
[[ -f "$profile" ]] || { log "ERROR: routing profile $profile not found"; exit 1; }

rm -rf "$build"
mkdir -p "$build"
ln -s "../$name.osm.pbf" "$build/$name.osm.pbf"

log "osrm-extract with $profile"
osrm-extract -p "$profile" "$build/$name.osm.pbf"
log "osrm-partition"
osrm-partition "$build/$name.osrm"
log "osrm-customize"
osrm-customize "$build/$name.osrm"

rm -f "$build/$name.osm.pbf"
find "$data" -maxdepth 1 -name "$name.osrm*" -delete
mv "$build"/"$name".osrm* "$data"/
rmdir "$build"
log "dataset ready: $data/$name.osrm ($(du -sh "$data" | cut -f1) in the volume)"
