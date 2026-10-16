// Public runtime config for the admin app (GET /public/config?app=admin):
// map style URL, default city centre and the 2GIS base URL. Loaded once; defaults apply until then.

import { ref } from 'vue'
import { api } from '../services/api'
import type { LatLon } from '../api/common'
import { DEFAULT_MAP_CENTER, DEFAULT_MAP_STYLE_URL, DEFAULT_MAP_ZOOM } from '../maps/constants'

interface PublicConfig {
  map?: { style_url?: string }
  navigation?: { twogis_base_url?: string }
  city?: { center?: LatLon; zoom?: number } | null
}

const styleUrl = ref(DEFAULT_MAP_STYLE_URL)
const center = ref<LatLon>({ ...DEFAULT_MAP_CENTER })
const zoom = ref(DEFAULT_MAP_ZOOM)
const twoGisBaseUrl = ref('https://2gis.kz')
let loading: Promise<void> | null = null

export function loadPublicConfig(): Promise<void> {
  loading ??= api
    .get<PublicConfig>('/public/config', { query: { app: 'admin' } })
    .then((cfg) => {
      if (cfg.map?.style_url) styleUrl.value = cfg.map.style_url
      if (cfg.navigation?.twogis_base_url) twoGisBaseUrl.value = cfg.navigation.twogis_base_url
      if (cfg.city?.center) center.value = cfg.city.center
      if (cfg.city?.zoom) zoom.value = cfg.city.zoom
    })
    .catch((err: unknown) => {
      // The default style keeps maps working.
      console.warn('[config] public config failed', err)
      loading = null
    })
  return loading
}

export function usePublicConfig() {
  void loadPublicConfig()
  return { styleUrl, twoGisBaseUrl, center, zoom }
}
