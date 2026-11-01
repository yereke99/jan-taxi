<!--
  Map of the village commission editor. Saved villages are gray (circle or
  boundary, with a dot at the centre); the village being edited is black: its
  centre is a draggable pin, a circle has a draggable point on its edge that
  sets the radius, a boundary is drawn and edited like a service zone (click
  to add a vertex, click the first vertex to close, drag a vertex, right-click
  to delete it). Without a centre a click sets it; in the check mode a click
  only reports the point.
-->
<script setup lang="ts">
import { Marker, type GeoJSONSource, type MapLayerMouseEvent, type MapMouseEvent, type Map as MapLibreMap } from 'maplibre-gl'
import { onBeforeUnmount, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LatLon } from '../../api/common'
import type { CommissionArea, CommissionAreaShape } from '../../api/config'
import { circleBounds, geodesicRing } from '../../maps/circle'
import { featureCollection, type Feature, type FeatureCollection } from '../../maps/geojson'
import MapView from '../../maps/MapView.vue'
import { pinMarkerElement, stopMarkerElement, vertexMarkerElement } from '../../maps/markers'
import { CLOSE_TOLERANCE_PX, MIN_RING_VERTICES, draftClick, fromGeometry, polygonsBounds, screenDistance, type PolygonRings, type Position } from '../../maps/polygon'
import { bearingDegrees, destinationPoint, distanceMeters } from '../../utils/geo'
import { DEFAULT_RADIUS_M, dragRadius } from './commissionAreaModel'

const props = defineProps<{
  /** Saved villages shown for context (the edited one excluded). */
  others: CommissionArea[]
  /** Shape of the village being edited; null while nothing is edited. */
  shape: CommissionAreaShape | null
  /** Centre of the edited village. */
  center: LatLon | null
  /** Radius of the edited circle, metres. */
  radius: number | null
  /** Boundary of the edited village. */
  polygons: PolygonRings[]
  /** Ring being drawn (null when not drawing). */
  draft: Position[] | null
  /** A click reports the point (pick) instead of editing. */
  checking: boolean
  /** Point last checked. */
  checkPoint: LatLon | null
}>()

const emit = defineEmits<{
  'update:center': [center: LatLon]
  'update:radius': [radius: number]
  'update:polygons': [polygons: PolygonRings[]]
  'update:draft': [draft: Position[] | null]
  /** The draft ring was closed into a new polygon. */
  closed: [ring: Position[]]
  pick: [point: LatLon]
}>()

type Bounds = [[number, number], [number, number]]

const { t } = useI18n()
const map = shallowRef<MapLibreMap | null>(null)
let drag: { p: number; r: number; v: number } | null = null
let centerMarker: Marker | null = null
let radiusMarker: Marker | null = null
let checkMarker: Marker | null = null
let draggingCenter = false
let draggingRadius = false
/** Direction of the radius point from the centre (degrees from north); follows the last drag. */
let radiusBearing = 90

function toLatLon(p: { lng: number; lat: number }): LatLon {
  return { lat: Number(p.lat.toFixed(6)), lon: Number(p.lng.toFixed(6)) }
}

function polygonFeature(rings: PolygonRings, properties: Record<string, unknown>): Feature {
  const coords = rings.map((r) => [...r.map((p): [number, number] => [p[0], p[1]]), [r[0]![0], r[0]![1]] as [number, number]])
  return { type: 'Feature', properties, geometry: { type: 'Polygon', coordinates: coords } }
}

function circleFeature(center: LatLon, radius: number, properties: Record<string, unknown>): Feature {
  return { type: 'Feature', properties, geometry: { type: 'Polygon', coordinates: [geodesicRing(center, radius)] } }
}

function drawable(polygons: readonly PolygonRings[]): PolygonRings[] {
  return polygons.filter((rings) => (rings[0]?.length ?? 0) >= MIN_RING_VERTICES)
}

