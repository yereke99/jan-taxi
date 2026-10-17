// Who the driver pays for a top-up (ARCHITECTURE §6.1): the Kaspi Pay seller
// ИП "JAN-TAXI" with its BIN, and the Kaspi Pay link. The link comes from the
// public config (the top-up's copy is the fallback), the name from the top-up;
// the BIN is read from the public config when the API provides it, else the
// seeded company BIN is shown.

import type { TopUp } from '../../api/types'
import { http } from '../../services/http'
import type { PublicConfig } from '../../services/config'

/** Seeded payee (migration 00006): shown until the API says otherwise. */
export const DEFAULT_PAYEE_NAME = 'ИП "JAN-TAXI"'
export const DEFAULT_PAYEE_BIN = '000809500289'

export interface Payee {
  name: string
  bin: string
  /** https Kaspi Pay link, or null when none is configured. */
  paymentUrl: string | null
}

const BIN_RE = /^\d{12}$/

function rec(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {}
}

/** A 12-digit BIN from any of the places the public config may carry it. */
export function binFromConfig(raw: unknown): string | null {
  const r = rec(raw)
  const kaspi = rec(r.kaspi)
  const company = rec(r.company)
  for (const v of [kaspi.recipient_bin, kaspi.bin, kaspi.seller_bin, company.bin]) {
    const s = typeof v === 'string' ? v.replace(/\s+/g, '') : ''
    if (BIN_RE.test(s)) return s
  }
  return null
}

function httpsOrNull(url: string | null | undefined): string | null {
  if (!url) return null
  try {
    return new URL(url).protocol === 'https:' ? url : null
  } catch {
    return null
  }
}

export function resolvePayee(config: PublicConfig, topup: Pick<TopUp, 'payment'> | null, bin: string | null): Payee {
  return {
    name: topup?.payment.recipient_name || config.kaspi?.recipient_name || DEFAULT_PAYEE_NAME,
    bin: bin ?? DEFAULT_PAYEE_BIN,
    // The public config carries the current link; the top-up keeps the one from its creation.
    paymentUrl: httpsOrNull(config.kaspi?.payment_url) ?? httpsOrNull(topup?.payment.url),
  }
}

let binRequest: Promise<string | null> | null = null

/** Reads the payee BIN from the public config once per app session. */
export function loadPayeeBin(): Promise<string | null> {
  binRequest ??= http
    .get<unknown>('/public/config', { query: { app: 'driver' }, timeoutMs: 8_000 })
    .then(binFromConfig)
    .catch(() => {
      binRequest = null
      return null
    })
  return binRequest
}

/** Groups a BIN for reading: 0008 0950 0289. */
export function formatBin(bin: string): string {
  return BIN_RE.test(bin) ? `${bin.slice(0, 4)} ${bin.slice(4, 8)} ${bin.slice(8)}` : bin
}
