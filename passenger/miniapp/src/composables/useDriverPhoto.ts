// The assigned driver's avatar: a private JPEG behind the bearer token
// (GET /orders/{id}/driver-photo), exposed as an object URL for <img>.

import { onBeforeUnmount, ref, watch, type Ref } from 'vue'
import { fetchDriverPhoto } from '../api/orders'

export function useDriverPhoto(orderId: Readonly<Ref<string | null>>, hasPhoto: Readonly<Ref<boolean>>): Readonly<Ref<string | null>> {
  const url = ref<string | null>(null)
  let controller: AbortController | null = null
  let loadedFor: string | null = null

  function release(): void {
    controller?.abort()
    controller = null
    if (url.value) URL.revokeObjectURL(url.value)
    url.value = null
    loadedFor = null
  }

  watch(
    () => [orderId.value, hasPhoto.value] as const,
    async ([id, has]) => {
      if (!id || !has) {
        release()
        return
      }
      if (loadedFor === id) return
      release()
      loadedFor = id
      const ctrl = new AbortController()
      controller = ctrl
      try {
        const blob = await fetchDriverPhoto(id, ctrl.signal)
        if (ctrl.signal.aborted) return
        url.value = URL.createObjectURL(blob)
      } catch {
        // No photo (404) or network: the card shows the initial instead.
        if (!ctrl.signal.aborted) loadedFor = null
      }
    },
    { immediate: true },
  )

  onBeforeUnmount(release)
  return url
}
