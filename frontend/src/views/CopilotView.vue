<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  AnalyzeLastCapture,
  AudioLevel,
  CaptureScreen,
  DiscardPendingSegment,
  GetUIPreferences,
  IsListening,
  LastCapture,
  ListeningStatus,
  SaveUIPreferences as SaveUIPreferencesToDB,
  SendMessage,
  SendPendingSegment,
  SetManualSend,
  StartListening,
  StopListening,
} from '../../wailsjs/go/main/App'
import type { Message, TranscriptEntry } from '../types'

const props = defineProps<{ assistantMode: string }>()
const emit = defineEmits<{ (event: 'open-session-setup'): void }>()

const listening = defineModel<boolean>('listening', { default: false })
const startedAt = defineModel<string>('startedAt', { default: '—' })
const captureStatus = defineModel<string>('captureStatus', { default: 'Sin captura pendiente' })
const audioGuessing = defineModel<boolean>('audioGuessing', { default: false })
const audioCandidates = defineModel<number>('audioCandidates', { default: 0 })
const codeFontSize = defineModel<number>('codeFontSize', {
  default: Number(localStorage.getItem('sonara-code-font-size') || 12),
})
const panelsResizing = defineModel<boolean>('panelsResizing', { default: false })
const analyzingCapture = defineModel<boolean>('analyzingCapture', { default: false })

const assistantModeLabel = computed(
  () =>
    ({ interview: 'Entrevista', daily: 'Reunión', coding: 'Coding', coaching: 'Coaching' })[
      props.assistantMode
    ] || 'Entrevista',
)
const assistantModeTitle = computed(
  () =>
    ({
      interview: 'Copiloto en vivo',
      daily: 'Copiloto de reunión',
      coding: 'Copiloto de código',
      coaching: 'Coach de entrevista',
    })[props.assistantMode] || 'Copiloto en vivo',
)

const draft = ref('')
const transcript = ref('Esperando audio de la reunión…')
const messages = ref<Message[]>([])
const messagesPanel = ref<HTMLElement | null>(null)
// Replies stream in token by token, so the newest one grows past the bottom of
// the panel and the whole thing reads as frozen. Follow it, but only when the
// view is already at the bottom: yanking the panel while the user reads an
// earlier answer is worse than not following at all.
watch(
  messages,
  () => {
    const panel = messagesPanel.value
    // Measured before the DOM updates, which is what makes "was at the bottom"
    // mean the state the user is looking at rather than the one after growing.
    if (!panel || panel.scrollHeight - panel.scrollTop - panel.clientHeight > 72) return
    void nextTick(() => {
      messagesPanel.value?.scrollTo({ top: messagesPanel.value.scrollHeight })
    })
  },
  { deep: true },
)
const uiSessionGeneration = ref(1)
const captureRequestID = ref(0)
const busy = ref(false)
const responsePanelHeight = ref(0)
const transcriptPanelHeight = ref(0)
const panelMaxHeight = Math.max(280, window.innerHeight - 100)
const responsePanelEl = ref<HTMLElement | null>(null)
const transcriptPanelEl = ref<HTMLElement | null>(null)
const panelResize = ref<{
  panel: 'response' | 'transcript'
  pointerId: number
  startY: number
  startHeight: number
} | null>(null)
const sendingMessage = ref(false)
const lastAnalyzedCapture = ref(localStorage.getItem('sonara-last-analyzed-capture') || '')
const captureSessionStartedAt = ref(0)
const lastVoiceSequence = ref(0)
const lastRetractions = ref(0)
const listenStatus = ref('')
const manualSend = ref(localStorage.getItem('sonara-manual-send') === '1')
const pendingSeconds = ref(0)
const droppedSegments = ref(0)
const translation = ref('')
// Past finished turns, oldest first, so a quick back-and-forth doesn't erase
// the one before it: the panel only ever showed the latest, so a second
// phrase silently threw the first one away.
const transcriptHistory = ref<TranscriptEntry[]>([])
const MAX_TRANSCRIPT_HISTORY = 50
const transcriptPanel = ref<HTMLElement | null>(null)
function archiveCurrentTranscript() {
  // Only a real, answered turn is worth keeping: idle/status placeholders
  // ("Esperando audio…", "Sesión reabierta", …) never got a translation.
  if (!transcript.value || !translation.value) return
  transcriptHistory.value.push({ text: transcript.value, translation: translation.value })
  if (transcriptHistory.value.length > MAX_TRANSCRIPT_HISTORY) transcriptHistory.value.shift()
}
const streamingIndex = ref<number | null>(null)
const WAVE_BARS = 18
const levels = ref<number[]>(Array(WAVE_BARS).fill(0))
const emptyConversationLabel = ref('Escribe una pregunta o espera la próxima intervención.')