function othersData(): FeatureCollection {
  const out: Feature[] = []
  for (const a of props.others) {
    const properties = { active: a.is_active }
    if (a.shape === 'circle' && a.radius_m !== null) out.push(circleFeature(a.center, a.radius_m, properties))
    else for (const rings of drawable(fromGeometry(a.boundary))) out.push(polygonFeature(rings, properties))
  }
  return featureCollection(out)
}

function centersData(): FeatureCollection {
  return featureCollection(
    props.others.map((a): Feature => ({ type: 'Feature', properties: { active: a.is_active }, geometry: { type: 'Point', coordinates: [a.center.lon, a.center.lat] } })),
  )
}

function editData(): FeatureCollection {
  if (props.shape === 'circle' && props.center && props.radius !== null) return featureCollection([circleFeature(props.center, props.radius, {})])
  if (props.shape === 'polygon') return featureCollection(drawable(props.polygons).map((rings, i) => polygonFeature(rings, { i })))
  return featureCollection()
}

function vertexData(): FeatureCollection {
  const out: Feature[] = []
  if (props.shape === 'polygon') {
    props.polygons.forEach((rings, p) =>
      rings.forEach((ring, r) => ring.forEach((pt, v) => out.push({ type: 'Feature', properties: { p, r, v }, geometry: { type: 'Point', coordinates: [pt[0], pt[1]] } }))),
    )
  }
  return featureCollection(out)
}

function draftData(): FeatureCollection {
  const d = props.shape === 'polygon' ? (props.draft ?? []) : []
  const out: Feature[] = d.map((pt, i) => ({ type: 'Feature', properties: { first: i === 0 }, geometry: { type: 'Point', coordinates: [pt[0], pt[1]] } }))
  if (d.length > 1) out.push({ type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: d.map((p): [number, number] => [p[0], p[1]]) } })
  return featureCollection(out)
}

function setData(id: string, data: FeatureCollection): void {
  ;(map.value?.getSource(id) as GeoJSONSource | undefined)?.setData(data)
}

function onRadiusDrag(): void {
  const c = props.center
  if (!c || !radiusMarker) return
  const ll = radiusMarker.getLngLat()
  const p = { lat: ll.lat, lon: ll.lng }
  radiusBearing = bearingDegrees(c, p)
  emit('update:radius', dragRadius(distanceMeters(c, p)))
}

/** Centre pin, radius point and the checked point, created and removed as the props change. */
function placeMarkers(): void {
  const m = map.value
  if (!m) return
  const c = props.shape ? props.center : null
  if (!c) {
    centerMarker?.remove()
    centerMarker = null
  } else if (!centerMarker) {
    const el = pinMarkerElement()
    el.title = t('commission_areas.center')
    const mk = new Marker({ element: el, anchor: 'bottom', draggable: true }).setLngLat([c.lon, c.lat]).addTo(m)
    mk.on('dragstart', () => (draggingCenter = true))
    mk.on('drag', () => emit('update:center', toLatLon(mk.getLngLat())))
    mk.on('dragend', () => {
      draggingCenter = false
      emit('update:center', toLatLon(mk.getLngLat()))
    })
    centerMarker = mk
  } else if (!draggingCenter) centerMarker.setLngLat([c.lon, c.lat])

  const r = props.shape === 'circle' && c ? props.radius : null
  if (!c || r === null) {
    radiusMarker?.remove()
    radiusMarker = null
  } else {
    const edge = destinationPoint(c, r, radiusBearing)
    if (!radiusMarker) {
      const el = vertexMarkerElement(false)
      el.title = t('commission_areas.radius')
      const mk = new Marker({ element: el, draggable: true }).setLngLat([edge.lon, edge.lat]).addTo(m)
      mk.on('dragstart', () => (draggingRadius = true))
      mk.on('drag', onRadiusDrag)
      mk.on('dragend', () => {
        draggingRadius = false
        onRadiusDrag()
        placeMarkers()
      })
      radiusMarker = mk
    } else if (!draggingRadius) radiusMarker.setLngLat([edge.lon, edge.lat])
  }

  const q = props.checkPoint
  if (!q) {
    checkMarker?.remove()
    checkMarker = null
  } else if (!checkMarker) checkMarker = new Marker({ element: stopMarkerElement('A', t('commission_areas.check_point')) }).setLngLat([q.lon, q.lat]).addTo(m)
  else checkMarker.setLngLat([q.lon, q.lat])
}

