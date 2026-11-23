<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  DeleteVoiceSample,
  ExportMeetingTranscript,
  HasVoiceSample,
  MeetingPeople,
  MeetingTranscriptSegments,
  MeetingTranscriptStatus,
  Microphones,
  RecordVoiceSample,
  RenameMeetingSpeaker,
  RetryMeetingSegments,
  SelectedMicrophone,
  SetMeetingPeople,
  SetMicrophone,
  StartMeetingTranscript,
  StopMeetingTranscript,
  SuggestMeetingSpeakerNames,
} from '../../wailsjs/go/app/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { usePanelResize } from '../composables/usePanelResize'

type Segment = {
  id: number
  track: string
  startMs: number
  endMs: number
  text: string
  status: string
  speakerKey: string
}
type View = {
  sessionId: string
  startedAt: string
  language: string
  segments: Segment[]
  speakers: Record<string, string>
}
type Status = {
  state: string
  sessionId: string
  elapsedMs: number
  provider: string
  microphoneState: string
  outputState: string
  pending: number
  failed: number
  message: string
  diarizeEnabled: boolean
  diarizeNote: string
  diarizeSessionId: string
  meetSpeakers: string[] | null
}
type Mic = { target: string; label: string; running: boolean; app: string }
// One renameable entity: the microphone track, the generic meeting_output
// label, or one diarized participant (track + speakerKey).
type SpeakerRef = { track: string; speakerKey: string }

// sessionId is the saved meeting to show; empty means none picked yet.
const props = defineProps<{ sessionId: string; audioLanguage: string }>()
const emit = defineEmits<{
  (event: 'sessions-changed'): void
  (event: 'opened', id: string): void
  (event: 'set-audio-language', language: string): void
}>()

const status = ref<Status>({
  state: 'idle',
  sessionId: '',
  elapsedMs: 0,
  provider: '',
  microphoneState: '',
  outputState: '',
  pending: 0,
  failed: 0,
  message: '',
  diarizeEnabled: false,
  diarizeNote: '',
  diarizeSessionId: '',
  meetSpeakers: null,
})
const view = ref<View | null>(null)
const mics = ref<Mic[]>([])
const selectedMic = ref('')
const message = ref('')
const busy = ref(false)
const list = ref<HTMLElement | null>(null)
const editing = ref<SpeakerRef | null>(null)
const draftName = ref('')
const tick = ref(0)
// Suggestions are never applied automatically: each one needs a click to
// accept, same principle as every other rename in this panel.
const suggestions = ref<Record<string, string>>({})
const suggesting = ref(false)
const suggestMessage = ref('')
// Phase 3: a voluntary voice sample, used to clear echoes of the user's own
// voice from the meeting_output track. Off until one is recorded.
const hasVoiceSample = ref(false)
const recordingSample = ref(false)
const sampleMessage = ref('')

const recording = computed(() => status.value.state === 'recording')
const processing = computed(() => status.value.state === 'processing')
// The meeting on screen is the one being recorded only when ids match: a saved
// meeting can be read while another is recording.
const shownIsLive = computed(
  () =>
    !!view.value &&
    view.value.sessionId === status.value.sessionId &&
    status.value.state !== 'idle',
)
const providerLabels: Record<string, string> = {
  deepseek: 'DeepSeek',
  azure: 'Azure OpenAI',
  openai: 'OpenAI',
  local: 'Whisper local',
}
const providerLabel = computed(() => providerLabels[status.value.provider] || 'OpenAI')
const selectedMicAvailable = computed(() => mics.value.some((mic) => mic.app === selectedMic.value))
const canStart = computed(
  () =>
    !busy.value &&
    status.value.state === 'idle' &&
    !!selectedMic.value &&
    selectedMicAvailable.value,
)
const speakers = computed(
  () => view.value?.speakers ?? { microphone: 'Yo', meeting_output: 'Participante' },
)
// Every distinct speaker actually present in this meeting's segments, in
// first-appearance order, so the rename chips match who really spoke instead
// of always showing a fixed microphone/meeting_output pair.
const presentSpeakers = computed<SpeakerRef[]>(() => {
  const seen = new Set<string>()
  const refs: SpeakerRef[] = []
  for (const segment of view.value?.segments ?? []) {
    if (segment.status !== 'ok') continue
    const dedupeKey = `${segment.track}:${segment.speakerKey}`
    if (seen.has(dedupeKey)) continue
    seen.add(dedupeKey)
    refs.push({ track: segment.track, speakerKey: segment.speakerKey })
  }
  return refs
})

