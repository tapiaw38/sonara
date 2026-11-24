<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { AudioSourceOption } from '../types'

const props = defineProps<{
  view: 'copilot' | 'meeting' | 'practice'
  assistantMode: string
  statusClass: string
  statusLabel: string
  alwaysOnTop: boolean
  audioGuessing: boolean
  audioCandidates: number
  audioSources: AudioSourceOption[]
  selectedAudioSource: string
  audioSourceMessage: string
}>()

const emit = defineEmits<{
  (event: 'select-mode', mode: string): void
  (event: 'show-meeting'): void
  (event: 'show-practice'): void
  (event: 'open-settings'): void
  (event: 'open-consumption'): void
  (event: 'toggle-always-on-top'): void
  (event: 'refresh-audio-sources'): void
  (event: 'select-audio-source', target: string): void
}>()

const compact = defineModel<boolean>('compact', { default: false })
const ghost = defineModel<boolean>('ghost', { default: false })
const opacityPercent = defineModel<number>('opacityPercent', { default: 22 })

const showAudioSources = ref(false)

// The four copilot modes live behind one tab: the bar stays short and the
// tab still says which mode is on.
const copilotModes = [
  { id: 'interview', label: 'En vivo', hint: 'Respuestas para tu entrevista' },
  { id: 'daily', label: 'Reunión', hint: 'Ayuda en reuniones del día a día' },
  { id: 'coding', label: 'Coding', hint: 'Problemas técnicos y de código' },
  { id: 'coaching', label: 'Coaching', hint: 'Feedback sobre tus respuestas' },
]
const currentMode = computed(
  () => copilotModes.find((mode) => mode.id === props.assistantMode) ?? copilotModes[0],
)
const modeMenu = ref(false)
const modeMenuEl = ref<HTMLElement | null>(null)

function chooseMode(id: string) {
  modeMenu.value = false
  emit('select-mode', id)
}

function closeModeMenu(event: Event) {
  if (
    event instanceof KeyboardEvent
      ? event.key === 'Escape'
      : !modeMenuEl.value?.contains(event.target as Node)
  )
    modeMenu.value = false
}
onMounted(() => {
  document.addEventListener('pointerdown', closeModeMenu)
  document.addEventListener('keydown', closeModeMenu)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', closeModeMenu)
  document.removeEventListener('keydown', closeModeMenu)
})

function setOpacity(value: string | number) {
  opacityPercent.value = Number(value)
  localStorage.setItem('sonara-opacity', String(opacityPercent.value))
}

function toggleAudioSources() {
  showAudioSources.value = !showAudioSources.value
  if (showAudioSources.value) emit('refresh-audio-sources')
}
</script>

<template>
  <header class="topbar">
    <div class="brand">
      <img class="brand-mark" src="/sonara-logo.png" alt="" /><span>SONARA</span>
    </div>
    <nav class="product-nav" aria-label="Modo de trabajo">
      <div ref="modeMenuEl" class="nav-copilot" :class="{ active: view === 'copilot' }">
        <button class="nav-copilot-main" @click="chooseMode(currentMode.id)">
          Copiloto <span class="nav-mode">{{ currentMode.label }}</span>
        </button>
        <button
          class="nav-copilot-toggle"
          :aria-expanded="modeMenu"
          aria-haspopup="menu"
          title="Cambiar modo del copiloto"
          @click="modeMenu = !modeMenu"
        >
          ▾
        </button>
        <div v-if="modeMenu" class="mode-menu" role="menu">
          <button
            v-for="mode in copilotModes"
            :key="mode.id"
            role="menuitemradio"
            :aria-checked="view === 'copilot' && mode.id === assistantMode"
            :class="{ current: mode.id === currentMode.id }"
            @click="chooseMode(mode.id)"
          >
            <strong>{{ mode.label }}</strong><small>{{ mode.hint }}</small>
          </button>
        </div>
      </div>
      <button :class="{ active: view === 'meeting' }" @click="emit('show-meeting')">
        Transcribir
      </button>
      <button :class="{ active: view === 'practice' }" @click="emit('show-practice')">
        Práctica
      </button>
    </nav>
    <div class="meeting-state">
      <span class="status-dot" :class="statusClass"></span>{{ statusLabel }}
    </div>
    <div class="overlay-controls">
      <button
        class="overlay-button settings-button"
        title="Configuración"
        aria-label="Configuración"
        @click="emit('open-settings')"
      >
        ⚙
      </button>
      <button
        class="overlay-button"
        title="Consumo"
        aria-label="Consumo"
        @click="emit('open-consumption')"
      >
        <svg
          width="15"
          height="15"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          aria-hidden="true"
        >
          <path d="M4 4v16h16M8 16v-4m4 4V8m4 8V5" />
        </svg>
      </button>
      <button
        class="overlay-button"
        :class="{ active: compact }"
        title="Modo compacto"
        @click="compact = !compact"
      >
        ▣
      </button>
      <button
        class="overlay-button"
        :class="{ active: ghost }"
        title="Modo casi invisible"
        @click="ghost = !ghost"
      >
        ◌
      </button>
      <label v-if="ghost" class="opacity-control" title="Opacidad"
        ><input
          type="range"
          min="5"
          max="100"
          :value="opacityPercent"
          @input="setOpacity(($event.target as HTMLInputElement).value)"
        /><span>{{ opacityPercent }}%</span></label
      >
      <button
        class="overlay-button"
        :class="{ active: alwaysOnTop }"
        title="Siempre encima"
        @click="emit('toggle-always-on-top')"
      >
        ↥
      </button>
      <button
        class="overlay-button audio-source-top-control"
        :class="{ guessing: audioGuessing }"
        :title="
          audioGuessing
            ? `Fuente de audio: adivinando entre ${audioCandidates} posibles, revisá`
            : 'Fuente de audio'
        "
        aria-label="Elegir fuente de audio"
        @click="toggleAudioSources"
      >
        ♫
      </button>
    </div>
    <div v-if="showAudioSources" class="audio-source-popover">
      <label for="audio-source-popover-select">Fuente de audio</label>
      <div class="audio-source-controls">
        <select
          id="audio-source-popover-select"
          :value="selectedAudioSource"
          @change="emit('select-audio-source', ($event.target as HTMLSelectElement).value)"
        >
          <option value="">Automática</option>
          <option v-for="source in audioSources" :key="source.target" :value="source.target">
            {{ source.label }} · #{{ source.target }}{{ source.running ? ' · activo' : '' }}
          </option>
        </select>
        <button
          title="Actualizar fuentes"
          aria-label="Actualizar fuentes de audio"
          @click="emit('refresh-audio-sources')"
        >
          ↻
        </button>
      </div>
      <small v-if="audioGuessing" class="audio-source-hint guessing"
        >Hay {{ audioCandidates }} fuentes de audio posibles; Sonara eligió una adivinando. Revisá
        si es la correcta.</small
      >
      <small v-if="audioSourceMessage" class="audio-source-hint">{{ audioSourceMessage }}</small>
    </div>
  </header>
</template>
