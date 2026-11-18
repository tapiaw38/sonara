<script setup lang="ts">
import { ref } from 'vue'
import type { AudioSourceOption } from '../types'

defineProps<{
  view: 'copilot' | 'meeting'
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
  (event: 'open-settings'): void
  (event: 'toggle-always-on-top'): void
  (event: 'refresh-audio-sources'): void
  (event: 'select-audio-source', target: string): void
}>()

const compact = defineModel<boolean>('compact', { default: false })
const ghost = defineModel<boolean>('ghost', { default: false })
const opacityPercent = defineModel<number>('opacityPercent', { default: 22 })

const showAudioSources = ref(false)

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
      <button
        :class="{ active: view === 'copilot' && assistantMode === 'interview' }"
        @click="emit('select-mode', 'interview')"
      >
        En vivo
      </button>
      <button
        :class="{ active: view === 'copilot' && assistantMode === 'daily' }"
        @click="emit('select-mode', 'daily')"
      >
        Reunión
      </button>
      <button
        :class="{ active: view === 'copilot' && assistantMode === 'coding' }"
        @click="emit('select-mode', 'coding')"
      >
        Coding
      </button>
      <button
        :class="{ active: view === 'copilot' && assistantMode === 'coaching' }"
        @click="emit('select-mode', 'coaching')"
      >
        Coaching
      </button>
      <button :class="{ active: view === 'meeting' }" @click="emit('show-meeting')">
        Transcribir
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