// One color per person, in order of first appearance, shared by the chips
// above and the names in the transcript.
const speakerPalette = ['#86d6b0', '#c2a8ff', '#f2b880', '#7cc4f2', '#f28aa8', '#d8d27a']
function speakerColor(track: string, speakerKey = '') {
  const index = presentSpeakers.value.findIndex(
    (ref) => ref.track === track && ref.speakerKey === speakerKey,
  )
  return speakerPalette[Math.max(index, 0) % speakerPalette.length]
}

// Chat-style: name and time only when the speaker changes or after a pause,
// instead of repeating them on every block.
const groupGapMs = 30_000
const rows = computed(() => {
  let previous: Segment | null = null
  return (view.value?.segments ?? []).map((segment) => {
    const continued =
      segment.status === 'ok' &&
      previous?.status === 'ok' &&
      previous.track === segment.track &&
      previous.speakerKey === segment.speakerKey &&
      segment.startMs - previous.endMs < groupGapMs
    previous = segment
    return { segment, continued }
  })
})

// Setup collapses while recording so the transcript gets the space.
const setupOpen = ref(true)
watch(
  () => status.value.state,
  (state) => {
    setupOpen.value = state === 'idle'
  },
)
// A saved meeting with text is opened to read it, not to set up a recording.
watch(
  () => view.value?.sessionId,
  () => {
    if (status.value.state === 'idle') setupOpen.value = !view.value?.segments.length
  },
)
const selectedMicLabel = computed(
  () => mics.value.find((mic) => mic.app === selectedMic.value)?.label ?? 'sin micrófono',
)

const trackText: Record<string, string> = {
  ok: 'capturando',
  starting: 'iniciando',
  missing: 'no disponible',
  reconnecting: 'reconectando',
  error: 'error',
}
function trackLabel(state: string) {
  return trackText[state] || '—'
}

function clock(ms: number) {
  const total = Math.max(0, Math.floor(ms / 1000))
  const part = (value: number) => String(value).padStart(2, '0')
  return `${part(Math.floor(total / 3600))}:${part(Math.floor(total / 60) % 60)}:${part(total % 60)}`
}

// elapsed advances locally between status events so the timer does not stutter.
const elapsed = computed(() => {
  void tick.value
  return clock(status.value.elapsedMs + (recording.value ? Date.now() - statusAt.value : 0))
})
const statusAt = ref(Date.now())
let timer = 0

// Mirrors the Go side's speakerDisplayKey/trackLabel: a diarized participant's
// own name, falling back to the track's generic name, falling back to "unknown".
function speaker(track: string, speakerKey = '') {
  const key = speakerKey ? `${track}:${speakerKey}` : track
  return speakers.value[key] || speakers.value[track] || 'Hablante desconocido'
}

function sessionDate(iso: string) {
  const when = new Date(iso)
  return Number.isNaN(when.getTime())
    ? iso
    : when.toLocaleString([], {
        day: '2-digit',
        month: '2-digit',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      })
}

async function refreshMics() {
  try {
    mics.value = (await Microphones()) as Mic[]
    selectedMic.value = await SelectedMicrophone()
  } catch {
    message.value = 'No se pudo leer los micrófonos (¿PipeWire está activo?).'
  }
}

async function recordSample() {
  recordingSample.value = true
  sampleMessage.value = 'Grabando 8 segundos… hablá con naturalidad.'
  try {
    await RecordVoiceSample()
    hasVoiceSample.value = true
    sampleMessage.value = 'Muestra guardada. Al terminar una reunión se intenta detectar tu eco.'
  } catch (error) {
    sampleMessage.value = String(error)
  } finally {
    recordingSample.value = false
  }
}

async function deleteSample() {
  try {
    await DeleteVoiceSample()
    hasVoiceSample.value = false
    sampleMessage.value = 'Muestra borrada.'
  } catch (error) {
    sampleMessage.value = String(error)
  }
}

const people = ref(0)
const transcriptResize = usePanelResize('sonara-meeting-transcript-height', 300)

async function choosePeople(value: string) {
  people.value = await SetMeetingPeople(Number(value))
}

async function chooseMic(target: string) {
  try {
    selectedMic.value = await SetMicrophone(target)
    message.value = ''
  } catch (error) {
    message.value = String(error)
    await refreshMics()
  }
}

