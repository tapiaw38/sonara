<script setup lang="ts">
import { computed, ref } from 'vue'
import { AIEndpoint, AnalyzeLastCapture, CaptureScreen, DiscardPendingSegment, IsListening, LastCapture, ListeningStatus, SendMessage, SendPendingSegment, SetManualSend, StartListening, StopListening } from '../wailsjs/go/main/App'
import { WindowSetAlwaysOnTop } from '../wailsjs/runtime/runtime'

type Message = { role: 'user' | 'assistant'; text: string; time: string }

const listening = ref(false)
const draft = ref('')
const transcript = ref('Esperando audio de la reunión…')
const messages = ref<Message[]>([])
const startedAt = ref('—')
const endpoint = ref('')
const busy = ref(false)
const compact = ref(false)
const ghost = ref(false)
const alwaysOnTop = ref(true)
const opacityPercent = ref(Number(localStorage.getItem('sonara-opacity') || 22))
const captureStatus = ref('Sin captura pendiente')
const analyzingCapture = ref(false)
const sendingMessage = ref(false)
const lastAnalyzedCapture = ref(localStorage.getItem('sonara-last-analyzed-capture') || '')
const lastVoiceSequence = ref(0)
const listenStatus = ref('')
const manualSend = ref(localStorage.getItem('sonara-manual-send') === '1')
const pendingSeconds = ref(0)

const statusLabel = computed(() => listening.value ? 'Escuchando' : 'En pausa')
const statusClass = computed(() => listening.value ? 'is-live' : 'is-idle')
const agentWorking = computed(() => analyzingCapture.value || sendingMessage.value || /transcribiendo|analizando/i.test(listenStatus.value))

async function toggleListening() {
  if (busy.value) return
  busy.value = true
  listening.value = listening.value ? await StopListening() : await StartListening()
  busy.value = false
  startedAt.value = listening.value
    ? new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    : '—'
  transcript.value = listening.value
    ? 'Conectando audio de Chrome/Meet…'
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

async function loadState() {
  try {
    listening.value = await IsListening()
    endpoint.value = await AIEndpoint()
    await SetManualSend(manualSend.value)
    await refreshCapture()
  } catch {
    endpoint.value = 'Servicio local no disponible'
  }
}

loadState()
WindowSetAlwaysOnTop(true)

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
    .replace(/\n/g, '<br>')

  return text.split(/```([\w+-]*)\n?([\s\S]*?)```/g).map((part, index, parts) => {
    if (index % 3 === 1) return ''
    if (index % 3 === 2) {
      const language = parts[index - 1] || 'text'
      return `<pre><code class="language-${escape(language)}">${escape(part.trim())}</code></pre>`
    }
    return formatPlain(part)
  }).join('')
}

