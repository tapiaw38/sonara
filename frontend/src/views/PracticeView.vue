<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref } from 'vue'
import { PracticeRealtime, StopPractice } from '../../wailsjs/go/app/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import PracticeTurnText from '../components/PracticeTurnText.vue'
import { usePanelResize } from '../composables/usePanelResize'
import { useColumnSplit } from '../composables/useColumnSplit'

const emit = defineEmits<{ (event: 'open-session-setup'): void }>()

type Phase =
  'idle' | 'listening' | 'speaking' | 'capturing' | 'transcribing' | 'thinking' | 'synthesizing'

const started = ref(false)
const voiceHold = ref(false)
const active = ref(false)
const busy = ref(false)
const phase = ref<Phase>('idle')
const message = ref('')
type BilingualText = {
  turnId: number
  original: string
  language: string
  translation?: string
  lines?: { original: string; translation: string }[]
  interrupted?: boolean
}
const transcripts = ref<BilingualText[]>([])
const suggestions = ref<BilingualText[]>([])
const closedTurns = new Set<number>()
const transcriptPanel = ref<HTMLElement | null>(null)
const suggestionPanel = ref<HTMLElement | null>(null)
const suggestionResize = usePanelResize('sonara-practice-suggestion-height')
const questionResize = usePanelResize('sonara-practice-transcript-height')
const practiceSplitKey = 'sonara-practice-split'
const practiceSplit = ref(Number(localStorage.getItem(practiceSplitKey)) || 0)
const columnSplit = useColumnSplit(practiceSplit, () => {
  if (practiceSplit.value) localStorage.setItem(practiceSplitKey, String(practiceSplit.value))
  else localStorage.removeItem(practiceSplitKey)
})

const phaseLabel: Record<Phase, string> = {
  idle: 'Listo para arrancar',
  listening: 'Escuchando…',
  speaking: 'Hablando… podés interrumpir con tu voz',
  capturing: 'Voz detectada…',
  transcribing: 'Transcribiendo…',
  thinking: 'Preparando respuesta…',
  synthesizing: 'Preparando voz…',
}

let run = 0
const level = ref(0)
let micLevel = 0
let levelFrame = 0

function tickLevel() {
  const raw = phase.value === 'listening' || phase.value === 'capturing' ? micLevel * 6 : 0
  const target = Math.min(1, raw)
  // Rise fast, fall slowly: the waves follow the voice without flickering.
  level.value += (target - level.value) * (target > level.value ? 0.45 : 0.1)
  levelFrame = requestAnimationFrame(tickLevel)
}
levelFrame = requestAnimationFrame(tickLevel)
const offLevel = EventsOn('practice:level', (value: number) => {
  micLevel = value
})

async function start() {
  if (active.value || busy.value) return
  const current = ++run
  message.value = ''
  answerLive.value = ''
  voiceHold.value = false
  active.value = true
  started.value = true
  phase.value = 'listening'
  try {
    await PracticeRealtime()
  } catch (error) {
    if (current === run) message.value = String(error)
  } finally {
    if (current === run) {
      active.value = false
      phase.value = 'idle'
    }
  }
}

function stop() {
  run++
  voiceHold.value = false
  active.value = false
  const current = run
  busy.value = true
  phase.value = 'idle'
  void StopPractice()
    .catch((error) => {
      if (current === run) message.value = String(error)
    })
    .finally(() => {
      if (current === run) busy.value = false
    })
}

const offVoiceHold = EventsOn('practice:voice-hold', (hold: boolean) => {
  if (active.value) voiceHold.value = hold
})

// Your answer as it is being transcribed, kept on screen until the next one.
const answerLive = ref('')

const offPhase = EventsOn('practice:phase', (next: Phase) => {
  if (!active.value) return
  phase.value = next
  if (next === 'capturing') answerLive.value = ''
})
const offPartial = EventsOn('practice:partial', (text: string) => {
  if (active.value) answerLive.value = text
})
function updateHistory(entries: BilingualText[], panel: HTMLElement | null, text: BilingualText) {
  if (closedTurns.has(text.turnId)) return
  const follow = panel && panel.scrollHeight - panel.scrollTop - panel.clientHeight <= 72
  const index = entries.findIndex((entry) => entry.turnId === text.turnId)
  if (index < 0) entries.push(text)
  else entries[index] = text
  if (follow) {
    void nextTick(() => panel.scrollTo({ top: panel.scrollHeight }))
  }
}