async function load(id: string) {
  suggestions.value = {}
  partials.value = {}
  suggestMessage.value = ''
  if (!id) {
    view.value = null
    return
  }
  try {
    view.value = (await MeetingTranscriptSegments(id)) as View
    message.value = ''
    await scrollToEnd(true)
  } catch (error) {
    view.value = null
    message.value = String(error)
  }
}

// Separation and naming add participants after the text arrived: only the
// names are refreshed, the segments already on screen stay as they are.
async function refreshSpeakers() {
  const id = view.value?.sessionId
  if (!id) return
  try {
    const fresh = (await MeetingTranscriptSegments(id)) as View
    if (view.value?.sessionId === id) view.value.speakers = fresh.speakers
  } catch {
    // keep the names already shown
  }
}

function applyStatus(next: Status) {
  const wasBusy = status.value.state !== 'idle'
  status.value = next
  statusAt.value = Date.now()
  if (wasBusy && next.state === 'idle') {
    emit('sessions-changed')
    void refreshSpeakers()
  }
}

async function scrollToEnd(force = false) {
  const el = list.value
  if (!el) return
  // Same rule as the chat panel: follow new text only if already at the end.
  if (!force && el.scrollHeight - el.scrollTop - el.clientHeight > 90) return
  await nextTick()
  list.value?.scrollTo({ top: list.value.scrollHeight })
}

function upsert(segment: Segment) {
  if (!view.value) return
  const segments = view.value.segments
  const index = segments.findIndex((item) => item.id === segment.id)
  if (index >= 0) segments.splice(index, 1, segment)
  else segments.push(segment)
  // Blocks finish out of order across the two tracks; the shared clock is the order.
  segments.sort((a, b) => a.startMs - b.startMs || a.id - b.id)
  void scrollToEnd()
}

async function start() {
  busy.value = true
  message.value = ''
  try {
    const next = (await StartMeetingTranscript()) as Status
    applyStatus(next)
    emit('opened', next.sessionId)
    emit('sessions-changed')
    await load(next.sessionId)
  } catch (error) {
    message.value = String(error)
  } finally {
    busy.value = false
  }
}

async function stop() {
  applyStatus((await StopMeetingTranscript()) as Status)
}

async function exportTxt() {
  if (!view.value) return
  try {
    const path = await ExportMeetingTranscript(view.value.sessionId)
    message.value = path ? `Exportado en ${path}` : ''
  } catch (error) {
    message.value = String(error)
  }
}

async function retry() {
  if (!view.value) return
  try {
    const count = await RetryMeetingSegments(view.value.sessionId)
    message.value = count
      ? `Se recuperaron ${count} fragmento(s).`
      : 'No se pudo recuperar ningún fragmento.'
    await load(view.value.sessionId)
  } catch (error) {
    message.value = String(error)
  }
}

function editName(ref: SpeakerRef) {
  editing.value = ref
  draftName.value = speaker(ref.track, ref.speakerKey)
}

async function suggestNames() {
  if (!view.value) return
  suggesting.value = true
  suggestMessage.value = ''
  try {
    suggestions.value = await SuggestMeetingSpeakerNames(view.value.sessionId)
    if (!Object.keys(suggestions.value).length)
      suggestMessage.value = 'No se encontraron nombres mencionados en la conversación.'
  } catch (error) {
    suggestMessage.value = String(error)
  } finally {
    suggesting.value = false
  }
}

async function acceptSuggestion(speakerKey: string) {
  const name = suggestions.value[speakerKey]
  const rest = { ...suggestions.value }
  delete rest[speakerKey]
  suggestions.value = rest
  if (!view.value || !name) return
  try {
    view.value.speakers = await RenameMeetingSpeaker(
      view.value.sessionId,
      'meeting_output',
      speakerKey,
      name,
    )
  } catch (error) {
    message.value = String(error)
  }
}

function dismissSuggestion(speakerKey: string) {
  const rest = { ...suggestions.value }
  delete rest[speakerKey]
  suggestions.value = rest
}

async function saveName() {
  const ref = editing.value
  if (!ref || !view.value) return
  editing.value = null
  try {
    view.value.speakers = await RenameMeetingSpeaker(
      view.value.sessionId,
      ref.track,
      ref.speakerKey,
      draftName.value,
    )
  } catch (error) {
    message.value = String(error)
  }
}

let offStatus: (() => void) | undefined
let offSegment: (() => void) | undefined
let offPartial: (() => void) | undefined
let offSpeakers: (() => void) | undefined

