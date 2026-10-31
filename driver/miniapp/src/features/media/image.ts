// Client-side image preparation before upload: decode, downscale so the longest
// side is at most 1600 px and re-encode as JPEG. This keeps uploads small on
// mobile networks; the server still validates and re-encodes every image (§12).

export const MAX_IMAGE_SIDE = 1600
export const JPEG_QUALITY = 0.85
/** API upload limit (10 MiB), checked before sending anything. */
export const MAX_UPLOAD_BYTES = 10 * 1024 * 1024
/** The profile photo is capped lower (drivers.MaxAvatarBytes, 5 MiB). */
export const AVATAR_MAX_BYTES = 5 * 1024 * 1024

const ACCEPTED_ORIGINAL_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp'])

export class ImageError extends Error {
  override readonly name = 'ImageError'
  constructor(readonly code: 'unsupported' | 'too_large') {
    super(code)
  }
}

/** Target size keeping the aspect ratio; never upscales. */
export function fitWithin(width: number, height: number, maxSide: number = MAX_IMAGE_SIDE): { width: number; height: number } {
  const longest = Math.max(width, height)
  if (longest <= maxSide || longest <= 0) return { width, height }
  const k = maxSide / longest
  return { width: Math.max(1, Math.round(width * k)), height: Math.max(1, Math.round(height * k)) }
}

interface Decoded {
  source: CanvasImageSource
  width: number
  height: number
  release: () => void
}

async function decode(file: Blob): Promise<Decoded> {
  if (typeof createImageBitmap === 'function') {
    try {
      const bmp = await createImageBitmap(file, { imageOrientation: 'from-image' })
      return { source: bmp, width: bmp.width, height: bmp.height, release: () => bmp.close() }
    } catch {
      // Fall through to <img>, which some WebViews decode more formats with.
    }
  }
  const url = URL.createObjectURL(file)
  try {
    const img = new Image()
    img.decoding = 'async'
    await new Promise<void>((resolve, reject) => {
      img.onload = () => resolve()
      img.onerror = () => reject(new ImageError('unsupported'))
      img.src = url
    })
    return { source: img, width: img.naturalWidth, height: img.naturalHeight, release: () => URL.revokeObjectURL(url) }
  } catch (err) {
    URL.revokeObjectURL(url)
    throw err
  }
}

function toJpeg(canvas: HTMLCanvasElement, quality: number): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((b) => (b ? resolve(b) : reject(new ImageError('unsupported'))), 'image/jpeg', quality)
  })
}

/**
 * Returns a JPEG no larger than MAX_IMAGE_SIDE on its longest side and
 * maxBytes in size. When the device cannot decode the file but it is already
 * an accepted type within the limit, the original is returned for the server
 * to process.
 */
export async function prepareImage(file: Blob, maxSide: number = MAX_IMAGE_SIDE, maxBytes: number = MAX_UPLOAD_BYTES): Promise<Blob> {
  let decoded: Decoded
  try {
    decoded = await decode(file)
  } catch {
    if (ACCEPTED_ORIGINAL_TYPES.has(file.type) && file.size <= maxBytes) return file
    throw new ImageError(ACCEPTED_ORIGINAL_TYPES.has(file.type) ? 'too_large' : 'unsupported')
  }
  try {
    const { width, height } = fitWithin(decoded.width, decoded.height, maxSide)
    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height
    const ctx = canvas.getContext('2d')
    if (!ctx) throw new ImageError('unsupported')
    ctx.fillStyle = '#fff'
    ctx.fillRect(0, 0, width, height)
    ctx.drawImage(decoded.source, 0, 0, width, height)
    const blob = await toJpeg(canvas, JPEG_QUALITY)
    if (blob.size > maxBytes) throw new ImageError('too_large')
    return blob
  } finally {
    decoded.release()
  }
}
