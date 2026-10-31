import { afterEach, describe, expect, it, vi } from 'vitest'
import { setHttpTransport } from '../services/http'
import { getRegistration, saveProfileIin } from './driver'

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('getRegistration', () => {
  afterEach(() => setHttpTransport(null))

  it('reads the saved application', async () => {
    setHttpTransport(vi.fn(async () => json(200, { id: 'd1', status: 'changes_requested', editable: true, first_name: 'Ерлан' })))
    const r = await getRegistration()
    expect(r.id).toBe('d1')
    expect(r.status).toBe('changes_requested')
    expect(r.first_name).toBe('Ерлан')
  })

  it('reads the patronymic and the own IIN, leading zeros kept', async () => {
    setHttpTransport(vi.fn(async () => json(200, { id: 'd1', status: 'draft', editable: true, patronymic: 'Ахметұлы', iin: '000301500128' })))
    const r = await getRegistration()
    expect(r.patronymic).toBe('Ахметұлы')
    expect(r.iin).toBe('000301500128')
  })

  it('reads a missing or malformed IIN as none', async () => {
    for (const iin of [null, '', '1234', 900101300017]) {
      setHttpTransport(vi.fn(async () => json(200, { id: 'd1', status: 'draft', iin })))
      expect((await getRegistration()).iin).toBeNull()
    }
  })

  it('treats "no profile yet" as a new, editable application instead of an error', async () => {
    for (const res of [
      json(404, { error: { code: 'not_found', message: 'not found' } }),
      json(403, { error: { code: 'driver_not_registered', message: 'driver profile required' } }),
    ]) {
      setHttpTransport(vi.fn(async () => res))
      const r = await getRegistration()
      expect(r.id).toBeNull()
      expect(r.status).toBe('draft')
      expect(r.editable).toBe(true)
      expect(r.missing_fields).toContain('avatar')
    }
  })

  it('still reports other failures', async () => {
    setHttpTransport(vi.fn(async () => json(500, { error: { code: 'internal', message: 'boom' } })))
    await expect(getRegistration()).rejects.toMatchObject({ code: 'internal' })
  })
})

describe('saveProfileIin', () => {
  afterEach(() => setHttpTransport(null))

  it('sends the IIN once and reads back the masked one', async () => {
    const transport = vi.fn(async (_url: string, _init: RequestInit) =>
      json(200, { id: 'd1', code: 'EMB001', status: 'approved', first_name: 'Ерлан', last_name: 'Сапаров', iin_masked: '********0017', vehicle: null }),
    )
    setHttpTransport(transport as unknown as typeof fetch)
    const p = await saveProfileIin('900101300017')
    const [url, init] = transport.mock.calls[0]!
    expect(String(url)).toContain('/driver/profile/iin')
    expect(init.method).toBe('PUT')
    expect(JSON.parse(String(init.body))).toEqual({ iin: '900101300017' })
    expect(p.iin_masked).toBe('********0017')
    expect(p.patronymic).toBe('')
  })

  it('reports a different stored IIN as a conflict', async () => {
    setHttpTransport(vi.fn(async () => json(409, { error: { code: 'conflict', message: 'x', details: { reason: 'iin_already_set' } } })))
    await expect(saveProfileIin('900101300017')).rejects.toMatchObject({ code: 'conflict', details: { reason: 'iin_already_set' } })
  })
})