// Words being spoken right now, per track, before their block is stored.
const partials = ref<Record<string, string>>({})
const partialRows = computed(() =>
  Object.entries(partials.value).filter(([, text]) => text.trim() !== ''),
)

onMounted(async () => {
  await refreshMics()
  hasVoiceSample.value = await HasVoiceSample().catch(() => false)
  people.value = await MeetingPeople().catch(() => 0)
  applyStatus((await MeetingTranscriptStatus()) as Status)
  offStatus = EventsOn('meeting:status', (next: Status) => applyStatus(next))
  offSegment = EventsOn('meeting:segment', (event: { sessionId: string; segment: Segment }) => {
    if (event.sessionId !== view.value?.sessionId) return
    upsert(event.segment)
    const { track, speakerKey } = event.segment
    if (speakerKey && !speakers.value[`${track}:${speakerKey}`]) void refreshSpeakers()
  })
  offSpeakers = EventsOn('meeting:speakers', (sessionId: string) => {
    if (sessionId === view.value?.sessionId) void refreshSpeakers()
  })
  offPartial = EventsOn(
    'meeting:partial',
    (event: { sessionId: string; track: string; text: string }) => {
      if (event.sessionId !== view.value?.sessionId) return
      partials.value = { ...partials.value, [event.track]: event.text ?? '' }
      void scrollToEnd()
    },
  )
  timer = window.setInterval(() => {
    tick.value++
  }, 1000)
  // Coming back to the tab while a meeting records shows that meeting.
  await load(props.sessionId || (status.value.state !== 'idle' ? status.value.sessionId : ''))
})

onBeforeUnmount(() => {
  offStatus?.()
  offSegment?.()
  offPartial?.()
  offSpeakers?.()
  window.clearInterval(timer)
})

watch(
  () => props.sessionId,
  (id) => {
    void load(id)
  },
)
</script>