const statusLabel = computed(() => (listening.value ? 'Escuchando' : 'En pausa'))
const statusClass = computed(() => (listening.value ? 'is-live' : 'is-idle'))
const agentWorking = computed(
  () =>
    analyzingCapture.value ||
    sendingMessage.value ||
    /transcribiendo|analizando|respondiendo/i.test(listenStatus.value),
)
const audioDetected = computed(
  () => listening.value && levels.value.slice(-5).some((level) => level >= 0.05),
)

function panelHeightStyle(height: number) {
  return height > 0 ? { height: `${height}px`, minHeight: `${height}px` } : undefined
}

function legacyPanelHeight(key: string) {
  const value = Number(localStorage.getItem(key))
  return Number.isFinite(value) && value > 0 ? value : 0
}

function clearLegacyPanelHeights() {
  localStorage.removeItem('sonara-response-height')
  localStorage.removeItem('sonara-transcript-height')
}

async function loadPanelPreferences() {
  try {
    const preferences = await GetUIPreferences()
    const migrated = {
      responsePanelHeight:
        preferences.responsePanelHeight || legacyPanelHeight('sonara-response-height'),
      transcriptPanelHeight:
        preferences.transcriptPanelHeight || legacyPanelHeight('sonara-transcript-height'),
    }
    const hasLegacyValues =
      (!preferences.responsePanelHeight && migrated.responsePanelHeight > 0) ||
      (!preferences.transcriptPanelHeight && migrated.transcriptPanelHeight > 0)
    responsePanelHeight.value = migrated.responsePanelHeight
    transcriptPanelHeight.value = migrated.transcriptPanelHeight
    if (hasLegacyValues) {
      try {
        await SaveUIPreferencesToDB(migrated)
        clearLegacyPanelHeights()
      } catch (error) {
        console.warn(
          'No se pudieron migrar preferencias locales a SQLite; se conserva copia local.',
          error,
        )
      }
    } else {
      clearLegacyPanelHeights()
    }
  } catch (error) {
    responsePanelHeight.value = legacyPanelHeight('sonara-response-height')
    transcriptPanelHeight.value = legacyPanelHeight('sonara-transcript-height')
    console.warn(
      'No se pudieron cargar preferencias de panel desde SQLite; se conservan los valores locales.',
      error,
    )
  }
}

async function savePanelPreferences() {
  const preferences = {
    responsePanelHeight: responsePanelHeight.value,
    transcriptPanelHeight: transcriptPanelHeight.value,
  }
  try {
    await SaveUIPreferencesToDB(preferences)
    clearLegacyPanelHeights()
  } catch (error) {
    if (preferences.responsePanelHeight)
      localStorage.setItem('sonara-response-height', String(preferences.responsePanelHeight))
    else localStorage.removeItem('sonara-response-height')
    if (preferences.transcriptPanelHeight)
      localStorage.setItem('sonara-transcript-height', String(preferences.transcriptPanelHeight))
    else localStorage.removeItem('sonara-transcript-height')
    console.warn(
      'No se pudieron guardar preferencias de panel en SQLite; se conserva copia local.',
      error,
    )
  }
}