function redraw(): void {
  setData('areas-other', othersData())
  setData('areas-other-centers', centersData())
  setData('area-edit', editData())
  setData('area-vertices', vertexData())
  setData('area-draft', draftData())
  placeMarkers()
  const c = map.value?.getCanvas()
  if (c) c.style.cursor = props.checking || (props.shape === 'polygon' && props.draft) || (props.shape && !props.center) ? 'crosshair' : ''
}

function onMapClick(e: MapMouseEvent): void {
  const m = map.value
  if (!m) return
  const point = toLatLon(e.lngLat)
  if (props.checking) {
    emit('pick', point)
    return
  }
  if (props.shape === 'polygon' && props.draft) {
    const first = props.draft[0]
    const nearFirst = first ? screenDistance(m.project([first[0], first[1]]), e.point) <= CLOSE_TOLERANCE_PX : false
    const res = draftClick(props.draft, [point.lon, point.lat], nearFirst)
    if (res.closed) {
      emit('closed', res.closed)
      emit('update:draft', null)
    } else emit('update:draft', res.vertices)
    return
  }
  if (props.shape && !props.center) emit('update:center', point)
}

function vertexAt(e: MapLayerMouseEvent): { p: number; r: number; v: number } | null {
  const f = e.features?.[0]
  if (!f) return null
  const { p, r, v } = f.properties as { p: number; r: number; v: number }
  return { p: Number(p), r: Number(r), v: Number(v) }
}

function onVertexDown(e: MapLayerMouseEvent): void {
  if (props.checking || props.draft || e.originalEvent.button !== 0) return
  const hit = vertexAt(e)
  if (!hit) return
  e.preventDefault()
  drag = hit
  map.value?.dragPan.disable()
}

function onMove(e: MapMouseEvent): void {
  if (!drag) return
  const next = props.polygons.map((rings) => rings.map((ring) => ring.map((p) => [p[0], p[1]] as Position)))
  const ring = next[drag.p]?.[drag.r]
  if (!ring) return
  ring[drag.v] = [Number(e.lngLat.lng.toFixed(6)), Number(e.lngLat.lat.toFixed(6))]
  emit('update:polygons', next)
}

function onUp(): void {
  if (!drag) return
  drag = null
  map.value?.dragPan.enable()
}

function onVertexContext(e: MapLayerMouseEvent): void {
  const hit = vertexAt(e)
  if (!hit || props.checking || props.draft) return
  e.preventDefault()
  const next = props.polygons.map((rings) => rings.map((ring) => ring.map((p) => [p[0], p[1]] as Position)))
  const ring = next[hit.p]?.[hit.r]
  if (!ring || ring.length <= MIN_RING_VERTICES) return
  ring.splice(hit.v, 1)
  emit('update:polygons', next)
}

function union(list: Array<Bounds | null>): Bounds | null {
  const all = list.filter((b): b is Bounds => b !== null)
  if (all.length === 0) return null
  return [
    [Math.min(...all.map((b) => b[0][0])), Math.min(...all.map((b) => b[0][1]))],
    [Math.max(...all.map((b) => b[1][0])), Math.max(...all.map((b) => b[1][1]))],
  ]
}

function editBounds(): Bounds | null {
  const c = props.shape ? props.center : null
  if (props.shape === 'circle' && c && props.radius !== null) return circleBounds(c, props.radius)
  return union([props.shape === 'polygon' ? polygonsBounds(props.polygons) : null, c ? circleBounds(c, DEFAULT_RADIUS_M) : null])
}

function othersBounds(): Bounds | null {
  return union(props.others.map((a) => (a.shape === 'circle' && a.radius_m !== null ? circleBounds(a.center, a.radius_m) : polygonsBounds(fromGeometry(a.boundary)))))
}

