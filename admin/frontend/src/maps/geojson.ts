// Minimal GeoJSON shapes for map sources (the GeoJSON ambient types used by
// maplibre-gl's declarations are not part of this project's type roots).

export type Coordinates = [number, number]

export type Geometry =
  | { type: 'Point'; coordinates: Coordinates }
  | { type: 'LineString'; coordinates: Coordinates[] }
  | { type: 'Polygon'; coordinates: Coordinates[][] }
  | { type: 'MultiPolygon'; coordinates: Coordinates[][][] }

export interface Feature {
  type: 'Feature'
  id?: string | number
  properties: Record<string, unknown>
  geometry: Geometry
}

export interface FeatureCollection {
  type: 'FeatureCollection'
  features: Feature[]
}

export function featureCollection(features: Feature[] = []): FeatureCollection {
  return { type: 'FeatureCollection', features }
}