const offQuestion = EventsOn('practice:question', (text: BilingualText) => {
  if (active.value || busy.value) updateHistory(transcripts.value, transcriptPanel.value, text)
})
const offSuggestion = EventsOn('practice:suggestion', (text: BilingualText) => {
  if (active.value || busy.value) updateHistory(suggestions.value, suggestionPanel.value, text)
})

const offInterrupted = EventsOn('practice:interrupted', (turnId: number) => {
  closedTurns.add(turnId)
  for (const entries of [transcripts.value, suggestions.value]) {
    const entry = entries.find((item) => item.turnId === turnId)
    if (entry) entry.interrupted = true
  }
})

onBeforeUnmount(() => {
  offVoiceHold()
  offQuestion()
  offSuggestion()
  offInterrupted()
  offPhase()
  offPartial()
  offLevel()
  cancelAnimationFrame(levelFrame)
  if (active.value || busy.value) stop()
})
</script>

<template>
  <div class="practice">
    <div class="content-header">
      <div>
        <p class="eyebrow">PRÁCTICA · ENTREVISTA HABLADA</p>
        <h1>Simulación con voz</h1>
        <p class="workspace-subtitle">
          Un entrevistador de voz te hace preguntas y responde en el idioma configurado; vos
          respondés hablando, sin tocar nada.
        </p>
      </div>
      <div class="content-actions">
        <button
          class="session-setup-trigger"
          title="Tipo de entrevista, empresa, puesto, objetivo y tu experiencia"
          @click="emit('open-session-setup')"
        >
          ＋ Contexto
        </button>
      </div>
    </div>

    <section class="practice-controls">
      <button
        class="voice-orb"
        :class="[phase, { started }]"
        :style="{ '--level': level.toFixed(3) }"
        :disabled="busy || active"
        :aria-label="started ? phaseLabel[phase] : 'Empezar práctica'"
        @click="start"
      >
        <span class="orb-rings" aria-hidden="true"><i></i><i></i><i></i></span>
        <span class="orb" aria-hidden="true">
          <i class="blob b1"></i><i class="blob b2"></i><i class="blob b3"></i><i class="gloss"></i>
        </span>
      </button>
      <p class="phase-label">
        {{ !started ? 'Tocá para empezar' : phaseLabel[phase] }}
      </p>
      <p v-if="answerLive" class="answer-live" role="status">
        <span>Tu respuesta</span>{{ answerLive }}
      </p>
      <small v-if="active && voiceHold" role="status"> Sigo escuchando… </small>
      <button v-if="started && active" class="end-button" @click="stop">Terminar práctica</button>
      <small v-if="message" class="meeting-message" role="alert">{{ message }}</small>
    </section>
    <div
      class="panels practice-results"
      :class="{ 'column-resizing': columnSplit.resizing.value }"
      :style="columnSplit.style.value"
    >
      <section
        class="panel response-panel resizable-panel"
        :class="{ resizing: suggestionResize.resizing.value }"
        :style="suggestionResize.style.value"
      >
        <div class="panel-heading">Respuesta sugerida</div>
        <div ref="suggestionPanel" class="transcript-body" role="log">
          <p v-if="!suggestions.length" class="transcript-text">
            Tu respuesta sugerida aparecerá acá.
          </p>
          <div v-for="entry in suggestions" :key="entry.turnId" class="practice-entry">
            <PracticeTurnText :entry="entry" />
          </div>
        </div>
        <div
          class="panel-resizer"
          role="separator"
          aria-label="Cambiar alto de la respuesta sugerida"
          aria-orientation="horizontal"
          tabindex="0"
          @pointerdown="suggestionResize.start"
          @pointermove="suggestionResize.move"
          @pointerup="suggestionResize.stop"
          @pointercancel="suggestionResize.stop"
          @lostpointercapture="suggestionResize.stop"
          @keydown.up.prevent="suggestionResize.nudge($event, -16)"
          @keydown.down.prevent="suggestionResize.nudge($event, 16)"
        ></div>
      </section>
      <section
        class="panel transcript-panel resizable-panel"
        :class="{ resizing: questionResize.resizing.value }"
        :style="questionResize.style.value"
      >
        <div class="panel-heading">Transcripción</div>
        <div ref="transcriptPanel" class="transcript-body" role="log">
          <p v-if="!transcripts.length" class="transcript-text">Esperando al simulador…</p>
          <div v-for="entry in transcripts" :key="entry.turnId" class="practice-entry">
            <PracticeTurnText :entry="entry" />
          </div>
        </div>
        <div
          class="panel-resizer"
          role="separator"
          aria-label="Cambiar alto de la transcripción"
          aria-orientation="horizontal"
          tabindex="0"
          @pointerdown="questionResize.start"
          @pointermove="questionResize.move"
          @pointerup="questionResize.stop"
          @pointercancel="questionResize.stop"
          @lostpointercapture="questionResize.stop"
          @keydown.up.prevent="questionResize.nudge($event, -16)"
          @keydown.down.prevent="questionResize.nudge($event, 16)"
        ></div>
      </section>
      <div
        class="column-resizer"
        :class="{ resizing: columnSplit.resizing.value }"
        role="separator"
        aria-label="Cambiar ancho de los paneles"
        aria-orientation="vertical"
        tabindex="0"
        :aria-valuenow="practiceSplit || 64"
        aria-valuemin="25"
        aria-valuemax="80"
        title="Arrastrá para cambiar el ancho; doble clic para restablecer"
        @pointerdown="columnSplit.start"
        @pointermove="columnSplit.move"
        @pointerup="columnSplit.stop"
        @pointercancel="columnSplit.stop"
        @lostpointercapture="columnSplit.stop"
        @dblclick="columnSplit.reset"
        @keydown.left.prevent="columnSplit.nudge(-2)"
        @keydown.right.prevent="columnSplit.nudge(2)"
      ></div>
    </div>
  </div>
