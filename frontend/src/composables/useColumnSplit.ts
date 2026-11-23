import { computed, ref, type Ref } from 'vue'

// Drag-to-resize for the split between two side-by-side panels. share is the
// left column's percent of the width (0 = the layout's default); commit runs
// once a drag or key press settles, to persist it.
export function useColumnSplit(share: Ref<number>, commit: () => void, min = 25, max = 80) {
  const resizing = ref(false)
  let drag: { pointerId: number; left: number; width: number; max: number } | null = null
  const handleWidth = 14 // the middle grid track (style.css .panels)

  const clamp = (value: number) => Math.max(min, Math.min(max, Math.round(value)))

  function start(event: PointerEvent) {
    if (event.button !== 0) return
    event.preventDefault()
    const handle = event.currentTarget as HTMLElement
    const container = handle.parentElement
    const box = container?.getBoundingClientRect()
    if (!container || !box) return
    // Past this the right panel sits at its minimum width and stops shrinking:
    // cap the share there, so the handle never runs ahead of the panels.
    const minRight =
      parseFloat(getComputedStyle(container).getPropertyValue('--split-min-b')) || 240
    const tracks = box.width - handleWidth
    const maxByLayout = tracks > 0 ? ((tracks - minRight) / tracks) * 100 : max
    drag = {
      pointerId: event.pointerId,
      left: box.left,
      width: tracks,
      max: Math.min(max, maxByLayout),
    }
    resizing.value = true
    handle.setPointerCapture(event.pointerId)
  }

  function move(event: PointerEvent) {
    if (!drag || drag.pointerId !== event.pointerId) return
    const value = ((event.clientX - drag.left - handleWidth / 2) / drag.width) * 100
    share.value = clamp(Math.min(value, drag.max))
  }

  function stop(event: PointerEvent) {
    if (!drag || drag.pointerId !== event.pointerId) return
    drag = null
    resizing.value = false
    commit()
  }

  // The default split is 64/36 (1.28fr / 0.72fr) when nothing was chosen.
  function nudge(delta: number) {
    share.value = clamp((share.value || 64) + delta)
    commit()
  }

  function reset() {
    share.value = 0
    commit()
  }

  // CSS variables, so the narrow-screen single column rule still wins.
  const style = computed(() =>
    share.value ? { '--split-a': `${share.value}fr`, '--split-b': `${100 - share.value}fr` } : {},
  )
  return { resizing, style, start, move, stop, nudge, reset }
}
