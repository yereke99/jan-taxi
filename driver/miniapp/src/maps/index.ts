// Shared with the other Mini App — keep both copies identical (make verify-shared).

export { default as MapView } from './MapView.vue'
export { default as CenterPin } from './CenterPin.vue'
export type { PinCallout } from './CenterPin.vue'
export { setupMapLibre } from './setup'
export { MAP_KEY, useMap, type MapRef, type MapPadding } from './context'
export { useRouteLayer, type RouteLayer } from './useRouteLayer'
export { useStopMarkers, type StopMarkerPoint, type StopMarkers, type StopMarkersOptions } from './useStopMarkers'
export {
  createCarMarker,
  createUserLocationMarker,
  useCarMarker,
  useUserLocationMarker,
  metersPerPixel,
  type CarMarker,
  type CarPosition,
  type UserLocation,
  type UserLocationMarker,
} from './vehicleMarkers'
export { fitToPoints, computeFitPadding, paddingBeyondCamera, type FitOptions, type FitPadding } from './fitToPoints'
export { lineCollection, straightLine, type LineStringGeometry, type Position } from './geojson'
export * from './constants'