function startPanelResize(event: PointerEvent, panel: 'response' | 'transcript') {
  if (event.button !== 0) return
  event.preventDefault()
  const element = panel === 'response' ? responsePanelEl.value : transcriptPanelEl.value
  const startHeight = element?.getBoundingClientRect().height ?? 360
  panelResize.value = { panel, pointerId: event.pointerId, startY: event.clientY, startHeight }
  panelsResizing.value = true
  const handle = event.currentTarget as HTMLElement
  handle.setPointerCapture(event.pointerId)
}

function resizePanel(event: PointerEvent) {
  const drag = panelResize.value
  if (!drag || drag.pointerId !== event.pointerId) return
  const minHeight = drag.panel === 'response' ? 280 : 180
  const maxHeight = Math.max(minHeight, window.innerHeight - 100)
  const height = Math.max(
    minHeight,
    Math.min(maxHeight, Math.round(drag.startHeight + event.clientY - drag.startY)),
  )
  if (drag.panel === 'response') responsePanelHeight.value = height
  else transcriptPanelHeight.value = height
}

function stopPanelResize(event: PointerEvent) {
  const drag = panelResize.value
  if (!drag || drag.pointerId !== event.pointerId) return
  panelResize.value = null
  panelsResizing.value = false
  void savePanelPreferences()
}

function nudgePanel(panel: 'response' | 'transcript', delta: number) {
  const element = panel === 'response' ? responsePanelEl.value : transcriptPanelEl.value
  const currentHeight =
    panel === 'response' ? responsePanelHeight.value : transcriptPanelHeight.value
  const minHeight = panel === 'response' ? 280 : 180
  const maxHeight = Math.max(minHeight, window.innerHeight - 100)
  const height = Math.max(
    minHeight,
    Math.min(
      maxHeight,
      Math.round((currentHeight || element?.getBoundingClientRect().height || 360) + delta),
    ),
  )
  if (panel === 'response') responsePanelHeight.value = height
  else transcriptPanelHeight.value = height
  void savePanelPreferences()
}

async function toggleListening() {
  if (busy.value) return
  busy.value = true
  listening.value = listening.value ? await StopListening() : await StartListening()
  busy.value = false
  startedAt.value = listening.value
    ? new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : '—'
  transcript.value = listening.value ? 'Conectando audio de Teams o navegador…' : 'Escucha pausada.'
}

async function toggleSendMode() {
  manualSend.value = await SetManualSend(!manualSend.value)
  localStorage.setItem('sonara-manual-send', manualSend.value ? '1' : '0')
}

async function sendPending() {
  if (await SendPendingSegment()) pendingSeconds.value = 0
}

async function discardPending() {
  await DiscardPendingSegment()
  pendingSeconds.value = 0
}

function adjustCodeFont(delta: number) {
  codeFontSize.value = Math.max(10, Math.min(18, codeFontSize.value + delta))
  localStorage.setItem('sonara-code-font-size', String(codeFontSize.value))
}

async function handleMessageClick(event: MouseEvent) {
  const target = event.target
  if (!(target instanceof HTMLElement)) return
  const button = target.closest<HTMLButtonElement>('[data-copy-code]')
  if (!button) return
  const code = button.parentElement?.querySelector('code')?.textContent || ''
  try {
    await navigator.clipboard.writeText(code)
  } catch {
    const input = document.createElement('textarea')
    input.value = code
    input.style.position = 'fixed'
    input.style.opacity = '0'
    document.body.append(input)
    input.select()
    document.execCommand('copy')
    input.remove()
  }
  button.textContent = 'Copiado'
  window.setTimeout(() => {
    if (button.isConnected) button.textContent = 'Copiar'
  }, 1400)
}