/** Frames the edited village, else every saved one; otherwise keeps the view. */
function fit(): void {
  const m = map.value
  if (!m) return
  const b = editBounds() ?? othersBounds()
  if (b) m.fitBounds(b, { padding: 48, maxZoom: 14, duration: 0 })
}

function onReady(m: MapLibreMap): void {
  map.value = m
  m.doubleClickZoom.disable()
  m.addSource('areas-other', { type: 'geojson', data: othersData() })
  m.addSource('areas-other-centers', { type: 'geojson', data: centersData() })
  m.addSource('area-edit', { type: 'geojson', data: editData() })
  m.addSource('area-vertices', { type: 'geojson', data: vertexData() })
  m.addSource('area-draft', { type: 'geojson', data: draftData() })
  m.addLayer({ id: 'areas-other-fill', type: 'fill', source: 'areas-other', paint: { 'fill-color': '#8E8E93', 'fill-opacity': ['case', ['get', 'active'], 0.12, 0.05] } })
  m.addLayer({ id: 'areas-other-line', type: 'line', source: 'areas-other', paint: { 'line-color': '#8E8E93', 'line-width': 1.5, 'line-dasharray': [2, 2] } })
  m.addLayer({
    id: 'areas-other-centers',
    type: 'circle',
    source: 'areas-other-centers',
    paint: { 'circle-radius': 3, 'circle-color': '#8E8E93', 'circle-stroke-color': '#FFFFFF', 'circle-stroke-width': 1 },
  })
  m.addLayer({ id: 'area-edit-fill', type: 'fill', source: 'area-edit', paint: { 'fill-color': '#000000', 'fill-opacity': 0.08 } })
  m.addLayer({ id: 'area-edit-line', type: 'line', source: 'area-edit', paint: { 'line-color': '#000000', 'line-width': 2 } })
  m.addLayer({
    id: 'area-draft-line',
    type: 'line',
    source: 'area-draft',
    filter: ['==', ['geometry-type'], 'LineString'],
    paint: { 'line-color': '#000000', 'line-width': 2, 'line-dasharray': [2, 1] },
  })
  m.addLayer({
    id: 'area-draft-points',
    type: 'circle',
    source: 'area-draft',
    filter: ['==', ['geometry-type'], 'Point'],
    paint: {
      'circle-radius': ['case', ['get', 'first'], 7, 4],
      'circle-color': ['case', ['get', 'first'], '#000000', '#FFFFFF'],
      'circle-stroke-color': ['case', ['get', 'first'], '#FFFFFF', '#000000'],
      'circle-stroke-width': 2,
    },
  })
  m.addLayer({
    id: 'area-vertices',
    type: 'circle',
    source: 'area-vertices',
    paint: { 'circle-radius': 5, 'circle-color': '#FFFFFF', 'circle-stroke-color': '#000000', 'circle-stroke-width': 2 },
  })
  m.on('click', onMapClick)
  m.on('mousedown', 'area-vertices', onVertexDown)
  m.on('contextmenu', 'area-vertices', onVertexContext)
  m.on('mousemove', onMove)
  m.on('mouseup', onUp)
  m.on('mouseenter', 'area-vertices', () => {
    if (!props.draft && !props.checking) m.getCanvas().style.cursor = 'move'
  })
  m.on('mouseleave', 'area-vertices', () => {
    if (!props.draft && !props.checking) m.getCanvas().style.cursor = ''
  })
  fit()
  redraw()
}

watch(() => [props.others, props.shape, props.center, props.radius, props.polygons, props.draft, props.checking, props.checkPoint], redraw)

onBeforeUnmount(() => {
  drag = null
  for (const mk of [centerMarker, radiusMarker, checkMarker]) mk?.remove()
  centerMarker = radiusMarker = checkMarker = null
})

defineExpose({ fit })
</script>

<template>
  <MapView height="560px" @ready="onReady" />
</template>
