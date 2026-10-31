// Shared with the other Mini App — keep both copies identical (make verify-shared).
//
// Copy text to the clipboard from a tap. The async Clipboard API is tried
// first; older WebViews fall back to a temporary selection + execCommand.

export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // Permission denied or insecure context: use the fallback below.
  }
  const el = document.createElement('textarea')
  try {
    el.value = text
    el.setAttribute('readonly', '')
    el.style.position = 'fixed'
    el.style.top = '-1000px'
    el.style.opacity = '0'
    document.body.appendChild(el)
    el.select()
    el.setSelectionRange(0, text.length)
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    el.remove()
  }
}
