// MapLibre GL JS 6 is ESM-only and needs its worker URL set once when bundled;
// without it the map mounts but never draws tiles.

import { setWorkerUrl } from 'maplibre-gl'
import 'maplibre-gl/dist/maplibre-gl.css'
import workerUrl from 'maplibre-gl/dist/maplibre-gl-worker.mjs?worker&url'

let configured = false

export function setupMapLibre(): void {
  if (configured) return
  setWorkerUrl(workerUrl)
  configured = true
}
