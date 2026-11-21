import { computed, ref } from 'vue'

// Drag-to-resize for a panel's height, same handle as the copilot panels.
// The chosen height is remembered per panel (localStorage key).
export function usePanelResize(storageKey: string, minHeight = 180) {
  const height = ref(Number(localStorage.getItem(storageKey)) || 0)
  const resizing = ref(false)
  let drag: { pointerId: number; startY: number; startHeight: number } | null = null

  const clamp = (value: number) =>
    Math.max(minHeight, Math.min(Math.max(minHeight, window.innerHeight - 100), Math.round(value)))

  function start(event: PointerEvent) {
    if (event.button !== 0) return
    event.preventDefault()
    const handle = event.currentTarget as HTMLElement
    const startHeight = handle.parentElement?.getBoundingClientRect().height ?? 360
    drag = { pointerId: event.pointerId, startY: event.clientY, startHeight }
    resizing.value = true
    handle.setPointerCapture(event.pointerId)
  }

  function move(event: PointerEvent) {
    if (!drag || drag.pointerId !== event.pointerId) return
    height.value = clamp(drag.startHeight + event.clientY - drag.startY)
  }

  function stop(event: PointerEvent) {
    if (!drag || drag.pointerId !== event.pointerId) return
    drag = null
    resizing.value = false
    localStorage.setItem(storageKey, String(height.value))
  }

  function nudge(event: KeyboardEvent, delta: number) {
    const current =
      height.value ||
      (event.currentTarget as HTMLElement).parentElement?.getBoundingClientRect().height ||
      360
    height.value = clamp(current + delta)
    localStorage.setItem(storageKey, String(height.value))
  }

  const style = computed(() => (height.value ? { height: `${height.value}px` } : {}))
  return { height, resizing, style, start, move, stop, nudge }
}
