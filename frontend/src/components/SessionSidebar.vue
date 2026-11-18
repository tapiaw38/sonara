<script setup lang="ts">
import { ref } from 'vue'
import type { SessionRow } from '../types'

defineProps<{
  assistantModeLabel: string
  startedAt: string
  sessions: SessionRow[]
  currentSessionId: string
  view: 'copilot' | 'meeting'
  meetingSessionId: string
  sessionMessage: string
}>()

const emit = defineEmits<{
  (event: 'new-session'): void
  (event: 'open-session', id: string): void
  (event: 'remove-session', id: string): void
  (event: 'refresh-sessions'): void
}>()

const sidebarWidth = defineModel<number>('sidebarWidth', { default: 225 })
const resizing = defineModel<boolean>('resizing', { default: false })

const sidebar = ref<HTMLElement | null>(null)
const sidebarDrag = ref<{ pointerId: number; startX: number; startWidth: number } | null>(null)
const showMobileSessions = ref(false)

function setSidebarWidth(width: number) {
  sidebarWidth.value = Math.max(160, Math.min(420, window.innerWidth - 420, Math.round(width)))
}

function startSidebarResize(event: PointerEvent) {
  if (event.button !== 0) return
  event.preventDefault()
  sidebarDrag.value = {
    pointerId: event.pointerId,
    startX: event.clientX,
    startWidth: sidebar.value?.getBoundingClientRect().width ?? sidebarWidth.value,
  }
  resizing.value = true
  const handle = event.currentTarget as HTMLElement
  handle.setPointerCapture(event.pointerId)
}

function resizeSidebar(event: PointerEvent) {
  const drag = sidebarDrag.value
  if (drag?.pointerId !== event.pointerId) return
  setSidebarWidth(drag.startWidth + event.clientX - drag.startX)
}

function stopSidebarResize(event: PointerEvent) {
  if (sidebarDrag.value?.pointerId !== event.pointerId) return
  sidebarDrag.value = null
  resizing.value = false
  localStorage.setItem('sonara-sidebar-width', String(sidebarWidth.value))
}

function nudgeSidebar(delta: number) {
  setSidebarWidth((sidebar.value?.getBoundingClientRect().width ?? sidebarWidth.value) + delta)
  localStorage.setItem('sonara-sidebar-width', String(sidebarWidth.value))
}

function newSession() {
  showMobileSessions.value = false
  emit('new-session')
}

function openSession(id: string) {
  showMobileSessions.value = false
  emit('open-session', id)
}

function sessionDate(row: SessionRow) {
  const when = new Date(row.startedAt)
  if (Number.isNaN(when.getTime())) return row.id
  return when.toLocaleString([], {
    day: '2-digit',
    month: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

// The title arrives a moment after the first exchange, so until then the date is
// the only thing that identifies a row.
function sessionLabel(row: SessionRow) {
  return row.title || sessionDate(row)
}
</script>

<template>
  <aside ref="sidebar" class="sidebar">
    <div class="section-label">SESIÓN</div>
    <div class="session-card active" aria-label="Sesión actual">
      <span class="session-icon">◉</span
      ><span
        ><strong>{{ assistantModeLabel }} actual</strong><small>Hoy · {{ startedAt }}</small></span
      >
    </div>
    <button class="session-card" @click="newSession">
      <span class="session-icon muted">＋</span
      ><span><strong>Nueva sesión</strong><small>Limpiar contexto y empezar de nuevo</small></span>
    </button>

    <button
      class="mobile-sessions-toggle"
      type="button"
      :aria-expanded="showMobileSessions"
      aria-controls="saved-sessions"
      title="Sesiones guardadas"
      aria-label="Sesiones guardadas"
      @click="showMobileSessions = !showMobileSessions"
    >
      ▤
    </button>
    <div id="saved-sessions" class="session-history" :class="{ 'mobile-open': showMobileSessions }">
      <div class="section-label">
        GUARDADAS
        <button
          class="session-refresh"
          title="Actualizar lista"
          aria-label="Actualizar sesiones guardadas"
          @click="emit('refresh-sessions')"
        >
          ↻
        </button>
      </div>
      <ul v-if="sessions.length" class="session-list">
        <li
          v-for="row in sessions"
          :key="row.id"
          class="session-row"
          :class="{ current: row.mode !== 'meeting' && row.id === currentSessionId }"
        >
          <button
            class="session-open"
            :disabled="
              row.mode === 'meeting'
                ? view === 'meeting' && row.id === meetingSessionId
                : row.id === currentSessionId
            "
            :title="row.preview || 'Sesión sin mensajes'"
            @click="openSession(row.id)"
          >
            <strong
              ><span v-if="row.mode === 'meeting'">◔ </span>{{ sessionLabel(row)
              }}<span v-if="row.mode !== 'meeting' && row.id === currentSessionId">
                · en curso</span
              ></strong
            >
            <small
              >{{ sessionDate(row) }} · {{ row.messages }}
              {{ row.mode === 'meeting' ? 'frag' : 'msg'
              }}{{ row.title ? '' : ' · ' + row.mode }}</small
            >
          </button>
          <button
            v-if="row.id !== currentSessionId"
            class="session-delete"
            title="Borrar sesión"
            :aria-label="'Borrar sesión ' + sessionLabel(row)"
            @click="emit('remove-session', row.id)"
          >
            ×
          </button>
        </li>
      </ul>
      <small v-if="sessionMessage" class="session-hint">{{ sessionMessage }}</small>
    </div>
  </aside>
  <div
    class="sidebar-resizer"
    role="separator"
    aria-label="Cambiar ancho de barra lateral"
    aria-orientation="vertical"
    tabindex="0"
    :aria-valuenow="sidebarWidth"
    aria-valuemin="160"
    aria-valuemax="420"
    @pointerdown="startSidebarResize"
    @pointermove="resizeSidebar"
    @pointerup="stopSidebarResize"
    @pointercancel="stopSidebarResize"
    @lostpointercapture="stopSidebarResize"
    @keydown.left.prevent="nudgeSidebar(-16)"
    @keydown.right.prevent="nudgeSidebar(16)"
  ></div>
</template>
