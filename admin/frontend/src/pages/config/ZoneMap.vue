<!--
  Map surface of the zone editor. Shows the city's other zones in gray, the
  zone being edited in black with draggable vertex handles, and the ring
  being drawn. Click to add a vertex, click the first vertex to close the
  polygon, drag a handle to move it, right-click a handle to delete it.
-->
<script setup lang="ts">
import type { GeoJSONSource, MapLayerMouseEvent, MapMouseEvent, Map as MapLibreMap } from 'maplibre-gl'
import { onBeforeUnmount, shallowRef, watch } from 'vue'
import type { LatLon } from '../../api/common'
import type { Zone } from '../../api/config'
import { featureCollection, type Feature, type FeatureCollection } from '../../maps/geojson'
import MapView from '../../maps/MapView.vue'
import { CLOSE_TOLERANCE_PX, MIN_RING_VERTICES, draftClick, fromGeometry, polygonsBounds, screenDistance, type PolygonRings, type Position } from '../../maps/polygon'

const props = defineProps<{
  center: LatLon
  zoom: number
  /** Zones of the city shown for context (the edited one excluded). */
  others: Zone[]
  /** Polygons of the zone being edited. */
  polygons: PolygonRings[]
  /** Ring being drawn (null when not drawing). */
  draft: Position[] | null
}>()

const emit = defineEmits<{
  'update:polygons': [polygons: PolygonRings[]]
  'update:draft': [draft: Position[] | null]
  /** The draft ring was closed into a new polygon. */
  closed: [ring: Position[]]
}>()

const map = shallowRef<MapLibreMap | null>(null)
let drag: { p: number; r: number; v: number } | null = null

function polygonFeature(rings: PolygonRings, props: Record<string, unknown>): Feature {
  const coords = rings.map((r) => [...r.map((p): [number, number] => [p[0], p[1]]), [r[0]![0], r[0]![1]] as [number, number]])
  return { type: 'Feature', properties: props, geometry: { type: 'Polygon', coordinates: coords } }
}

function othersData(): FeatureCollection {
  const out: Feature[] = []
  for (const z of props.others) for (const rings of fromGeometry(z.geometry)) out.push(polygonFeature(rings, { active: z.is_active }))
  return featureCollection(out)
}

function editData(): FeatureCollection {
  return featureCollection(props.polygons.filter((rings) => (rings[0]?.length ?? 0) >= MIN_RING_VERTICES).map((rings, i) => polygonFeature(rings, { i })))
}

function vertexData(): FeatureCollection {
  const out: Feature[] = []
  props.polygons.forEach((rings, p) =>
    rings.forEach((ring, r) => ring.forEach((pt, v) => out.push({ type: 'Feature', properties: { p, r, v }, geometry: { type: 'Point', coordinates: [pt[0], pt[1]] } }))),
  )
  return featureCollection(out)
}

function draftData(): FeatureCollection {
  const d = props.draft ?? []
  const out: Feature[] = d.map((pt, i) => ({ type: 'Feature', properties: { first: i === 0 }, geometry: { type: 'Point', coordinates: [pt[0], pt[1]] } }))
  if (d.length > 1) out.push({ type: 'Feature', properties: {}, geometry: { type: 'LineString', coordinates: d.map((p): [number, number] => [p[0], p[1]]) } })
  return featureCollection(out)
}

function setData(id: string, data: FeatureCollection): void {
  ;(map.value?.getSource(id) as GeoJSONSource | undefined)?.setData(data)
}

function redraw(): void {
  setData('zones-other', othersData())
  setData('zone-edit', editData())
  setData('zone-vertices', vertexData())
  setData('zone-draft', draftData())
  const c = map.value?.getCanvas()
  if (c) c.style.cursor = props.draft ? 'crosshair' : ''
}

function onMapClick(e: MapMouseEvent): void {
  const m = map.value
  if (!m || !props.draft) return
  const point: Position = [Number(e.lngLat.lng.toFixed(6)), Number(e.lngLat.lat.toFixed(6))]
  const first = props.draft[0]
  const nearFirst = first ? screenDistance(m.project([first[0], first[1]]), e.point) <= CLOSE_TOLERANCE_PX : false
  const res = draftClick(props.draft, point, nearFirst)
  if (res.closed) {
    emit('closed', res.closed)
    emit('update:draft', null)
  } else emit('update:draft', res.vertices)
}

