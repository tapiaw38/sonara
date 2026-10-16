<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { AIEndpoint, AnalyzeLastCapture, AudioLevel, AudioSources, CaptureScreen, DeleteSession, DiscardPendingSegment, IsListening, LastCapture, ListeningStatus, NewSession, OpenSession, SendMessage, SendPendingSegment, Sessions, SetAssistantMode, SetAudioLanguage, SetAudioSource, SetManualSend, SetOpenAIAPIKey, SetSessionBrief, StartListening, StopListening, ToggleAlwaysOnTop } from '../wailsjs/go/main/App'
import { WindowSetAlwaysOnTop } from '../wailsjs/runtime/runtime'

type Message = { role: 'user' | 'assistant'; text: string; time: string }
type AudioSourceOption = { target: string; label: string; running: boolean; meeting: boolean }
type SessionRow = { id: string; startedAt: string; mode: string; language: string; messages: number; preview: string; title: string }

const listening = ref(false)
const draft = ref('')
const transcript = ref('Esperando audio de la reunión…')
const messages = ref<Message[]>([])
const messagesPanel = ref<HTMLElement | null>(null)
// Replies stream in token by token, so the newest one grows past the bottom of
// the panel and the whole thing reads as frozen. Follow it, but only when the
// view is already at the bottom: yanking the panel while the user reads an
// earlier answer is worse than not following at all.
watch(messages, () => {
  const panel = messagesPanel.value
  // Measured before the DOM updates, which is what makes "was at the bottom"
  // mean the state the user is looking at rather than the one after growing.
  if (!panel || panel.scrollHeight - panel.scrollTop - panel.clientHeight > 72) return
  void nextTick(() => {
    messagesPanel.value?.scrollTo({ top: messagesPanel.value.scrollHeight })
  })
}, { deep: true })
const uiSessionGeneration = ref(1)
const captureRequestID = ref(0)
const startedAt = ref('—')
const endpoint = ref('')
const apiKey = ref('')
const apiKeyMessage = ref('')
const savingApiKey = ref(false)
const showSettings = ref(false)
const showSessionSetup = ref(false)
const sessionSetupMessage = ref('')
const sessionScenario = ref('General')
const sessionCompany = ref('')
const sessionRole = ref('')
const sessionObjective = ref('')
const sessionNotes = ref('')
const codeFontSize = ref(Number(localStorage.getItem('sonara-code-font-size') || 12))
const busy = ref(false)
const compact = ref(false)
const ghost = ref(false)
const alwaysOnTop = ref(false)
const opacityPercent = ref(Number(localStorage.getItem('sonara-opacity') || 22))
const savedSidebarWidth = Number(localStorage.getItem('sonara-sidebar-width'))
const sidebarWidth = ref(Number.isFinite(savedSidebarWidth) && savedSidebarWidth >= 160 ? Math.min(savedSidebarWidth, 420) : 225)
const sidebar = ref<HTMLElement | null>(null)
const sidebarDrag = ref<{ pointerId: number; startX: number; startWidth: number } | null>(null)
const showMobileSessions = ref(false)
const captureStatus = ref('Sin captura pendiente')
const analyzingCapture = ref(false)
const sendingMessage = ref(false)
const lastAnalyzedCapture = ref(localStorage.getItem('sonara-last-analyzed-capture') || '')
const captureSessionStartedAt = ref(0)
const lastVoiceSequence = ref(0)
const listenStatus = ref('')
const manualSend = ref(localStorage.getItem('sonara-manual-send') === '1')
const pendingSeconds = ref(0)
const droppedSegments = ref(0)
const audioGuessing = ref(false)
const audioCandidates = ref(0)
const audioSources = ref<AudioSourceOption[]>([])
const selectedAudioSource = ref('')
const audioSourceMessage = ref('')
const showAudioSources = ref(false)
const audioLanguage = ref(localStorage.getItem('sonara-audio-language') === 'en' ? 'en' : 'es')
const storedMode = localStorage.getItem('sonara-mode') || 'interview'
const assistantMode = ref(['interview', 'daily', 'coding', 'coaching'].includes(storedMode) ? storedMode : 'interview')
const assistantModeLabel = computed(() => ({ interview: 'Entrevista', daily: 'Reunión', coding: 'Coding', coaching: 'Coaching' }[assistantMode.value] || 'Entrevista'))
const assistantModeTitle = computed(() => ({ interview: 'Copiloto en vivo', daily: 'Copiloto de reunión', coding: 'Copiloto de código', coaching: 'Coach de entrevista' }[assistantMode.value] || 'Copiloto en vivo'))
const translation = ref('')
const streamingIndex = ref<number | null>(null)
const WAVE_BARS = 18
const levels = ref<number[]>(Array(WAVE_BARS).fill(0))

