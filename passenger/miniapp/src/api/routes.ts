// POST /routes/preview: route + fare quote for 2..max_points stops (§19).

import { http } from '../services/http'
import { parseQuote } from './parse'
import type { RoutePreviewRequest, RouteQuote } from './types'

export async function previewRoute(req: RoutePreviewRequest, signal?: AbortSignal): Promise<RouteQuote> {
  return parseQuote(await http.post<unknown>('/routes/preview', req, { signal }))
}
