import { describe, expect, it } from 'vitest'
import { normalizeReceipt, normalizeTopUp } from '../../api/normalize'
import { ApiError } from '../../services/http'
import { binFromConfig, DEFAULT_PAYEE_BIN, formatBin, resolvePayee } from './payee'
import { defaultPublicConfig } from '../../services/config'
import { hasPdfHeader, knownReasons, receiptFileKind, receiptOutcome, receiptUploadError, receiptUploadName } from './receipt'

describe('receipt file kind', () => {
  it('recognises the Kaspi PDF by type or name', () => {
    expect(receiptFileKind({ type: 'application/pdf', name: 'x' })).toBe('pdf')
    expect(receiptFileKind({ type: '', name: 'receipt_QR18149336524.PDF' })).toBe('pdf')
    // Android pickers may report neither a type nor an extension.
    expect(receiptFileKind({ type: '', name: 'download' })).toBe('pdf')
  })

  it('accepts screenshots as evidence and refuses anything else', () => {
    expect(receiptFileKind({ type: 'image/jpeg', name: 'a.jpg' })).toBe('image')
    expect(receiptFileKind({ type: '', name: 'shot.PNG' })).toBe('image')
    expect(receiptFileKind({ type: 'application/msword', name: 'a.doc' })).toBe('unsupported')
    expect(receiptFileKind({ type: 'image/heic', name: 'a.heic' })).toBe('unsupported')
  })

  it('checks the %PDF- header', async () => {
    expect(await hasPdfHeader(new Blob(['%PDF-1.7\n...']))).toBe(true)
    expect(await hasPdfHeader(new Blob(['<html>']))).toBe(false)
    expect(await hasPdfHeader(new Blob([]))).toBe(false)
  })
})

describe('receipt upload name', () => {
  it('keeps the Kaspi name that carries the receipt number', () => {
    expect(receiptUploadName('receipt_QR18149336524.pdf')).toBe('receipt_QR18149336524.pdf')
  })

  it('reduces other names to safe characters with a .pdf extension', () => {
    expect(receiptUploadName('Чек Kaspi (1).pdf')).toBe('Kaspi_1_.pdf')
    expect(receiptUploadName('/storage/emulated/0/Download/receipt')).toBe('receipt.pdf')
    expect(receiptUploadName('')).toBe('receipt.pdf')
    expect(receiptUploadName(undefined)).toBe('receipt.pdf')
    expect(receiptUploadName('.pdf')).toBe('receipt.pdf')
    expect(receiptUploadName('a'.repeat(300) + '.pdf').length).toBeLessThanOrEqual(100)
  })
})

describe('receipt outcome', () => {
  const pdf = (decision: 'auto_confirmed' | 'manual_review' | null, reasons: string[] = []) => ({
    kind: 'pdf' as const,
    decision,
    reasons,
    receipt_serial: 'QR18149336524',
    amount: 1000,
    paid_at: '2026-10-17T10:00:00Z',
  })

  it('tells an automatic credit from an administrator confirmation', () => {
    expect(receiptOutcome({ status: 'confirmed', receipt: pdf('auto_confirmed'), rejected_reason: null })).toBe('credited')
    expect(receiptOutcome({ status: 'confirmed', receipt: null, rejected_reason: null })).toBe('confirmed')
    expect(receiptOutcome({ status: 'confirmed', receipt: pdf('manual_review', ['amount_mismatch']), rejected_reason: null })).toBe('confirmed')
  })

  it('shows reviews, duplicates and closed requests', () => {
    expect(receiptOutcome({ status: 'pending_verification', receipt: pdf('manual_review', ['receipt_too_old']), rejected_reason: null })).toBe('review')
    expect(receiptOutcome({ status: 'rejected', receipt: null, rejected_reason: 'duplicate_receipt' })).toBe('duplicate')
    expect(
      receiptOutcome({ status: 'rejected', receipt: { ...pdf(null, ['duplicate_receipt']) }, rejected_reason: null }),
    ).toBe('duplicate')
    expect(receiptOutcome({ status: 'rejected', receipt: null, rejected_reason: 'wrong amount' })).toBe('rejected')
    expect(receiptOutcome({ status: 'expired', receipt: null, rejected_reason: null })).toBe('expired')
    expect(receiptOutcome({ status: 'created', receipt: null, rejected_reason: null })).toBe('waiting')
  })

  it('keeps only the reasons the app can explain, once each', () => {
    expect(knownReasons(['amount_mismatch', 'producer_unexpected', 'amount_mismatch', 'image_receipt'])).toEqual(['amount_mismatch', 'image_receipt'])
    expect(knownReasons(undefined)).toEqual([])
  })
})