function vertexAt(e: MapLayerMouseEvent): { p: number; r: number; v: number } | null {
  const f = e.features?.[0]
  if (!f) return null
  const { p, r, v } = f.properties as { p: number; r: number; v: number }
  return { p: Number(p), r: Number(r), v: Number(v) }
}

function onVertexDown(e: MapLayerMouseEvent): void {
  if (props.draft || e.originalEvent.button !== 0) return
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
  if (!hit || props.draft) return
  e.preventDefault()
  const next = props.polygons.map((rings) => rings.map((ring) => ring.map((p) => [p[0], p[1]] as Position)))
  const ring = next[hit.p]?.[hit.r]
  if (!ring || ring.length <= MIN_RING_VERTICES) return
  ring.splice(hit.v, 1)
  emit('update:polygons', next)
}

function onReady(m: MapLibreMap): void {
  map.value = m
  m.doubleClickZoom.disable()
  m.addSource('zones-other', { type: 'geojson', data: othersData() })
  m.addSource('zone-edit', { type: 'geojson', data: editData() })
  m.addSource('zone-vertices', { type: 'geojson', data: vertexData() })
  m.addSource('zone-draft', { type: 'geojson', data: draftData() })
  m.addLayer({ id: 'zones-other-fill', type: 'fill', source: 'zones-other', paint: { 'fill-color': '#8E8E93', 'fill-opacity': ['case', ['get', 'active'], 0.12, 0.05] } })
  m.addLayer({ id: 'zones-other-line', type: 'line', source: 'zones-other', paint: { 'line-color': '#8E8E93', 'line-width': 1.5, 'line-dasharray': [2, 2] } })
  m.addLayer({ id: 'zone-edit-fill', type: 'fill', source: 'zone-edit', paint: { 'fill-color': '#000000', 'fill-opacity': 0.08 } })
  m.addLayer({ id: 'zone-edit-line', type: 'line', source: 'zone-edit', paint: { 'line-color': '#000000', 'line-width': 2 } })
  m.addLayer({
    id: 'zone-draft-line',
    type: 'line',
    source: 'zone-draft',
    filter: ['==', ['geometry-type'], 'LineString'],
    paint: { 'line-color': '#000000', 'line-width': 2, 'line-dasharray': [2, 1] },
  })
  m.addLayer({
    id: 'zone-draft-points',
    type: 'circle',
    source: 'zone-draft',
    filter: ['==', ['geometry-type'], 'Point'],
    paint: {
      'circle-radius': ['case', ['get', 'first'], 7, 4],
      'circle-color': ['case', ['get', 'first'], '#000000', '#FFFFFF'],
      'circle-stroke-color': ['case', ['get', 'first'], '#FFFFFF', '#000000'],
      'circle-stroke-width': 2,
    },
  })
  m.addLayer({
    id: 'zone-vertices',
    type: 'circle',
    source: 'zone-vertices',
    paint: { 'circle-radius': 5, 'circle-color': '#FFFFFF', 'circle-stroke-color': '#000000', 'circle-stroke-width': 2 },
  })
  m.on('click', onMapClick)
  m.on('mousedown', 'zone-vertices', onVertexDown)
  m.on('contextmenu', 'zone-vertices', onVertexContext)
  m.on('mousemove', onMove)
  m.on('mouseup', onUp)
  m.on('mouseenter', 'zone-vertices', () => {
    if (!props.draft) m.getCanvas().style.cursor = 'move'
  })
  m.on('mouseleave', 'zone-vertices', () => {
    if (!props.draft) m.getCanvas().style.cursor = ''
  })
  fit()
  redraw()
}

/** Frames the edited polygons, else the other zones, else keeps the city centre. */
function fit(): void {
  const m = map.value
  if (!m) return
  const b = polygonsBounds(props.polygons) ?? polygonsBounds(props.others.flatMap((z) => fromGeometry(z.geometry)))
  if (b) m.fitBounds(b, { padding: 40, maxZoom: 15, duration: 0 })
}

watch(() => [props.others, props.polygons, props.draft], redraw, { deep: false })

onBeforeUnmount(() => {
  drag = null
})

defineExpose({ fit })
</script>

<template>
  <MapView :center="center" :zoom="zoom" height="560px" @ready="onReady" />
</template>
