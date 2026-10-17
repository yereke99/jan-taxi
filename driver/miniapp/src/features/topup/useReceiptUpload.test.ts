import { effectScope } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { TopUp } from '../../api/types'
import { ApiError } from '../../services/http'
import { ReceiptFileError, useReceiptUpload, type ReceiptUploadDeps } from './useReceiptUpload'

vi.mock('../media/image', () => ({
  ImageError: class ImageError extends Error {
    constructor(readonly code: string) {
      super(code)
    }
  },
  prepareImage: vi.fn(async () => new Blob(['jpeg'], { type: 'image/jpeg' })),
}))

function topup(over: Partial<TopUp> = {}): TopUp {
  return { id: 't1', status: 'created', receipt: null, ...over } as TopUp
}

function setup(deps: Partial<ReceiptUploadDeps> = {}) {
  const upload = vi.fn<ReceiptUploadDeps['upload']>(async (_b, _n, onProgress) => {
    onProgress(0.5)
    onProgress(1)
    return topup({ status: 'confirmed' })
  })
  const submit = vi.fn<ReceiptUploadDeps['submit']>(async (t) => ({ ...t, status: 'pending_verification' }))
  const scope = effectScope()
  const task = scope.run(() => useReceiptUpload({ upload: deps.upload ?? upload, submit: deps.submit ?? submit }))!
  return { task, upload, submit, scope }
}

const pdf = (name = 'receipt_QR18149336524.pdf', body = '%PDF-1.7 test') => new File([body], name, { type: 'application/pdf' })

describe('useReceiptUpload', () => {
  it('sends a PDF as it is, with its own name, and reports progress', async () => {
    const { task, upload, submit } = setup()
    const states: string[] = []
    const res = await task.send(pdf())
    states.push(task.state.value)
    expect(res?.status).toBe('confirmed')
    expect(upload).toHaveBeenCalledTimes(1)
    expect(upload.mock.calls[0]![1]).toBe('receipt_QR18149336524.pdf')
    expect(submit).not.toHaveBeenCalled()
    expect(task.progress.value).toBe(1)
    expect(states).toEqual(['done'])
  })

  it('refuses a file that is not a PDF before uploading', async () => {
    const { task, upload } = setup()
    expect(await task.send(pdf('receipt.pdf', '<html>not a pdf'))).toBeNull()
    expect(task.state.value).toBe('error')
    expect(task.error.value).toBeInstanceOf(ReceiptFileError)
    expect((task.error.value as ReceiptFileError).code).toBe('not_pdf')
    await task.send(new File(['x'], 'a.docx', { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }))
    expect((task.error.value as ReceiptFileError).code).toBe('not_pdf')
    expect(upload).not.toHaveBeenCalled()
  })

  it('refuses a PDF over 5 MiB', async () => {
    const { task, upload } = setup()
    const big = new File(['%PDF-' + 'x'.repeat(5 * 1024 * 1024)], 'big.pdf', { type: 'application/pdf' })
    await task.send(big)
    expect((task.error.value as ReceiptFileError).code).toBe('too_large')
    expect(upload).not.toHaveBeenCalled()
  })

  it('attaches a screenshot as evidence and sends the top-up for review', async () => {
    const upload = vi.fn<ReceiptUploadDeps['upload']>(async () => topup({ status: 'created' }))
    const { task, submit } = setup({ upload })
    const res = await task.send(new File(['png'], 'shot.png', { type: 'image/png' }))
    expect(upload.mock.calls[0]![1]).toBe('receipt.jpg')
    expect(submit).toHaveBeenCalledTimes(1)
    expect(res?.status).toBe('pending_verification')
  })

  it('keeps the API error and retries the same file', async () => {
    const upload = vi
      .fn<ReceiptUploadDeps['upload']>()
      .mockRejectedValueOnce(new ApiError(0, 'network_error', 'down'))
      .mockResolvedValueOnce(topup({ status: 'pending_verification' }))
    const { task } = setup({ upload })
    expect(await task.send(pdf())).toBeNull()
    expect(task.state.value).toBe('error')
    expect((task.error.value as ApiError).code).toBe('network_error')
    const res = await task.retry()
    expect(res?.status).toBe('pending_verification')
    expect(task.state.value).toBe('done')
    expect(upload).toHaveBeenCalledTimes(2)
  })
})