<template>
  <div class="meeting">
    <div class="content-header">
      <div>
        <p class="eyebrow">
          TRANSCRIBIR · {{ shownIsLive ? 'EN CURSO' : view ? 'REUNIÓN GUARDADA' : 'NUEVA' }}
        </p>
        <h1>
          {{ view ? `Reunión · ${sessionDate(view.startedAt)}` : 'Transcripción de reunión' }}
        </h1>
        <p class="workspace-subtitle">Quién habla y qué dijo, línea por línea.</p>
      </div>
    </div>
    <div class="meeting-controls panel">
      <details
        class="meeting-setup"
        :open="setupOpen"
        @toggle="setupOpen = ($event.target as HTMLDetailsElement).open"
      >
        <summary>
          Ajustes de grabación
          <span class="setup-summary"
            >{{ selectedMicLabel }} · {{ audioLanguage === 'en' ? 'English' : 'Español' }} ·
            {{ people ? `${people} personas` : 'personas: auto' }}</span
          >
        </summary>
        <div class="setup-grid">
          <div class="field">
            <label for="meeting-mic">Micrófono</label>
            <div class="meeting-mic">
              <select
                id="meeting-mic"
                :value="selectedMic"
                :disabled="recording || processing"
                @change="chooseMic(($event.target as HTMLSelectElement).value)"
              >
                <option value="">Elegí un micrófono…</option>
                <option v-for="mic in mics" :key="mic.target" :value="mic.app">
                  {{ mic.label }}
                </option>
              </select>
              <button
                title="Actualizar micrófonos"
                aria-label="Actualizar micrófonos"
                :disabled="recording"
                @click="refreshMics"
              >
                ↻
              </button>
            </div>
          </div>
          <div class="field">
            <label
              for="meeting-language"
              title="Idioma en que se habla: forzar otro inventa palabras"
              >Idioma</label
            >
            <select
              id="meeting-language"
              :value="audioLanguage"
              :disabled="recording || processing"
              @change="emit('set-audio-language', ($event.target as HTMLSelectElement).value)"
            >
              <option value="es">Español</option>
              <option value="en">English</option>
            </select>
          </div>
          <div v-if="status.diarizeEnabled" class="field">
            <label for="meeting-people" title="Cuántas personas hablan por este micrófono"
              >Personas en la sala</label
            >
            <select
              id="meeting-people"
              :value="people"
              @change="choosePeople(($event.target as HTMLSelectElement).value)"
            >
              <option :value="0">Automático</option>
              <option v-for="n in 6" :key="n" :value="n">{{ n }}</option>
            </select>
          </div>
          <div v-if="status.diarizeEnabled" class="field">
            <label title="Sirve para descartar tu eco si usás parlantes">Mi voz</label>
            <div class="sample-actions">
              <button
                v-if="!hasVoiceSample"
                class="meeting-secondary"
                :disabled="recordingSample || recording || processing"
                @click="recordSample"
              >
                {{ recordingSample ? 'Grabando…' : 'Grabar muestra' }}
              </button>
              <template v-else>
                <span class="sample-status">✓ Grabada</span>
                <button
                  class="link-button"
                  :disabled="recordingSample || recording || processing"
                  @click="recordSample"
                >
                  Regrabar
                </button>
                <button
                  class="link-button"
                  :disabled="recordingSample || recording || processing"
                  @click="deleteSample"
                >
                  Borrar
                </button>
              </template>
            </div>
          </div>
        </div>
        <p
          class="meeting-note"
          title="Graba tu micrófono y el audio de la reunión (Teams o navegador, fuente elegida en ♫)."
        >
          Transcribe con <strong>{{ providerLabel }}</strong> · el audio no se guarda · avisá que
          estás transcribiendo
        </p>
        <small v-if="sampleMessage" class="meeting-message" role="status">{{
          sampleMessage
        }}</small>
      </details>
      <div class="meeting-row meeting-actions">
        <button
          v-if="!recording && !processing"
          class="record-button"
          :disabled="!canStart"
          @click="start"
        >
          <span>●</span>{{ busy ? 'Iniciando…' : 'Iniciar transcripción' }}
        </button>
        <button v-else class="record-button stop" :disabled="processing" @click="stop">
          <span>■</span>{{ processing ? 'Procesando…' : 'Detener transcripción' }}
        </button>
        <button
          class="meeting-secondary"
          :disabled="!view || !view.segments.length"
          @click="exportTxt"
        >
          Exportar .txt
        </button>
        <button
          class="meeting-secondary"
          :disabled="!view || !view.segments.length || suggesting"
          @click="suggestNames"
        >
          {{ suggesting ? 'Buscando nombres…' : 'Sugerir nombres' }}
        </button>
      </div>
      <div v-if="Object.keys(suggestions).length" class="name-suggestions" role="status">
        <span>Se mencionaron estos nombres en la conversación:</span>
        <span v-for="(name, key) in suggestions" :key="key" class="suggestion-chip">
          {{ speaker('meeting_output', key) }} → <strong>{{ name }}</strong>
          <button title="Usar este nombre" @click="acceptSuggestion(key)">Aplicar</button>
          <button title="Descartar" @click="dismissSuggestion(key)">×</button>
        </span>
      </div>
      <small v-if="suggestMessage" class="meeting-message" role="status">{{
        suggestMessage
      }}</small>
      <div v-if="!status.diarizeEnabled" class="meeting-warning" role="status">
        Separación de participantes apagada: falta sherpa-onnx (local) o una clave de Gemini, así
        que no se distingue quién habla.
      </div>
      <div
        v-else-if="status.diarizeNote && view && status.diarizeSessionId === view.sessionId"
        class="meeting-warning"
        role="status"
      >
        {{ status.diarizeNote }}
      </div>
      <div v-if="status.state !== 'idle'" class="track-status" role="status">
        <span class="rec-dot" :class="{ live: recording }"></span><strong>{{ elapsed }}</strong>
        <span
          >Tu micrófono:
          <em :class="status.microphoneState">{{ trackLabel(status.microphoneState) }}</em></span
        >
        <span
          >Reunión: <em :class="status.outputState">{{ trackLabel(status.outputState) }}</em></span
        >
        <span v-if="status.meetSpeakers?.length"
          >Google Meet: {{ status.meetSpeakers.join(', ') }}</span
        >
        <span v-if="status.pending">En cola: {{ status.pending }}</span>
        <span v-if="processing">Terminando de transcribir lo grabado…</span>
      </div>
      <div v-if="status.message" class="meeting-warning" role="alert">{{ status.message }}</div>
      <div v-if="status.failed" class="meeting-warning" role="alert">
        {{ status.failed }} fragmento(s) no se pudieron transcribir y quedan marcados.
        <button class="meeting-secondary" @click="retry">Reintentar</button>
      </div>
      <small v-if="message" class="meeting-message" role="status">{{ message }}</small>
    </div>

    <section
      class="panel meeting-transcript resizable-panel"
      :class="{ resizing: transcriptResize.resizing.value }"
      :style="transcriptResize.style.value"
    >
      <div class="panel-heading">
        <span>Transcripción</span>
        <span v-if="view" class="speaker-names">
          <template v-for="ref in presentSpeakers" :key="ref.track + ':' + ref.speakerKey">
            <input
              v-if="editing && editing.track === ref.track && editing.speakerKey === ref.speakerKey"
              v-model="draftName"
              class="speaker-input"
              maxlength="60"
              :placeholder="speaker(ref.track, ref.speakerKey)"
              @keydown.enter.prevent="saveName"
              @keydown.esc="editing = null"
              @blur="saveName"
            />
            <button
              v-else
              class="speaker-chip"
              :style="{ color: speakerColor(ref.track, ref.speakerKey) }"
              :title="'Cambiar nombre de ' + speaker(ref.track, ref.speakerKey)"
              @click="editName(ref)"
            >
              {{ speaker(ref.track, ref.speakerKey) }} ✎
            </button>
          </template>
        </span>
      </div>
      <div v-if="view && (view.segments.length || partialRows.length)" ref="list" class="segments">
        <template v-for="{ segment, continued } in rows" :key="segment.id">
          <div v-if="segment.status === 'ok'" class="turn" :class="{ continued }">
            <div v-if="!continued" class="turn-head">
              <strong :style="{ color: speakerColor(segment.track, segment.speakerKey) }">{{
                speaker(segment.track, segment.speakerKey)
              }}</strong>
              <time>{{ clock(segment.startMs) }}</time>
            </div>
            <p>{{ segment.text }}</p>
          </div>
          <p v-else-if="segment.status === 'echo'" class="segment echo">
            <time>{{ clock(segment.startMs) }}</time><span
              >Eco de tu propia voz, omitido como duplicado.</span
            >
          </p>
          <p v-else class="segment gap" :class="segment.status">
            <time>{{ clock(segment.startMs) }}–{{ clock(segment.endMs) }}</time>
            <span
              >{{
                segment.status === 'failed'
                  ? 'Sin transcribir: falló la transcripción'
                  : 'Sin transcribir: audio no capturado'
              }}
              ({{ speaker(segment.track, segment.speakerKey) }})</span
            >
          </p>
        </template>
        <div v-for="[track, text] in partialRows" :key="'partial:' + track" class="turn partial">
          <div class="turn-head">
            <strong :style="{ color: speakerColor(track) }">{{ speaker(track) }}</strong>
            <time>ahora</time>
          </div>
          <p>{{ text }}</p>
        </div>
      </div>
      <div v-else class="empty-state">
        <span class="empty-icon">✦</span>
        <p v-if="shownIsLive">Escuchando… el texto aparece mientras hablan.</p>
        <p v-else-if="view">Esta reunión no tiene texto guardado.</p>
        <p v-else>
          Elegí un micrófono e iniciá la transcripción, o abrí una reunión guardada desde la barra
          lateral.
        </p>
      </div>
      <div
        class="panel-resizer"
        role="separator"
        aria-label="Cambiar alto de la transcripción"
        aria-orientation="horizontal"
        tabindex="0"
        @pointerdown="transcriptResize.start"
        @pointermove="transcriptResize.move"
        @pointerup="transcriptResize.stop"
        @pointercancel="transcriptResize.stop"
        @lostpointercapture="transcriptResize.stop"
        @keydown.up.prevent="transcriptResize.nudge($event, -16)"
        @keydown.down.prevent="transcriptResize.nudge($event, 16)"
      ></div>
    </section>
  </div>