function formatMessage(text: string) {
  const escape = (value: string) =>
    value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

  const formatPlain = (value: string) =>
    escape(value)
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/^[-*] (.+)$/gm, '<span class="message-bullet">•</span> $1')
      // The Spanish version of a suggested English answer is there to read, not
      // to say out loud, so it must not look like part of the answer.
      .replace(/^ES: (.+)$/gm, '<span class="reply-translation"><i>ES</i>$1</span>')
      .replace(/\n/g, '<br>')

  return text
    .split(/```([\w+-]*)\n?([\s\S]*?)```/g)
    .map((part, index, parts) => {
      if (index % 3 === 1) return ''
      if (index % 3 === 2) {
        const language = parts[index - 1] || 'text'
        return `<pre><button type="button" class="copy-code-button" data-copy-code>Copiar</button><code class="language-${escape(language)}">${escape(part.trim())}</code></pre>`
      }
      return formatPlain(part)
    })
    .join('')
}

async function refreshCapture() {
  const generation = uiSessionGeneration.value
  const capture = await LastCapture()
  if (generation !== uiSessionGeneration.value) return
  if (!capture.queued) return
  const time = new Date(capture.capturedAt).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
  })
  captureStatus.value = capture.agentReady
    ? `Captura ${time} · lista para agente`
    : `Captura ${time} · pendiente de conectar IA`
  if (
    capture.agentReady &&
    Date.parse(capture.capturedAt) >= captureSessionStartedAt.value &&
    capture.capturedAt !== lastAnalyzedCapture.value
  ) {
    await analyzeCapture(capture.capturedAt)
  }
}

async function captureScreen() {
  const generation = uiSessionGeneration.value
  captureStatus.value = 'Capturando pantalla…'
  try {
    await CaptureScreen()
    if (generation === uiSessionGeneration.value) await refreshCapture()
  } catch {
    if (generation === uiSessionGeneration.value)
      captureStatus.value = 'No se pudo capturar. Verifica grim.'
  }
}

async function analyzeCapture(capturedAt: string) {
  if (analyzingCapture.value) return
  const generation = uiSessionGeneration.value
  const requestID = ++captureRequestID.value
  analyzingCapture.value = true
  captureStatus.value = 'Sonara analizando captura…'
  try {
    const result = await AnalyzeLastCapture()
    if (generation !== uiSessionGeneration.value) return
    const time = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    messages.value.push({ role: 'assistant', text: result.response, time })
    lastAnalyzedCapture.value = capturedAt
    localStorage.setItem('sonara-last-analyzed-capture', capturedAt)
    captureStatus.value = `Captura analizada · Sonara respondió`
  } catch {
    if (generation !== uiSessionGeneration.value) return
    captureStatus.value = 'Sonara no pudo analizar captura. Captura otra para reintentar.'
    lastAnalyzedCapture.value = capturedAt
    localStorage.setItem('sonara-last-analyzed-capture', capturedAt)
  } finally {
    if (requestID === captureRequestID.value) analyzingCapture.value = false
  }
}

async function refreshLevel() {
  if (!listening.value) {
    if (levels.value.some(Boolean)) levels.value = Array(WAVE_BARS).fill(0)
    return
  }
  try {
    levels.value = [...levels.value.slice(1), await AudioLevel()]
  } catch {
    // A dropped poll just leaves the previous frame on screen.
  }
}