async function refreshCapture() {
  const capture = await LastCapture()
  if (!capture.queued) return
  const time = new Date(capture.capturedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  captureStatus.value = capture.agentReady
    ? `Captura ${time} · lista para agente`
    : `Captura ${time} · pendiente de conectar IA`
  if (capture.agentReady && capture.capturedAt !== lastAnalyzedCapture.value) {
    await analyzeCapture(capture.capturedAt)
  }
}

async function captureScreen() {
  captureStatus.value = 'Capturando pantalla…'
  try {
    await CaptureScreen()
    await refreshCapture()
  } catch {
    captureStatus.value = 'No se pudo capturar. Verifica grim.'
  }
}

async function analyzeCapture(capturedAt: string) {
  if (analyzingCapture.value) return
  analyzingCapture.value = true
  captureStatus.value = 'Gillie analizando captura…'
  try {
    const result = await AnalyzeLastCapture()
    const time = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    messages.value.push({ role: 'assistant', text: result.response, time })
    lastAnalyzedCapture.value = capturedAt
    localStorage.setItem('sonara-last-analyzed-capture', capturedAt)
    captureStatus.value = `Captura analizada · Gillie respondió`
  } catch {
    captureStatus.value = 'Gillie no pudo analizar captura. Reintentará al capturar otra.'
    lastAnalyzedCapture.value = capturedAt
    localStorage.setItem('sonara-last-analyzed-capture', capturedAt)
  } finally {
    analyzingCapture.value = false
  }
}

window.setInterval(() => { void refreshCapture() }, 1500)
window.setInterval(() => { void refreshListening() }, 800)

async function refreshListening() {
  try {
    const state = await ListeningStatus()
    listening.value = state.listening
    listenStatus.value = state.status
    manualSend.value = state.manualSend
    pendingSeconds.value = state.pendingSeconds
    if (state.transcript) transcript.value = state.transcript
    if (state.sequence > lastVoiceSequence.value && state.response) {
      messages.value.push({
        role: 'assistant',
        text: state.response,
        time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
      })
      lastVoiceSequence.value = state.sequence
    }
  } catch {
    // Gillie/UI remains usable while an audio poll fails.
  }
}

async function sendMessage() {
  const text = draft.value.trim()
  if (!text || sendingMessage.value) return
  const time = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  messages.value.push({ role: 'user', text, time })
  draft.value = ''
  sendingMessage.value = true
  try {
    const result = await SendMessage(text)
    messages.value.push({
      role: 'assistant',
      text: result.response,
      time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    })
  } catch {
    messages.value.push({
      role: 'assistant',
      text: 'Gillie no pudo responder. Verifica que servicio local esté activo.',
      time: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    })
  } finally {
    sendingMessage.value = false
  }
}
</script>

<template>
  <main class="shell" :class="{ compact, ghost, 'is-analyzing': analyzingCapture }" :style="{ '--surface-opacity': ghost ? opacityPercent / 100 : 1 }">
    <header class="topbar">
      <div class="brand"><span class="brand-mark">S</span><span>SONARA</span></div>
      <div class="meeting-state"><span class="status-dot" :class="statusClass"></span>{{ statusLabel }}</div>
      <div class="overlay-controls">
        <button class="overlay-button" :class="{ active: compact }" title="Modo compacto" @click="compact = !compact">▣</button>
        <button class="overlay-button" :class="{ active: ghost }" title="Modo casi invisible" @click="ghost = !ghost">◌</button>
        <label v-if="ghost" class="opacity-control" title="Opacidad"><input type="range" min="5" max="100" :value="opacityPercent" @input="setOpacity(($event.target as HTMLInputElement).value)" /><span>{{ opacityPercent }}%</span></label>
        <button class="overlay-button" :class="{ active: alwaysOnTop }" title="Siempre encima" @click="alwaysOnTop = !alwaysOnTop; WindowSetAlwaysOnTop(alwaysOnTop)">↥</button>
      </div>
    </header>

    <section class="workspace">
      <aside class="sidebar">
        <div class="section-label">SESIÓN</div>
        <button class="session-card active"><span class="session-icon">◉</span><span><strong>Entrevista actual</strong><small>Hoy · {{ startedAt }}</small></span></button>
        <button class="session-card"><span class="session-icon muted">＋</span><span><strong>Nueva sesión</strong><small>Preparar otra conversación</small></span></button>

        <div class="sidebar-bottom">
          <div class="section-label">MODO</div>
          <button class="mode-button active">Entrevista</button>
          <button class="mode-button">Comunicación diaria</button>
          <div class="privacy-note"><span>◌</span><span><strong>Protección activa</strong><small>Ventana excluida de captura</small></span></div>
        </div>
      </aside>

      <div class="content">
        <div class="content-header">
          <div><p class="eyebrow">ENTREVISTA ACTUAL</p><h1>Asistente de reunión</h1></div>
          <button class="ghost-button" :class="{ scanning: analyzingCapture }" @click="captureScreen">{{ analyzingCapture ? '◌ Analizando' : '⌘ Capturar' }}</button>
        </div>

        <div class="panels">
          <section class="panel transcript-panel" :class="{ expanded: listening }">
            <div class="panel-heading"><span>Transcripción</span><span class="live-label" :class="statusClass"><i></i>{{ statusLabel }}</span></div>
            <div class="transcript-body"><p class="transcript-text">{{ transcript }}</p><div v-if="listening" class="audio-pulse"><span></span><span></span><span></span><small>{{ listenStatus || 'Escuchando audio de Meet' }}</small></div><div v-if="listening" class="wave"><i v-for="n in 18" :key="n" :style="{ height: `${12 + (n * 17) % 30}px` }"></i></div></div>
            <div v-if="pendingSeconds > 0" class="pending-bar"><span>Fragmento de {{ pendingSeconds.toFixed(1) }}s en espera</span><button class="pending-send" @click="sendPending">Enviar</button><button class="pending-discard" @click="discardPending">Descartar</button></div>
            <div class="listen-row">
              <button class="listen-button" :class="{ stop: listening }" :disabled="busy" @click="toggleListening"><span>{{ listening ? '■' : '●' }}</span>{{ busy ? 'Conectando…' : listening ? 'Pausar escucha' : 'Comenzar escucha' }}</button>
              <button class="mode-toggle" :class="{ manual: manualSend }" :title="manualSend ? 'Los fragmentos esperan tu confirmación' : 'Se envía solo al detectar silencio'" @click="toggleSendMode">{{ manualSend ? 'Manual' : 'Auto' }}</button>
            </div>
          </section>

          <section class="panel response-panel">
            <div class="panel-heading"><span>Respuesta sugerida</span><span class="kbd">⌘ ↵</span></div>
            <div v-if="messages.length || agentWorking" class="messages"><div v-for="(message, index) in messages" :key="index" class="message" :class="message.role"><span class="message-role">{{ message.role === 'user' ? 'TÚ' : 'SONARA' }} · {{ message.time }}</span><div class="message-content" v-html="formatMessage(message.text)"></div></div><div v-if="agentWorking" class="typing-indicator"><span></span><span></span><span></span><small>{{ analyzingCapture ? 'Gillie leyendo imagen' : listenStatus || 'Gillie preparando respuesta' }}</small></div></div><div v-else class="empty-state"><span class="empty-icon">✦</span><p>Escribe una pregunta o espera la próxima intervención.</p></div>
            <div class="composer"><textarea v-model="draft" :disabled="sendingMessage" placeholder="Escribe una pregunta…" rows="2" @keydown.meta.enter.prevent="sendMessage" @keydown.ctrl.enter.prevent="sendMessage"></textarea><button class="send-button" :disabled="sendingMessage" aria-label="Enviar" @click="sendMessage">{{ sendingMessage ? '…' : '↑' }}</button></div>
          </section>
        </div>

        <footer class="footer"><span>{{ captureStatus }} · Ctrl+Alt+C</span><span class="footer-right"><span class="status-dot is-live"></span> IA: {{ endpoint || 'sin conectar' }}</span></footer>
      </div>
    </section>
  </main>
</template>