const statusLabel = computed(() => listening.value ? 'Escuchando' : 'En pausa')
const statusClass = computed(() => listening.value ? 'is-live' : 'is-idle')
const agentWorking = computed(() => analyzingCapture.value || sendingMessage.value || /transcribiendo|analizando|respondiendo/i.test(listenStatus.value))
const audioDetected = computed(() => listening.value && levels.value.slice(-5).some((level) => level >= 0.05))

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
  localStorage.setItem('sonara-sidebar-width', String(sidebarWidth.value))
}

function nudgeSidebar(delta: number) {
  setSidebarWidth((sidebar.value?.getBoundingClientRect().width ?? sidebarWidth.value) + delta)
  localStorage.setItem('sonara-sidebar-width', String(sidebarWidth.value))
}

async function toggleListening() {
  if (busy.value) return
  busy.value = true
  listening.value = listening.value ? await StopListening() : await StartListening()
  busy.value = false
  startedAt.value = listening.value
    ? new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : '—'
  transcript.value = listening.value
    ? 'Conectando audio de Teams o navegador…'
    : 'Escucha pausada.'
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

async function setAudioLanguage(language: string) {
  audioLanguage.value = await SetAudioLanguage(language)
  localStorage.setItem('sonara-audio-language', audioLanguage.value)
}

async function refreshAudioSources() {
  try {
    audioSources.value = await AudioSources()
    if (selectedAudioSource.value && !audioSources.value.some((source) => source.target === selectedAudioSource.value)) {
      selectedAudioSource.value = await SetAudioSource('')
      audioSourceMessage.value = 'Fuente anterior desapareció. Elegí otra.'
    } else {
      audioSourceMessage.value = audioSources.value.length ? '' : 'Abrí Teams y actualizá la lista.'
    }
  } catch {
    audioSourceMessage.value = 'No se pudo leer audio de PipeWire.'
  }
}

async function selectAudioSource(target: string) {
  try {
    selectedAudioSource.value = await SetAudioSource(target)
    audioSourceMessage.value = ''
  } catch {
    await refreshAudioSources()
    audioSourceMessage.value = 'Fuente desapareció. Elegí otra.'
  }
}

async function setMode(mode: string) {
  assistantMode.value = await SetAssistantMode(mode)
  localStorage.setItem('sonara-mode', assistantMode.value)
}

function sessionProfileKey(id: string) { return `sonara-session-profile:${id}` }

async function activateSessionProfile(id: string) {
  sessionCompany.value = ''
  sessionRole.value = ''
  sessionObjective.value = ''
  sessionNotes.value = ''
  sessionScenario.value = 'General'
  if (id) {
    try {
      const saved = JSON.parse(localStorage.getItem(sessionProfileKey(id)) || '{}') as Record<string, string>
      sessionScenario.value = saved.scenario || 'General'
      sessionCompany.value = saved.company || ''
      sessionRole.value = saved.role || ''
      sessionObjective.value = saved.objective || ''
      sessionNotes.value = saved.notes || ''
    } catch {
      localStorage.removeItem(sessionProfileKey(id))
    }
  }
  await SetSessionBrief([
    `Scenario: ${sessionScenario.value}`,
    sessionCompany.value && `Company: ${sessionCompany.value}`,
    sessionRole.value && `Role: ${sessionRole.value}`,
    sessionObjective.value && `Interview goal: ${sessionObjective.value}`,
    sessionNotes.value && `Candidate background and instructions: ${sessionNotes.value}`,
  ].filter(Boolean).join('\n'))
}

async function saveSessionSetup() {
  const profile = {
    scenario: sessionScenario.value,
    company: sessionCompany.value.trim(),
    role: sessionRole.value.trim(),
    objective: sessionObjective.value.trim(),
    notes: sessionNotes.value.trim(),
  }
  try {
    await SetSessionBrief([
      `Scenario: ${profile.scenario}`,
      profile.company && `Company: ${profile.company}`,
      profile.role && `Role: ${profile.role}`,
      profile.objective && `Interview goal: ${profile.objective}`,
      profile.notes && `Candidate background and instructions: ${profile.notes}`,
    ].filter(Boolean).join('\n'))
    if (currentSessionID.value) localStorage.setItem(sessionProfileKey(currentSessionID.value), JSON.stringify(profile))
    sessionSetupMessage.value = 'Contexto aplicado a esta sesión.'
  } catch {
    sessionSetupMessage.value = 'No se pudo aplicar el contexto.'
  }
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
  window.setTimeout(() => { if (button.isConnected) button.textContent = 'Copiar' }, 1400)
}

const sessions = ref<SessionRow[]>([])
const currentSessionID = ref('')
const sessionMessage = ref('')
const emptyConversationLabel = ref('Escribe una pregunta o espera la próxima intervención.')

function sessionDate(row: SessionRow) {
  const when = new Date(row.startedAt)
  if (Number.isNaN(when.getTime())) return row.id
  return when.toLocaleString([], { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
}

// The title arrives a moment after the first exchange, so until then the date is
// the only thing that identifies a row.
function sessionLabel(row: SessionRow) {
  return row.title || sessionDate(row)
}

async function refreshSessions() {
  try {
    sessions.value = await Sessions()
    sessionMessage.value = sessions.value.length ? '' : 'Sin sesiones guardadas todavía.'
  } catch {
    sessionMessage.value = 'No se pudo leer las sesiones guardadas.'
  }
}

// Reopening replaces the conversation and shows every saved message. The
// backend keeps only the recent messages in prompt context.
async function openSession(id: string) {
  if (id === currentSessionID.value) return
  try {
    const opened = await OpenSession(id)
    const selected = sessions.value.find((row) => row.id === id)
    sessions.value = opened.sessions
    uiSessionGeneration.value++
    captureRequestID.value++
    captureSessionStartedAt.value = Date.now()
    messages.value = opened.messages.map((message) => ({
      role: message.role === 'user' ? 'user' : 'assistant',
      text: message.text,
      time: new Date(message.createdAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    }))
    emptyConversationLabel.value = opened.messages.length
      ? 'Escribe una pregunta o espera la próxima intervención.'
      : 'Esta sesión no tiene mensajes guardados.'
    translation.value = ''
    draft.value = ''
    streamingIndex.value = null
    sendingMessage.value = false
    analyzingCapture.value = false
    const state = await ListeningStatus()
    lastVoiceSequence.value = state.sequence
    transcript.value = state.transcript
    currentSessionID.value = id
    if (selected?.mode) await setMode(selected.mode)
    await activateSessionProfile(id)
    sessionMessage.value = 'Sesión reabierta. El contexto volvió al prompt.'
    showMobileSessions.value = false
  } catch (error) {
    sessionMessage.value = String(error || 'No se pudo abrir la sesión')
  }
}

async function removeSession(id: string) {
  try {
    sessions.value = await DeleteSession(id)
    localStorage.removeItem(sessionProfileKey(id))
    sessionMessage.value = 'Sesión borrada.'
  } catch (error) {
    sessionMessage.value = String(error || 'No se pudo borrar la sesión')
  }
}

async function newSession() {
  showMobileSessions.value = false
  uiSessionGeneration.value++
  captureRequestID.value++
  captureSessionStartedAt.value = Date.now()
  await NewSession()
  messages.value = []
  emptyConversationLabel.value = 'Escribe una pregunta o espera la próxima intervención.'
  transcript.value = 'Esperando audio de la reunión…'
  translation.value = ''
  draft.value = ''
  lastVoiceSequence.value = (await ListeningStatus()).sequence
  streamingIndex.value = null
  sendingMessage.value = false
  analyzingCapture.value = false
  startedAt.value = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  captureStatus.value = 'Nueva sesión lista'
  currentSessionID.value = (await ListeningStatus()).sessionId
  await activateSessionProfile(currentSessionID.value)
  await refreshSessions()
}

async function loadState() {
  try {
    listening.value = await IsListening()
    endpoint.value = await AIEndpoint()
    await SetManualSend(manualSend.value)
    await setAudioLanguage(audioLanguage.value)
    await setMode(assistantMode.value)
    const state = await ListeningStatus()
    selectedAudioSource.value = state.audioSource || ''
    currentSessionID.value = state.sessionId
    await activateSessionProfile(currentSessionID.value)
    await refreshAudioSources()
    await refreshSessions()
    await refreshCapture()
  } catch {
    endpoint.value = 'Servicio local no disponible'
  }
}

async function saveApiKey() {
  if (savingApiKey.value) return
  savingApiKey.value = true
  apiKeyMessage.value = ''
  try {
    await SetOpenAIAPIKey(apiKey.value)
    apiKey.value = ''
    endpoint.value = await AIEndpoint()
    apiKeyMessage.value = endpoint.value.includes('Falta') ? 'API key eliminada de esta sesión' : 'API key configurada para esta sesión'
  } catch (error) {
    apiKeyMessage.value = String(error || 'No se pudo guardar la API key')
  } finally {
    savingApiKey.value = false
  }
}

loadState()

async function toggleAlwaysOnTop() {
  const target = !alwaysOnTop.value
  WindowSetAlwaysOnTop(target)
  if (await ToggleAlwaysOnTop()) {
    alwaysOnTop.value = target
  }
}

function setOpacity(value: string | number) {
  opacityPercent.value = Number(value)
  localStorage.setItem('sonara-opacity', String(opacityPercent.value))
}

function formatMessage(text: string) {
  const escape = (value: string) => value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')

  const formatPlain = (value: string) => escape(value)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/^[-*] (.+)$/gm, '<span class="message-bullet">•</span> $1')
    // The Spanish version of a suggested English answer is there to read, not
    // to say out loud, so it must not look like part of the answer.
    .replace(/^ES: (.+)$/gm, '<span class="reply-translation"><i>ES</i>$1</span>')
    .replace(/\n/g, '<br>')

  return text.split(/```([\w+-]*)\n?([\s\S]*?)```/g).map((part, index, parts) => {
    if (index % 3 === 1) return ''
    if (index % 3 === 2) {
      const language = parts[index - 1] || 'text'
      return `<pre><button type="button" class="copy-code-button" data-copy-code>Copiar</button><code class="language-${escape(language)}">${escape(part.trim())}</code></pre>`
    }
    return formatPlain(part)
  }).join('')
}

async function refreshCapture() {
  const generation = uiSessionGeneration.value
  const capture = await LastCapture()
  if (generation !== uiSessionGeneration.value) return
  if (!capture.queued) return
  const time = new Date(capture.capturedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  captureStatus.value = capture.agentReady
    ? `Captura ${time} · lista para agente`
    : `Captura ${time} · pendiente de conectar IA`
  if (capture.agentReady && Date.parse(capture.capturedAt) >= captureSessionStartedAt.value && capture.capturedAt !== lastAnalyzedCapture.value) {
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
    if (generation === uiSessionGeneration.value) captureStatus.value = 'No se pudo capturar. Verifica grim.'
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

window.setInterval(() => { void refreshCapture() }, 1500)
// Fast enough that a streamed reply reads as it is written.
window.setInterval(() => { void refreshListening() }, 300)
// The backend measures one RMS frame every 100ms, so the wave scrolls at the
// same rate the audio is actually sampled.
window.setInterval(() => { void refreshLevel() }, 100)

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
    if (state.transcript) transcript.value = state.transcript
    translation.value = state.translation
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
</script>

<template>
  <main class="shell" :class="{ compact, ghost, 'is-analyzing': analyzingCapture }" :style="{ '--surface-opacity': ghost ? opacityPercent / 100 : 1, '--code-font-size': `${codeFontSize}px` }">
    <header class="topbar">
      <div class="brand"><img class="brand-mark" src="/sonara-logo.png" alt="" /><span>SONARA</span></div>
      <nav class="product-nav" aria-label="Modo de trabajo">
        <button :class="{ active: assistantMode === 'interview' }" @click="setMode('interview')">En vivo</button>
        <button :class="{ active: assistantMode === 'daily' }" @click="setMode('daily')">Reunión</button>
        <button :class="{ active: assistantMode === 'coding' }" @click="setMode('coding')">Coding</button>
        <button :class="{ active: assistantMode === 'coaching' }" @click="setMode('coaching')">Coaching</button>
      </nav>
      <div class="meeting-state"><span class="status-dot" :class="statusClass"></span>{{ statusLabel }}</div>
      <div class="overlay-controls">
        <button class="overlay-button settings-button" title="Configuración" aria-label="Configuración" @click="showSettings = true">⚙</button>
        <button class="overlay-button" :class="{ active: compact }" title="Modo compacto" @click="compact = !compact">▣</button>
        <button class="overlay-button" :class="{ active: ghost }" title="Modo casi invisible" @click="ghost = !ghost">◌</button>
        <label v-if="ghost" class="opacity-control" title="Opacidad"><input type="range" min="5" max="100" :value="opacityPercent" @input="setOpacity(($event.target as HTMLInputElement).value)" /><span>{{ opacityPercent }}%</span></label>
        <button class="overlay-button" :class="{ active: alwaysOnTop }" title="Siempre encima" @click="toggleAlwaysOnTop()">↥</button>
        <button class="overlay-button audio-source-top-control" :class="{ guessing: audioGuessing }" :title="audioGuessing ? `Fuente de audio: adivinando entre ${audioCandidates} posibles, revisá` : 'Fuente de audio'" aria-label="Elegir fuente de audio" @click="showAudioSources = !showAudioSources; if (showAudioSources) refreshAudioSources()">♫</button>
      </div>
      <div v-if="showAudioSources" class="audio-source-popover">
        <label for="audio-source-popover-select">Fuente de audio</label>
        <div class="audio-source-controls">
          <select id="audio-source-popover-select" :value="selectedAudioSource" @change="selectAudioSource(($event.target as HTMLSelectElement).value)">
            <option value="">Automática</option>
            <option v-for="source in audioSources" :key="source.target" :value="source.target">{{ source.label }} · #{{ source.target }}{{ source.running ? ' · activo' : '' }}</option>
          </select>
          <button title="Actualizar fuentes" aria-label="Actualizar fuentes de audio" @click="refreshAudioSources">↻</button>
        </div>
        <small v-if="audioGuessing" class="audio-source-hint guessing">Hay {{ audioCandidates }} fuentes de audio posibles; Sonara eligió una adivinando. Revisá si es la correcta.</small>
        <small v-if="audioSourceMessage" class="audio-source-hint">{{ audioSourceMessage }}</small>
      </div>
    </header>

    <section class="workspace" :class="{ 'resizing-sidebar': sidebarDrag }" :style="{ '--sidebar-width': `${sidebarWidth}px` }">
      <aside ref="sidebar" class="sidebar">
        <div class="section-label">SESIÓN</div>
        <div class="session-card active" aria-label="Sesión actual"><span class="session-icon">◉</span><span><strong>{{ assistantModeLabel }} actual</strong><small>Hoy · {{ startedAt }}</small></span></div>
        <button class="session-card" @click="newSession"><span class="session-icon muted">＋</span><span><strong>Nueva sesión</strong><small>Limpiar contexto y empezar de nuevo</small></span></button>

        <button class="mobile-sessions-toggle" type="button" :aria-expanded="showMobileSessions" aria-controls="saved-sessions" title="Sesiones guardadas" aria-label="Sesiones guardadas" @click="showMobileSessions = !showMobileSessions">▤</button>
        <div id="saved-sessions" class="session-history" :class="{ 'mobile-open': showMobileSessions }">
          <div class="section-label">GUARDADAS <button class="session-refresh" title="Actualizar lista" aria-label="Actualizar sesiones guardadas" @click="refreshSessions">↻</button></div>
          <ul v-if="sessions.length" class="session-list">
            <li v-for="row in sessions" :key="row.id" class="session-row" :class="{ current: row.id === currentSessionID }">
              <button class="session-open" :disabled="row.id === currentSessionID" :title="row.preview || 'Sesión sin mensajes'" @click="openSession(row.id)">
                <strong>{{ sessionLabel(row) }}<span v-if="row.id === currentSessionID"> · en curso</span></strong>
                <small>{{ sessionDate(row) }} · {{ row.messages }} msg{{ row.title ? '' : ' · ' + row.mode }}</small>
              </button>
              <button v-if="row.id !== currentSessionID" class="session-delete" title="Borrar sesión" :aria-label="'Borrar sesión ' + sessionLabel(row)" @click="removeSession(row.id)">×</button>
            </li>
          </ul>
          <small v-if="sessionMessage" class="session-hint">{{ sessionMessage }}</small>
        </div>

      </aside>
      <div class="sidebar-resizer" role="separator" aria-label="Cambiar ancho de barra lateral" aria-orientation="vertical" tabindex="0" :aria-valuenow="sidebarWidth" aria-valuemin="160" aria-valuemax="420" @pointerdown="startSidebarResize" @pointermove="resizeSidebar" @pointerup="stopSidebarResize" @pointercancel="stopSidebarResize" @lostpointercapture="stopSidebarResize" @keydown.left.prevent="nudgeSidebar(-16)" @keydown.right.prevent="nudgeSidebar(16)"></div>

      <div class="content">
        <div class="content-header">
          <div><p class="eyebrow">{{ assistantModeLabel.toUpperCase() }} · SESIÓN ACTUAL</p><h1>{{ assistantModeTitle }}</h1><p class="workspace-subtitle">Escucha la conversación y prepara respuestas mientras hablas.</p></div>
          <div class="content-actions"><button class="session-setup-trigger" @click="showSessionSetup = true">＋ Contexto</button><button class="ghost-button" :class="{ scanning: analyzingCapture }" @click="captureScreen">{{ analyzingCapture ? '◌ Analizando' : '⌘ Capturar' }}</button></div>
        </div>

        <div class="panels">
          <section class="panel transcript-panel" :class="{ expanded: listening || droppedSegments > 0 }">
            <div class="panel-heading"><span>Transcripción</span><span class="live-label" :class="statusClass"><i></i>{{ statusLabel }}</span></div>
            <div class="transcript-body"><p class="transcript-text">{{ transcript }}</p><p v-if="translation" class="transcript-translation"><span>ES</span>{{ translation }}</p><div v-if="listening" class="audio-pulse" :class="{ active: audioDetected }"><span></span><span></span><span></span><small>{{ audioDetected ? listenStatus || 'Sonido detectado' : 'Esperando sonido…' }}</small></div><div v-if="audioDetected" class="wave"><i v-for="(level, index) in levels" :key="index" :class="{ active: level > 0.05 }" :style="{ height: `${3 + level * 42}px` }"></i></div></div>
            <div v-if="pendingSeconds > 0" class="pending-bar"><span>Fragmento de {{ pendingSeconds.toFixed(1) }}s en espera</span><button class="pending-send" @click="sendPending">Enviar</button><button class="pending-discard" @click="discardPending">Descartar</button></div>
            <div v-if="droppedSegments > 0" class="audio-warning" role="status">Audio perdido por cola llena: {{ droppedSegments }} fragmento(s). Revisá la transcripción.</div>
            <div class="listen-row">
              <button class="listen-button" :class="{ stop: listening }" :disabled="busy" @click="toggleListening"><span>{{ listening ? '■' : '●' }}</span>{{ busy ? 'Conectando…' : listening ? 'Pausar escucha' : 'Comenzar escucha' }}</button>
              <button class="mode-toggle" :class="{ manual: manualSend }" :title="manualSend ? 'Los fragmentos esperan tu confirmación' : 'Se envía solo al detectar silencio'" @click="toggleSendMode">{{ manualSend ? 'Manual' : 'Auto' }}</button>
            </div>
          </section>

          <section class="panel response-panel">
            <div class="panel-heading"><span>Respuesta sugerida</span><div class="response-tools"><div v-if="assistantMode === 'coding'" class="font-tools" aria-label="Tamaño de código"><button title="Reducir letra" :disabled="codeFontSize <= 10" @click="adjustCodeFont(-1)">A−</button><span>{{ codeFontSize }}px</span><button title="Aumentar letra" :disabled="codeFontSize >= 18" @click="adjustCodeFont(1)">A+</button></div><span class="kbd">⌘ ↵</span></div></div>
            <div v-if="messages.length || agentWorking" ref="messagesPanel" class="messages" @click="handleMessageClick"><div v-for="(message, index) in messages" :key="index" class="message" :class="message.role"><span class="message-role">{{ message.role === 'user' ? 'TÚ' : 'SONARA' }} · {{ message.time }}</span><div class="message-content" v-html="formatMessage(message.text)"></div></div><div v-if="agentWorking" class="typing-indicator"><span></span><span></span><span></span><small>{{ analyzingCapture ? 'Sonara leyendo imagen' : listenStatus || 'Sonara preparando respuesta' }}</small></div></div><div v-else class="empty-state"><span class="empty-icon">✦</span><p>{{ emptyConversationLabel }}</p></div>
            <div class="composer"><textarea v-model="draft" :disabled="sendingMessage" placeholder="Escribe una pregunta…" rows="2" @keydown.enter.exact.prevent="sendMessage" @keydown.meta.enter.prevent="sendMessage" @keydown.ctrl.enter.prevent="sendMessage"></textarea><button class="send-button" :disabled="sendingMessage" aria-label="Enviar" @click="sendMessage">{{ sendingMessage ? '…' : '↑' }}</button></div>
          </section>
        </div>

        <footer class="footer"><span>{{ captureStatus }} · Ctrl+Alt+C</span><span class="footer-right"><span class="status-dot is-live"></span> IA: {{ endpoint || 'sin conectar' }}</span></footer>
      </div>
    </section>

    <div v-if="showSettings" class="modal-overlay" @click.self="showSettings = false" @keydown.esc="showSettings = false">
      <section class="settings-dialog" role="dialog" aria-modal="true" aria-labelledby="settings-title">
        <header class="dialog-header"><div><p class="eyebrow">SONARA</p><h2 id="settings-title">Configuración</h2></div><button class="dialog-close" aria-label="Cerrar configuración" @click="showSettings = false">×</button></header>
        <div class="settings-section"><h3>Audio</h3><label for="settings-audio-source">Fuente de captura</label><div class="audio-source-controls"><select id="settings-audio-source" :value="selectedAudioSource" @change="selectAudioSource(($event.target as HTMLSelectElement).value)"><option value="">Automática</option><option v-for="source in audioSources" :key="source.target" :value="source.target">{{ source.label }} · #{{ source.target }}{{ source.running ? ' · activo' : '' }}</option></select><button title="Actualizar fuentes" aria-label="Actualizar fuentes de audio" @click="refreshAudioSources">↻</button></div><small v-if="audioGuessing" class="audio-source-hint guessing">Hay {{ audioCandidates }} fuentes posibles; revisa cuál corresponde a la reunión.</small><small v-if="audioSourceMessage" class="audio-source-hint">{{ audioSourceMessage }}</small><label>Idioma hablado</label><div class="language-switch"><button :class="{ active: audioLanguage === 'es' }" @click="setAudioLanguage('es')">Español</button><button :class="{ active: audioLanguage === 'en' }" @click="setAudioLanguage('en')">English</button></div></div>
        <div class="settings-section"><h3>OpenAI</h3><form class="api-key-form" @submit.prevent="saveApiKey"><label for="openai-key">API key</label><input id="openai-key" v-model="apiKey" type="password" autocomplete="off" spellcheck="false" placeholder="sk-…" /><small>Se guarda localmente en la configuración de Sonara.</small><div class="api-key-actions"><button type="submit" :disabled="savingApiKey || !apiKey">{{ savingApiKey ? 'Guardando…' : 'Guardar clave' }}</button><button type="button" :disabled="savingApiKey" @click="apiKey = ''; saveApiKey()">Quitar</button></div><small v-if="apiKeyMessage" role="status">{{ apiKeyMessage }}</small><small class="endpoint-status">{{ endpoint || 'Sin conectar' }}</small></form></div>
      </section>
    </div>

    <div v-if="showSessionSetup" class="modal-overlay" @click.self="showSessionSetup = false" @keydown.esc="showSessionSetup = false">
      <section class="settings-dialog session-dialog" role="dialog" aria-modal="true" aria-labelledby="session-setup-title">
        <header class="dialog-header"><div><p class="eyebrow">PREPARACIÓN</p><h2 id="session-setup-title">Contexto de entrevista</h2><p>Personaliza las respuestas para esta sesión.</p></div><button class="dialog-close" aria-label="Cerrar contexto" @click="showSessionSetup = false">×</button></header>
        <form class="session-setup-form" @submit.prevent="saveSessionSetup">
          <label for="session-scenario">Tipo de entrevista</label><select id="session-scenario" v-model="sessionScenario"><option>General</option><option>Conductual</option><option>Técnica</option><option>Diseño de sistemas</option><option>Producto y negocio</option><option>Personalizada</option></select>
          <label for="session-company">Empresa <span>Opcional</span></label><input id="session-company" v-model="sessionCompany" placeholder="Ej. Acme" />
          <label for="session-role">Puesto</label><input id="session-role" v-model="sessionRole" placeholder="Ej. Desarrollador frontend" />
          <label for="session-objective">Objetivo de la entrevista</label><input id="session-objective" v-model="sessionObjective" placeholder="Ej. entrevista técnica, primera ronda…" />
          <label for="session-notes">Tu experiencia o instrucciones</label><textarea id="session-notes" v-model="sessionNotes" rows="4" placeholder="Logros relevantes, tecnologías, tono y datos que Sonara debe tener en cuenta…"></textarea>
          <small class="profile-hint">Este contexto se agrega al prompt de la sesión y queda guardado localmente para volver a abrirla.</small>
          <div class="dialog-actions"><span role="status">{{ sessionSetupMessage }}</span><button class="primary-action" type="submit">Guardar contexto</button></div>
        </form>
      </section>
    </div>
  </main>
</template>