describe('receipt upload errors', () => {
  it('maps the API codes', () => {
    expect(receiptUploadError(new ApiError(409, 'receipt_already_used', ''))).toBe('duplicate')
    expect(receiptUploadError(new ApiError(422, 'unsupported_file_type', ''))).toBe('not_pdf')
    expect(receiptUploadError(new ApiError(415, 'unsupported_media_type', ''))).toBe('not_pdf')
    expect(receiptUploadError(new ApiError(413, 'file_too_large', ''))).toBe('too_large')
    expect(receiptUploadError(new ApiError(422, 'pdf_unreadable', ''))).toBe('unreadable')
    expect(receiptUploadError(new ApiError(422, 'pdf_not_allowed', ''))).toBe('not_allowed')
    expect(receiptUploadError(new ApiError(409, 'invalid_transition', ''))).toBe('closed')
    expect(receiptUploadError(new ApiError(429, 'too_many_requests', ''))).toBe('rate_limited')
    expect(receiptUploadError(new ApiError(0, 'network_error', ''))).toBe('network')
    expect(receiptUploadError(new ApiError(0, 'timeout', ''))).toBe('network')
    expect(receiptUploadError(new ApiError(500, 'internal', ''))).toBe('other')
    expect(receiptUploadError(new Error('x'))).toBe('other')
  })
})

describe('receipt payload', () => {
  it('normalises the receipt check of a top-up', () => {
    expect(normalizeReceipt(null)).toBeNull()
    expect(normalizeReceipt({ kind: 'zip' })).toBeNull()
    expect(normalizeReceipt({ kind: 'pdf', decision: 'auto_confirmed', reasons: ['a', 1, ''], amount: 500 })).toEqual({
      kind: 'pdf',
      decision: 'auto_confirmed',
      reasons: ['a'],
      receipt_serial: null,
      amount: 500,
      paid_at: null,
    })
    expect(normalizeTopUp({ id: 't1', status: 'created' }).receipt).toBeNull()
    expect(normalizeTopUp({ id: 't1', receipt: { kind: 'image', decision: null, reasons: ['image_receipt'] } }).receipt?.kind).toBe('image')
  })
})

describe('payee', () => {
  it('reads the BIN from the public config when present', () => {
    expect(binFromConfig({ kaspi: { recipient_bin: '0008 0950 0289' } })).toBe('000809500289')
    expect(binFromConfig({ company: { bin: '000809500289' } })).toBe('000809500289')
    expect(binFromConfig({ kaspi: { bin: '12345' } })).toBeNull()
    expect(binFromConfig(null)).toBeNull()
  })

  it('falls back to the seeded payee and accepts only https links', () => {
    const cfg = defaultPublicConfig()
    cfg.kaspi = { payment_url: 'https://pay.kaspi.kz/pay/xbp1imhd', recipient_name: 'ИП "JAN-TAXI"', instructions: null }
    const p = resolvePayee(cfg, null, null)
    expect(p).toEqual({ name: 'ИП "JAN-TAXI"', bin: DEFAULT_PAYEE_BIN, paymentUrl: 'https://pay.kaspi.kz/pay/xbp1imhd' })
    const payment = { method: 'kaspi_manual' as const, url: 'https://pay.kaspi.kz/pay/old', recipient_name: 'X', recipient_phone: null, instructions: '', reference: 'R', amount: 1, currency: 'KZT' as const }
    expect(resolvePayee(cfg, { payment }, '111111111111')).toEqual({ name: 'X', bin: '111111111111', paymentUrl: 'https://pay.kaspi.kz/pay/xbp1imhd' })
    cfg.kaspi = { payment_url: null, recipient_name: null, instructions: null }
    expect(resolvePayee(cfg, { payment }, null).paymentUrl).toBe('https://pay.kaspi.kz/pay/old')
    expect(resolvePayee(cfg, { payment: { ...payment, url: 'http://insecure.example' } }, null).paymentUrl).toBeNull()
  })

  it('groups the BIN for reading', () => {
    expect(formatBin('000809500289')).toBe('0008 0950 0289')
    expect(formatBin('abc')).toBe('abc')
  })
})