</template>

<style scoped>
.practice {
  align-content: start;
  display: grid;
  gap: 16px;
}
.practice-controls {
  padding: 34px 24px 18px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 18px;
}
.practice-results .panel {
  height: clamp(180px, 36vh, 360px);
  min-height: 0;
}
.practice-results .panel-heading {
  flex-shrink: 0;
}
.practice-results .transcript-body {
  min-height: 0;
  overflow-y: auto;
}
.practice-entry {
  flex-shrink: 0;
}
.practice-entry + .practice-entry {
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid rgb(41 42 53 / var(--surface-opacity));
}
@media (max-width: 620px) {
  .practice-results {
    grid-template-columns: minmax(0, 1fr);
  }
  .practice-results .panel {
    grid-column: auto;
    grid-row: auto;
  }
}
.voice-orb {
  --level: 0;
  position: relative;
  margin: 26px 0 30px;
  width: 150px;
  height: 150px;
  padding: 0;
  border-radius: 50%;
  background: none;
  /* The halo lives here, not on .orb: .orb's clip-path would cut it off. */
  box-shadow: 0 0 calc(18px + var(--level) * 60px) rgb(85 214 176 / calc(0.18 + var(--level) * 0.5));
}
.voice-orb:not(:disabled):hover {
  box-shadow: 0 0 34px rgb(85 214 176 / 0.38);
}
.voice-orb:disabled {
  cursor: default;
}
.orb {
  position: absolute;
  inset: 0;
  overflow: hidden;
  border-radius: 50%;
  /* WebKitGTK leaks the blurred, blended blobs past a rounded overflow clip,
     drawing a faint square around the sphere; clip-path clips them for real. */
  clip-path: circle(50%);
  isolation: isolate;
  background: radial-gradient(circle at 50% 62%, #0f3a30 0%, #08201a 62%, #050f0c 100%);
  box-shadow:
    inset 0 0 26px rgb(0 0 0 / 0.65),
    inset 0 0 0 1px rgb(85 214 176 / 0.22);
  transform: scale(calc(1 + var(--level) * 0.07));
  transition: transform 0.08s linear;
}
.voice-orb:not(:disabled):hover .orb {
  box-shadow:
    inset 0 0 26px rgb(0 0 0 / 0.65),
    inset 0 0 0 1px rgb(85 214 176 / 0.4);
}
/* Lava: blurred green blobs drifting inside the sphere. */
.blob {
  position: absolute;
  width: 72%;
  height: 72%;
  border-radius: 50%;
  filter: blur(16px);
  mix-blend-mode: screen;
  animation: lava-a 9s ease-in-out infinite alternate;
}
.blob.b1 {
  top: -8%;
  left: -6%;
  background: radial-gradient(circle, #55d6b0 0%, rgb(85 214 176 / 0) 70%);
}
.blob.b2 {
  right: -10%;
  bottom: -12%;
  background: radial-gradient(circle, #1f9c7a 0%, rgb(31 156 122 / 0) 70%);
  animation-name: lava-b;
  animation-duration: 11s;
}
.blob.b3 {
  top: 30%;
  left: 24%;
  width: 46%;
  height: 46%;
  background: radial-gradient(circle, #b9f2df 0%, rgb(185 242 223 / 0) 70%);
  opacity: 0.55;
  animation-name: lava-c;
  animation-duration: 13s;
}
.gloss {
  position: absolute;
  top: 9%;
  left: 18%;
  width: 42%;
  height: 26%;
  border-radius: 50%;
  background: radial-gradient(ellipse, rgb(255 255 255 / 0.16), rgb(255 255 255 / 0) 70%);
}
.voice-orb:not(.started) .blob {
  opacity: 0.6;
}
.voice-orb.thinking .blob,
.voice-orb.transcribing .blob {
  animation-duration: 2.4s;
}
.voice-orb.capturing .blob,
.voice-orb.speaking .blob {
  animation-duration: 4s;
}
@keyframes lava-a {
  0% {
    transform: translate(0, 0) scale(1);
  }
  50% {
    transform: translate(34%, 22%) scale(1.25);
  }
  100% {
    transform: translate(12%, 46%) scale(0.9);
  }
}
@keyframes lava-b {
  0% {
    transform: translate(0, 0) scale(1.1);
  }
  50% {
    transform: translate(-36%, -18%) scale(0.85);
  }
  100% {
    transform: translate(-14%, -44%) scale(1.2);
  }
}
@keyframes lava-c {
  0% {
    transform: translate(0, 0) scale(0.9);
  }
  50% {
    transform: translate(-24%, 20%) scale(1.3);
  }
  100% {
    transform: translate(22%, -18%) scale(1);
  }
}
/* Ripples: always moving, as strong and as wide as the voice is loud. */
.orb-rings {
  position: absolute;
  inset: 0;
  opacity: calc(var(--level) * 1.1);
  transform: scale(calc(1 + var(--level) * 0.35));
  transition: transform 0.08s linear;
  pointer-events: none;
}
.orb-rings i {
  position: absolute;
  inset: 0;
  border: 2px solid rgb(85 214 176 / 0.7);
  border-radius: 50%;
  animation: ripple 1.8s ease-out infinite;
}
.orb-rings i:nth-child(2) {
  animation-delay: 0.6s;
}
.orb-rings i:nth-child(3) {
  animation-delay: 1.2s;
}
@keyframes ripple {
  from {
    opacity: 0.9;
    transform: scale(1);
  }
  to {
    opacity: 0;
    transform: scale(1.75);
  }
}
@media (prefers-reduced-motion: reduce) {
  .blob,
  .orb-rings i {
    animation: none;
  }
}
.answer-live {
  max-width: 640px;
  margin: 0;
  color: #c9cad3;
  font-size: 13px;
  line-height: 1.55;
  text-align: center;
}
.answer-live span {
  display: block;
  margin-bottom: 2px;
  color: #747583;
  font-size: 10px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.phase-label {
  margin: 0;
  color: #d7d8e0;
  font-size: 13px;
  font-weight: 600;
  letter-spacing: -0.01em;
}
.end-button {
  padding: 8px 14px;
  border-radius: 7px;
  color: #f5e1e5;
  background: rgb(101 56 69 / var(--surface-opacity));
  font-size: 11px;
  transition: background 0.15s ease;
}
.end-button:hover {
  background: rgb(122 70 85 / var(--surface-opacity));
}
.meeting-message {
  color: #f5e1e5;
  font-size: 12px;
  overflow-wrap: anywhere;
}
</style>