async function refreshListening() {
  const generation = uiSessionGeneration.value
  try {
    const state = await ListeningStatus()
    if (generation !== uiSessionGeneration.value) return
    listening.value = state.listening
    listenStatus.value = state.status
    manualSend.value = state.manualSend
    pendingSeconds.value = state.pendingSeconds
    droppedSegments.value = state.droppedSegments
    audioGuessing.value = state.audioGuessing
    audioCandidates.value = state.audioCandidates
    if (state.transcript && state.transcript !== transcript.value) {
      archiveCurrentTranscript()
      transcript.value = state.transcript
    }
    translation.value = state.translation
    void nextTick(() => {
      const panel = transcriptPanel.value
      if (panel) panel.scrollTop = panel.scrollHeight
    })
    // An anticipated reply that did not hold: drop the message it started.
    if (state.retractions > lastRetractions.value) {
      lastRetractions.value = state.retractions
      if (streamingIndex.value !== null && streamingIndex.value === messages.value.length - 1)
        messages.value.pop()
      streamingIndex.value = null
    }
    if (state.sequence > lastVoiceSequence.value) {
      messages.value.push({
        role: 'assistant',
        text: state.response,
        time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      })
      lastVoiceSequence.value = state.sequence
      streamingIndex.value = messages.value.length - 1
    } else if (streamingIndex.value !== null) {
      // Keep this compatible with streamed replies when the provider supports them.
      messages.value[streamingIndex.value].text = state.response
      if (!state.streaming) streamingIndex.value = null
    }
  } catch {
    // Keep the UI usable while an audio status poll fails.
  }
}

async function sendMessage() {
  const text = draft.value.trim()
  if (!text || sendingMessage.value) return
  const generation = uiSessionGeneration.value
  const time = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  messages.value.push({ role: 'user', text, time })
  draft.value = ''
  sendingMessage.value = true
  try {
    const result = await SendMessage(text)
    if (generation !== uiSessionGeneration.value) return
    messages.value.push({
      role: 'assistant',
      text: result.response,
      time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    })
  } catch {
    if (generation !== uiSessionGeneration.value) return
    messages.value.push({
      role: 'assistant',
      text: 'Sonara no pudo responder. Verifica OPENAI_API_KEY y la conexión.',
      time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    })
  } finally {
    if (generation === uiSessionGeneration.value) sendingMessage.value = false
  }
}

function beginSessionSwitch() {
  uiSessionGeneration.value++
  captureRequestID.value++
  captureSessionStartedAt.value = Date.now()
}

async function resetAfterNewSession() {
  messages.value = []
  emptyConversationLabel.value = 'Escribe una pregunta o espera la próxima intervención.'
  transcript.value = 'Esperando audio de la reunión…'
  translation.value = ''
  transcriptHistory.value = []
  draft.value = ''
  lastVoiceSequence.value = (await ListeningStatus()).sequence
  streamingIndex.value = null
  sendingMessage.value = false
  analyzingCapture.value = false
  startedAt.value = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  captureStatus.value = 'Nueva sesión lista'
}

async function restoreOpenedSession(opened: {
  messages: { role: string; text: string; createdAt: string }[]
}) {
  messages.value = opened.messages.map((message) => ({
    role: message.role === 'user' ? 'user' : 'assistant',
    text: message.text,
    time: new Date(message.createdAt).toLocaleTimeString([], {
      hour: '2-digit',
      minute: '2-digit',
    }),
  }))
  emptyConversationLabel.value = opened.messages.length
    ? 'Escribe una pregunta o espera la próxima intervención.'
    : 'Esta sesión no tiene mensajes guardados.'
  translation.value = ''
  transcriptHistory.value = []
  draft.value = ''
  streamingIndex.value = null
  sendingMessage.value = false
  analyzingCapture.value = false
  const state = await ListeningStatus()
  lastVoiceSequence.value = state.sequence
  lastRetractions.value = state.retractions
  transcript.value = state.transcript
}

defineExpose({ beginSessionSwitch, resetAfterNewSession, restoreOpenedSession })

let captureTimer = 0
let listeningTimer = 0
let levelTimer = 0