</template>

<style scoped>
.meeting {
  display: grid;
  gap: 16px;
}
.meeting-controls {
  min-height: 0;
  padding: 16px 18px;
  display: grid;
  gap: 12px;
}
.meeting-row {
  display: flex;
  gap: 12px;
  align-items: center;
}
.meeting-row label {
  flex: none;
  color: #9b9daa;
  font-size: 12px;
}
.meeting-mic {
  flex: 1;
  min-width: 0;
  display: flex;
  gap: 5px;
}
.meeting-mic select,
#meeting-people,
#meeting-language {
  min-width: 0;
  flex: 1;
  padding: 8px 6px;
  border: 1px solid #34313f;
  border-radius: 6px;
  color: #ddd;
  background: #17171f;
  font-size: 12px;
}
#meeting-people,
#meeting-language {
  flex: none;
  width: 100%;
}
.setup-grid {
  display: grid;
  grid-template-columns: minmax(0, 2fr) repeat(3, minmax(0, 1fr));
  gap: 18px;
  align-items: end;
}
.field {
  display: grid;
  gap: 7px;
  min-width: 0;
}
.field > label {
  color: #9b9daa;
  font-size: 11px;
  letter-spacing: 0.02em;
}
.sample-actions {
  display: flex;
  gap: 10px;
  align-items: center;
  min-height: 36px;
}
.sample-actions .meeting-secondary {
  width: 100%;
  padding: 8px 13px;
}
.link-button {
  padding: 0;
  color: #8fb8a9;
  background: none;
  font-size: 11px;
  text-decoration: underline;
  text-underline-offset: 3px;
}
.link-button:disabled {
  opacity: 0.4;
  cursor: default;
}
@media (max-width: 760px) {
  .setup-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
.meeting-mic button {
  padding: 0 11px;
  border-radius: 6px;
  color: #b9f2df;
  background: rgb(29 58 49 / var(--surface-opacity));
  transition: background 0.15s ease;
}
.meeting-mic button:not(:disabled):hover {
  background: rgb(38 72 61 / var(--surface-opacity));
}
.meeting-mic button:disabled,
.meeting-mic select:disabled {
  opacity: 0.45;
  cursor: default;
}
.meeting-note {
  margin: 0;
  color: #80818f;
  font-size: 11px;
  line-height: 1.55;
}
.meeting-note strong {
  color: #a9c9bd;
  font-weight: 600;
}
.voice-sample-row label {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.field-hint {
  color: #6b6d7b;
  font-weight: 400;
  font-size: 10px;
}
.sample-status {
  padding: 0 6px;
  color: #86d6b0;
  font-size: 10px;
  align-self: center;
}
.meeting-actions {
  flex-wrap: wrap;
  padding-top: 12px;
  border-top: 1px solid rgb(41 42 53 / var(--surface-opacity));
}
.record-button {
  padding: 11px 16px;
  border-radius: 7px;
  color: #071711;
  background: rgb(98 215 179 / var(--surface-opacity));
  font-size: 11px;
  font-weight: 700;
  transition: background 0.15s ease;
}
.record-button:not(:disabled):hover {
  background: rgb(118 230 196 / var(--surface-opacity));
}
.record-button span {
  margin-right: 8px;
  font-size: 9px;
}
.record-button.stop {
  color: #f5e1e5;
  background: rgb(101 56 69 / var(--surface-opacity));
}
.record-button.stop:not(:disabled):hover {
  background: rgb(122 70 85 / var(--surface-opacity));
}
.record-button:disabled {
  opacity: 0.5;
  cursor: default;
}
.meeting-secondary {
  padding: 9px 13px;
  color: #b9f2df;
  border: 1px solid rgb(62 126 108 / var(--surface-opacity));
  border-radius: 7px;
  background: rgb(20 40 34 / var(--surface-opacity));
  font-size: 11px;
  transition: background 0.15s ease;
}
.meeting-secondary:not(:disabled):hover {
  background: rgb(27 57 47 / var(--surface-opacity));
}
.meeting-secondary:disabled {
  opacity: 0.4;
  cursor: default;
}
.track-status {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  align-items: center;
  color: #8d8f9d;
  font:
    11px 'DM Mono',
    monospace;
}
.track-status strong {
  color: #d7d8e0;
  font-weight: 500;
}
.track-status em {
  font-style: normal;
  color: #86d6b0;
}
.track-status em.missing,
.track-status em.error,
.track-status em.reconnecting {
  color: #eed39a;
}
.rec-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #626471;
}
.rec-dot.live {
  background: #e0687e;
  animation: rec-blink 1.2s ease-in-out infinite;
}
@keyframes rec-blink {
  50% {
    opacity: 0.35;
  }
}
.meeting-warning {
  padding: 8px 10px;
  display: flex;
  gap: 10px;
  align-items: center;
  justify-content: space-between;
  border-radius: 6px;
  border: 1px solid rgb(122 103 60 / var(--surface-opacity));
  color: #eed39a;
  background: rgb(57 44 32 / var(--surface-opacity));
  font-size: 11px;
}
.meeting-message {
  color: #a99dba;
  font-size: 11px;
  overflow-wrap: anywhere;
}
.name-suggestions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  padding: 9px 11px;
  border-radius: 7px;
  border: 1px solid rgb(62 126 108 / var(--surface-opacity));
  background: rgb(20 40 34 / var(--surface-opacity));
  color: #a9c9bd;
  font-size: 10px;
}
.suggestion-chip {
  display: flex;
  gap: 5px;
  align-items: center;
  padding: 3px 7px;
  border-radius: 5px;
  background: rgb(14 26 22 / var(--surface-opacity));
}
.suggestion-chip strong {
  color: #b9f2df;
}
.suggestion-chip button {
  padding: 2px 7px;
  border-radius: 4px;
  color: #0c1e18;
  background: rgb(98 215 179 / var(--surface-opacity));
  font-size: 9px;
  font-weight: 700;
  transition: background 0.15s ease;
}
.suggestion-chip button:hover {
  background: rgb(118 230 196 / var(--surface-opacity));
}
.suggestion-chip button[title='Descartar'] {
  color: #d7dce0;
  background: rgb(44 48 54 / var(--surface-opacity));
  font-weight: 400;
}
.suggestion-chip button[title='Descartar']:hover {
  background: rgb(58 63 70 / var(--surface-opacity));
}
.meeting-transcript {
  min-height: 300px;
  height: clamp(300px, calc(100vh - 360px), 620px);
}
.speaker-names {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.speaker-chip {
  padding: 4px 8px;
  border-radius: 5px;
  color: #a9c9bd;
  background: rgb(25 38 34 / var(--surface-opacity));
  font:
    11px 'DM Mono',
    monospace;
  transition: background 0.15s ease;
}
.speaker-chip:hover {
  background: rgb(33 49 44 / var(--surface-opacity));
}
.speaker-input {
  width: 110px;
  padding: 4px 6px;
  border: 1px solid #3d6a5c;
  border-radius: 5px;
  outline: none;
  color: #eee;
  background: #101016;
  font:
    10px 'DM Mono',
    monospace;
}
.segments {
  flex: 1;
  min-height: 0;
  padding: 14px 18px;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  scrollbar-color: #3d6a5c transparent;
}
.turn {
  margin-top: 14px;
}
.turn:first-child {
  margin-top: 0;
}
.turn.continued {
  margin-top: 4px;
}
.turn.partial p {
  color: #8d8f9d;
  font-style: italic;
}
.turn.partial p::after {
  content: '▍';
  margin-left: 2px;
  color: #62d7b3;
  animation: rec-blink 1s steps(1) infinite;
}
.turn-head {
  display: flex;
  gap: 8px;
  align-items: baseline;
  margin-bottom: 2px;
}
.turn-head strong {
  font-size: 12px;
  font-weight: 600;
}
.turn-head time {
  color: #626471;
  font:
    10px 'DM Mono',
    monospace;
}
.turn p {
  margin: 0;
  color: #d4d5dc;
  font-size: 13px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}
.meeting-setup {
  display: grid;
  gap: 16px;
}
.meeting-setup summary {
  cursor: pointer;
  color: #c9cad3;
  font-size: 12px;
  font-weight: 600;
  list-style-position: inside;
}
.meeting-setup[open] summary {
  margin-bottom: 4px;
}
.setup-summary {
  margin-left: 8px;
  color: #747583;
  font-weight: 400;
  font-size: 11px;
}
.segment {
  margin: 10px 0;
  display: grid;
  grid-template-columns: 62px 96px minmax(0, 1fr);
  gap: 8px;
  font-size: 12px;
  line-height: 1.55;
  overflow-wrap: anywhere;
}
.segment time {
  color: #626471;
  font:
    10px 'DM Mono',
    monospace;
  padding-top: 2px;
}
.segment strong {
  color: #86d6b0;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.segment.meeting_output strong {
  color: #c2a8ff;
}
.segment span {
  color: #d4d5dc;
}
.segment.gap {
  grid-template-columns: 130px minmax(0, 1fr);
  color: #8d8f9d;
  font-style: italic;
}
.segment.gap span {
  color: #b5a37a;
}
.segment.echo {
  grid-template-columns: 62px minmax(0, 1fr);
  color: #7c8089;
  font-style: italic;
  opacity: 0.75;
}
@media (max-width: 760px) {
  .segment {
    grid-template-columns: 56px minmax(0, 1fr);
  }
  .segment strong {
    grid-column: 2;
  }
  .segment span {
    grid-column: 2;
  }
  .meeting-row {
    flex-wrap: wrap;
  }
}
</style>
