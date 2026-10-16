// Shared with the other Mini App — keep both copies identical (make verify-shared).

import type { Map as MapLibreMap } from 'maplibre-gl'
import { inject, shallowRef, type InjectionKey, type ShallowRef } from 'vue'

/** Camera padding in px; the map's visual centre is the centre of the padded area. */
export interface MapPadding {
  top: number
  right: number
  bottom: number
  left: number
}

export type MapRef = Readonly<ShallowRef<MapLibreMap | null>>

/** Provided by MapView to its slot content (overlays, layers, markers). */
export const MAP_KEY: InjectionKey<MapRef> = Symbol('jt-map')

/** The nearest MapView's map (null until it is created, or outside a MapView). */
export function useMap(): MapRef {
  return inject(MAP_KEY, shallowRef<MapLibreMap | null>(null))
}
