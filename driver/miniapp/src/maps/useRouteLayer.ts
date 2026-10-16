// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Route line (white 8px casing under a black 5px line) and the driver's
// approach line (dashed gray), per §15. Layers are (re)installed whenever the
// style (re)loads, so a style switch keeps the current route.

import type { GeoJSONSource, Map as MapLibreMap } from 'maplibre-gl'
import { onScopeDispose, watch } from 'vue'
import { ROUTE_STYLE } from './constants'
import type { MapRef } from './context'
import { lineCollection, type LineStringGeometry } from './geojson'

const ROUTE_SOURCE = 'jt-route'
const APPROACH_SOURCE = 'jt-approach'
const LAYERS = {
  approach: 'jt-approach-line',
  casing: 'jt-route-casing',
  line: 'jt-route-line',
} as const

export interface RouteLayer {
  /** Shows the trip route (null clears it). */
  setRoute(geometry: LineStringGeometry | null): void
  /** Shows the driver → pickup approach (null clears it). */
  setApproach(geometry: LineStringGeometry | null): void
  clear(): void
}

/** First label layer, so the route draws above roads but under street names. */
function firstSymbolLayerId(m: MapLibreMap): string | undefined {
  return m.getStyle().layers.find((l) => l.type === 'symbol')?.id
}

export function useRouteLayer(map: MapRef): RouteLayer {
  let route: LineStringGeometry | null = null
  let approach: LineStringGeometry | null = null

  function install(m: MapLibreMap): boolean {
    try {
      const before = firstSymbolLayerId(m)
      if (!m.getSource(APPROACH_SOURCE)) m.addSource(APPROACH_SOURCE, { type: 'geojson', data: lineCollection(approach) })
      if (!m.getSource(ROUTE_SOURCE)) m.addSource(ROUTE_SOURCE, { type: 'geojson', data: lineCollection(route) })
      if (!m.getLayer(LAYERS.approach)) {
        m.addLayer(
          {
            id: LAYERS.approach,
            type: 'line',
            source: APPROACH_SOURCE,
            layout: { 'line-join': 'round', 'line-cap': 'butt' },
            paint: {
              'line-color': ROUTE_STYLE.approachColor,
              'line-width': ROUTE_STYLE.approachWidth,
              'line-dasharray': ROUTE_STYLE.approachDash,
            },
          },
          before,
        )
      }
      if (!m.getLayer(LAYERS.casing)) {
        m.addLayer(
          {
            id: LAYERS.casing,
            type: 'line',
            source: ROUTE_SOURCE,
            layout: { 'line-join': 'round', 'line-cap': 'round' },
            paint: { 'line-color': ROUTE_STYLE.casingColor, 'line-width': ROUTE_STYLE.casingWidth },
          },
          before,
        )
      }
      if (!m.getLayer(LAYERS.line)) {
        m.addLayer(
          {
            id: LAYERS.line,
            type: 'line',
            source: ROUTE_SOURCE,
            layout: { 'line-join': 'round', 'line-cap': 'round' },
            paint: { 'line-color': ROUTE_STYLE.lineColor, 'line-width': ROUTE_STYLE.lineWidth },
          },
          before,
        )
      }
      return true
    } catch {
      // Style not loaded yet; the next styledata event retries.
      return false
    }
  }

  function push(sourceId: string, geometry: LineStringGeometry | null): void {
    const m = map.value
    if (!m) return
    const src = m.getSource(sourceId) as GeoJSONSource | undefined
    if (src) void src.setData(lineCollection(geometry))
    else install(m)
  }

  const stop = watch(
    map,
    (m, _old, onCleanup) => {
      if (!m) return
      const onStyleData = (): void => {
        if (!m.getSource(ROUTE_SOURCE) || !m.getLayer(LAYERS.line)) install(m)
      }
      install(m)
      m.on('styledata', onStyleData)
      onCleanup(() => m.off('styledata', onStyleData))
    },
    { immediate: true },
  )

  onScopeDispose(() => {
    stop()
    const m = map.value
    if (!m) return
    try {
      for (const id of [LAYERS.line, LAYERS.casing, LAYERS.approach]) if (m.getLayer(id)) m.removeLayer(id)
      for (const id of [ROUTE_SOURCE, APPROACH_SOURCE]) if (m.getSource(id)) m.removeSource(id)
    } catch {
      // The map may already be removed together with its style.
    }
  })

  return {
    setRoute(geometry) {
      route = geometry
      push(ROUTE_SOURCE, route)
    },
    setApproach(geometry) {
      approach = geometry
      push(APPROACH_SOURCE, approach)
    },
    clear() {
      route = null
      approach = null
      push(ROUTE_SOURCE, null)
      push(APPROACH_SOURCE, null)
    },
  }
}
