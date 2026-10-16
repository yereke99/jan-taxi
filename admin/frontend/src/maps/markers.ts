// DOM elements for map markers (monochrome; blue only for the driver GPS dot, §15).

export function stopMarkerElement(letter: string, label: string): HTMLElement {
  const el = document.createElement('div')
  el.className = 'jt-stop-marker'
  el.textContent = letter
  el.setAttribute('role', 'img')
  el.setAttribute('aria-label', label)
  el.title = label
  return el
}

export function driverMarkerElement(label: string, busy: boolean): HTMLElement {
  const el = document.createElement('div')
  el.className = busy ? 'jt-driver-marker is-busy' : 'jt-driver-marker'
  el.setAttribute('role', 'img')
  el.setAttribute('aria-label', label)
  el.title = label
  return el
}

export function vertexMarkerElement(first: boolean): HTMLElement {
  const el = document.createElement('div')
  el.className = first ? 'jt-vertex is-first' : 'jt-vertex'
  return el
}

export function pinMarkerElement(): HTMLElement {
  const el = document.createElement('div')
  el.className = 'jt-pin'
  return el
}
