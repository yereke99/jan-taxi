import { afterEach, describe, expect, it, vi } from 'vitest'
import { setHttpTransport } from '../services/http'
import { getRegistration } from './driver'

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

  it('keeps protected own identity, verification status and PDF metadata through normalization', async () => {
    setHttpTransport(vi.fn(async () => json(200, {
      id:'d1',status:'pending',identity_enabled:true,
      identity:{document_type:'driving_licence',iin:'999999999990',document_number:'AA000012',patronymic:''},
      verification:{id:'j1',status:'admin_review',confidence:62,reasons:['iin_uncertain'],attempts:1,created_at:'now',finished_at:null},
      documents:{id_front:{id:'f1',kind:'id_front',url:'/api/v1/driver/files/f1',width:0,height:0,mime:'application/pdf'}},
    })))
    const r=await getRegistration()
    expect(r.identity_enabled).toBe(true)
    expect(r.identity?.document_number).toBe('AA000012')
    expect(r.verification?.status).toBe('admin_review')
    expect(r.documents.id_front?.mime).toBe('application/pdf')
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