onMounted(async () => {
  await loadPanelPreferences()
  listening.value = await IsListening()
  await SetManualSend(manualSend.value)
  await refreshCapture()
  captureTimer = window.setInterval(() => {
    void refreshCapture()
  }, 1500)
  // Fast enough that a streamed reply reads as it is written.
  listeningTimer = window.setInterval(() => {
    void refreshListening()
  }, 100)
  // The backend measures one RMS frame every 100ms, so the wave scrolls at the
  // same rate the audio is actually sampled.
  levelTimer = window.setInterval(() => {
    void refreshLevel()
  }, 100)
})

onBeforeUnmount(() => {
  window.clearInterval(captureTimer)
  window.clearInterval(listeningTimer)
  window.clearInterval(levelTimer)
})
</script>

<template>
  <div class="content-header">
    <div>
      <p class="eyebrow">{{ assistantModeLabel.toUpperCase() }} · SESIÓN ACTUAL</p>
      <h1>{{ assistantModeTitle }}</h1>
      <p class="workspace-subtitle">
        Escucha la conversación y prepara respuestas mientras hablas.
      </p>
    </div>
    <div class="content-actions">
      <button class="session-setup-trigger" @click="emit('open-session-setup')">＋ Contexto</button
      ><button class="ghost-button" :class="{ scanning: analyzingCapture }" @click="captureScreen">
        {{ analyzingCapture ? '◌ Analizando' : '⌘ Capturar' }}
      </button>
    </div>
  </div>

  <div class="panels">
    <section
      ref="transcriptPanelEl"
      class="panel transcript-panel"
      :class="{
        expanded: listening || droppedSegments > 0,
        resizing: panelResize?.panel === 'transcript',
      }"
      :style="panelHeightStyle(transcriptPanelHeight)"
    >
      <div class="panel-heading">
        <span>Transcripción</span
        ><span class="live-label" :class="statusClass"><i></i>{{ statusLabel }}</span>
      </div>
      <div ref="transcriptPanel" class="transcript-body">
        <div
          v-for="(entry, index) in transcriptHistory"
          :key="index"
          class="transcript-turn transcript-turn-past"
        >
          <p class="transcript-text">{{ entry.text }}</p>
          <p class="transcript-translation"><span>ES</span>{{ entry.translation }}</p>
        </div>
        <div class="transcript-turn">
          <p class="transcript-text">{{ transcript }}</p>
          <p v-if="translation" class="transcript-translation"><span>ES</span>{{ translation }}</p>
        </div>
        <div v-if="listening" class="audio-pulse" :class="{ active: audioDetected }">
          <span></span><span></span><span></span
          ><small>{{
            audioDetected ? listenStatus || 'Sonido detectado' : 'Esperando sonido…'
          }}</small>
        </div>
        <div v-if="audioDetected" class="wave">
          <i
            v-for="(level, index) in levels"
            :key="index"
            :class="{ active: level > 0.05 }"
            :style="{ height: `${3 + level * 42}px` }"
          ></i>
        </div>
      </div>
      <div v-if="pendingSeconds > 0" class="pending-bar">
        <span>Fragmento de {{ pendingSeconds.toFixed(1) }}s en espera</span
        ><button class="pending-send" @click="sendPending">Enviar</button
        ><button class="pending-discard" @click="discardPending">Descartar</button>
      </div>
      <div v-if="droppedSegments > 0" class="audio-warning" role="status">
        Audio perdido por cola llena: {{ droppedSegments }} fragmento(s). Revisá la transcripción.
      </div>
      <div class="listen-row">
        <button
          class="listen-button"
          :class="{ stop: listening }"
          :disabled="busy"
          @click="toggleListening"
        >
          <span>{{ listening ? '■' : '●' }}</span>{{
            busy ? 'Conectando…' : listening ? 'Pausar escucha' : 'Comenzar escucha'
          }}
        </button>
        <button
          class="mode-toggle"
          :class="{ manual: manualSend }"
          :title="
            manualSend
              ? 'Los fragmentos esperan tu confirmación'
              : 'Se envía solo al detectar silencio'
          "
          @click="toggleSendMode"
        >
          {{ manualSend ? 'Manual' : 'Auto' }}
        </button>
      </div>
      <div
        class="panel-resizer"
        role="separator"
        aria-label="Cambiar alto de transcripción"
        aria-orientation="horizontal"
        tabindex="0"
        :aria-valuenow="transcriptPanelHeight || undefined"
        aria-valuemin="180"
        :aria-valuemax="panelMaxHeight"
        @pointerdown="startPanelResize($event, 'transcript')"
        @pointermove="resizePanel"
        @pointerup="stopPanelResize"
        @pointercancel="stopPanelResize"
        @lostpointercapture="stopPanelResize"
        @keydown.up.prevent="nudgePanel('transcript', -16)"
        @keydown.down.prevent="nudgePanel('transcript', 16)"
      ></div>
    </section>

    <section
      ref="responsePanelEl"
      class="panel response-panel"
      :class="{ resizing: panelResize?.panel === 'response' }"
      :style="panelHeightStyle(responsePanelHeight)"
    >
      <div class="panel-heading">
        <span>Respuesta sugerida</span>
        <div class="response-tools">
          <div v-if="assistantMode === 'coding'" class="font-tools" aria-label="Tamaño de código">
            <button
              title="Reducir letra"
              :disabled="codeFontSize <= 10"
              @click="adjustCodeFont(-1)"
            >
              A−</button
            ><span>{{ codeFontSize }}px</span
            ><button
              title="Aumentar letra"
              :disabled="codeFontSize >= 18"
              @click="adjustCodeFont(1)"
            >
              A+
            </button>
          </div>
          <span class="kbd">⌘ ↵</span>
        </div>
      </div>
      <div
        v-if="messages.length || agentWorking"
        ref="messagesPanel"
        class="messages"
        @click="handleMessageClick"
      >
        <div
          v-for="(message, index) in messages"
          :key="index"
          class="message"
          :class="message.role"
        >
          <span class="message-role"
            >{{ message.role === 'user' ? 'TÚ' : 'SONARA' }} · {{ message.time }}</span
          >
          <div class="message-content" v-html="formatMessage(message.text)"></div>
        </div>
        <div v-if="agentWorking" class="typing-indicator">
          <span></span><span></span><span></span
          ><small>{{
            analyzingCapture
              ? 'Sonara leyendo imagen'
              : listenStatus || 'Sonara preparando respuesta'
          }}</small>
        </div>
      </div>
      <div v-else class="empty-state">
        <span class="empty-icon">✦</span>
        <p>{{ emptyConversationLabel }}</p>
      </div>
      <div class="composer">
        <textarea
          v-model="draft"
          :disabled="sendingMessage"
          placeholder="Escribe una pregunta…"
          rows="2"
          @keydown.enter.exact.prevent="sendMessage"
          @keydown.meta.enter.prevent="sendMessage"
          @keydown.ctrl.enter.prevent="sendMessage"
        ></textarea
        ><button
          class="send-button"
          :disabled="sendingMessage"
          aria-label="Enviar"
          @click="sendMessage"
        >
          {{ sendingMessage ? '…' : '↑' }}
        </button>
      </div>
      <div
        class="panel-resizer"
        role="separator"
        aria-label="Cambiar alto de respuesta sugerida"
        aria-orientation="horizontal"
        tabindex="0"
        :aria-valuenow="responsePanelHeight || undefined"
        aria-valuemin="280"
        :aria-valuemax="panelMaxHeight"
        @pointerdown="startPanelResize($event, 'response')"
        @pointermove="resizePanel"
        @pointerup="stopPanelResize"
        @pointercancel="stopPanelResize"
        @lostpointercapture="stopPanelResize"
        @keydown.up.prevent="nudgePanel('response', -16)"
        @keydown.down.prevent="nudgePanel('response', 16)"
      ></div>
    </section>
  </div>
</template>
