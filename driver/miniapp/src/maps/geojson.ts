// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Minimal GeoJSON types for what the API returns (route geometry) and what the
// map layers consume. Declared locally so app code does not depend on a
// transitive @types package.

export type Position = [number, number] | [number, number, number]

export interface LineStringGeometry {
  type: 'LineString'
  coordinates: Position[]
}

export interface LineFeature {
  type: 'Feature'
  properties: Record<string, never>
  geometry: LineStringGeometry
}

export interface LineFeatureCollection {
  type: 'FeatureCollection'
  features: LineFeature[]
}

export function lineCollection(geometry: LineStringGeometry | null): LineFeatureCollection {
  const valid = geometry !== null && geometry.type === 'LineString' && geometry.coordinates.length >= 2
  return {
    type: 'FeatureCollection',
    features: valid ? [{ type: 'Feature', properties: {}, geometry }] : [],
  }
}

/** Straight two-point line, e.g. driver → pickup before a real route exists. */
export function straightLine(from: { lat: number; lon: number }, to: { lat: number; lon: number }): LineStringGeometry {
  return {
    type: 'LineString',
    coordinates: [
      [from.lon, from.lat],
      [to.lon, to.lat],
    ],
  }
}
