<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  AIEndpoint,
  AudioSources,
  ClaimWindowFocus,
  DeleteSession,
  GetSessionProfile,
  ListeningStatus,
  NewSession,
  OpenSession,
  SaveSessionProfile,
  SetAssistantMode,
  SetAudioLanguage,
  SetAudioSource,
  Sessions,
  ToggleAlwaysOnTop,
} from '../wailsjs/go/main/App'
import { WindowSetAlwaysOnTop } from '../wailsjs/runtime/runtime'
import CopilotView from './views/CopilotView.vue'
import MeetingView from './views/MeetingView.vue'
import PracticeView from './views/PracticeView.vue'
import TopBar from './components/TopBar.vue'
import SessionSidebar from './components/SessionSidebar.vue'
import SettingsDialog from './components/SettingsDialog.vue'
import SessionSetupDialog from './components/SessionSetupDialog.vue'
import type { AudioSourceOption, SessionRow } from './types'

const copilotRef = ref<InstanceType<typeof CopilotView> | null>(null)

// 'meeting' is the transcription view; the four copilot modes share 'copilot'.
// It is UI-only: the copilot keeps its own mode and session underneath.
const view = ref<'copilot' | 'meeting' | 'practice'>('copilot')
const meetingSessionID = ref('')
const storedMode = localStorage.getItem('sonara-mode') || 'interview'
const assistantMode = ref(
  ['interview', 'daily', 'coding', 'coaching'].includes(storedMode) ? storedMode : 'interview',
)
const assistantModeLabel = computed(
  () =>
    ({ interview: 'Entrevista', daily: 'Reunión', coding: 'Coding', coaching: 'Coaching' })[
      assistantMode.value
    ] || 'Entrevista',
)

const endpoint = ref('')
const showSettings = ref(false)
const showSessionSetup = ref(false)
const sessionSetupMessage = ref('')
const sessionScenario = ref('General')
const sessionCompany = ref('')
const sessionRole = ref('')
const sessionObjective = ref('')
const sessionNotes = ref('')
const compact = ref(false)
const ghost = ref(false)
const alwaysOnTop = ref(false)
const opacityPercent = ref(Number(localStorage.getItem('sonara-opacity') || 22))
const savedSidebarWidth = Number(localStorage.getItem('sonara-sidebar-width'))
const sidebarWidth = ref(
  Number.isFinite(savedSidebarWidth) && savedSidebarWidth >= 160
    ? Math.min(savedSidebarWidth, 420)
    : 225,
)
const sidebarResizing = ref(false)
const panelsResizing = ref(false)
const codeFontSize = ref(Number(localStorage.getItem('sonara-code-font-size') || 12))
const listening = ref(false)
const startedAt = ref('—')
const captureStatus = ref('Sin captura pendiente')
const analyzingCapture = ref(false)
const audioGuessing = ref(false)
const audioCandidates = ref(0)
const audioSources = ref<AudioSourceOption[]>([])
const selectedAudioSource = ref('')
const audioSourceMessage = ref('')
const audioLanguage = ref(localStorage.getItem('sonara-audio-language') === 'en' ? 'en' : 'es')

const statusLabel = computed(() => (listening.value ? 'Escuchando' : 'En pausa'))
const statusClass = computed(() => (listening.value ? 'is-live' : 'is-idle'))

async function setAudioLanguage(language: string) {
  audioLanguage.value = await SetAudioLanguage(language)
  localStorage.setItem('sonara-audio-language', audioLanguage.value)
}

async function refreshAudioSources() {
  try {
    audioSources.value = await AudioSources()
    if (
      selectedAudioSource.value &&
      !audioSources.value.some((source) => source.target === selectedAudioSource.value)
    ) {
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

async function selectMode(mode: string) {
  view.value = 'copilot'
  await setMode(mode)
}

const legacySessionProfilePrefix = 'sonara-session-profile:'
function legacySessionProfileKey(id: string) {
  return `${legacySessionProfilePrefix}${id}`
}

async function activateSessionProfile(id: string) {
  sessionCompany.value = ''
  sessionRole.value = ''
  sessionObjective.value = ''
  sessionNotes.value = ''
  sessionScenario.value = 'General'
  if (id) {
    try {
      const saved = await GetSessionProfile(id)
      sessionScenario.value = saved.scenario || 'General'
      sessionCompany.value = saved.company || ''
      sessionRole.value = saved.role || ''
      sessionObjective.value = saved.objective || ''
      sessionNotes.value = saved.notes || ''
    } catch {
      // A session can be created before its first database write during startup.
    }
  }
}

async function migrateLegacySessionProfiles() {
  const keys = Array.from({ length: localStorage.length }, (_, index) =>
    localStorage.key(index),
  ).filter((key): key is string => Boolean(key?.startsWith(legacySessionProfilePrefix)))
  for (const key of keys) {
    const id = key.slice(legacySessionProfilePrefix.length)
    try {
      const profile = JSON.parse(localStorage.getItem(key) || '{}')
      await SaveSessionProfile(id, profile)
      localStorage.removeItem(key)
    } catch (error) {
      console.warn(
        `No se pudo migrar contexto de sesión ${id}; se conserva en localStorage para reintentar.`,
        error,
      )
    }
  }
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
    if (!currentSessionID.value) throw new Error('No hay sesión activa')
    await SaveSessionProfile(currentSessionID.value, profile)
    sessionSetupMessage.value = 'Contexto aplicado a esta sesión.'
  } catch {
    sessionSetupMessage.value = 'No se pudo aplicar el contexto.'
  }
}

const sessions = ref<SessionRow[]>([])
const currentSessionID = ref('')
const sessionMessage = ref('')

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
  const row = sessions.value.find((item) => item.id === id)
  // A saved meeting is read, not resumed: opening it must not replace the
  // copilot's conversation, so it never goes through OpenSession.
  if (row?.mode === 'meeting') {
    meetingSessionID.value = id
    view.value = 'meeting'
    return
  }
  if (id === currentSessionID.value) return
  view.value = 'copilot'
  copilotRef.value?.beginSessionSwitch()
  try {
    const opened = await OpenSession(id)
    const selected = sessions.value.find((item) => item.id === id)
    sessions.value = opened.sessions
    await copilotRef.value?.restoreOpenedSession(opened)
    currentSessionID.value = id
    if (selected?.mode) await setMode(selected.mode)
    await activateSessionProfile(id)
    sessionMessage.value = 'Sesión reabierta. El contexto volvió al prompt.'
  } catch (error) {
    sessionMessage.value = String(error || 'No se pudo abrir la sesión')
  }
}

async function removeSession(id: string) {
  try {
    sessions.value = await DeleteSession(id)
    localStorage.removeItem(legacySessionProfileKey(id))
    sessionMessage.value = 'Sesión borrada.'
  } catch (error) {
    sessionMessage.value = String(error || 'No se pudo borrar la sesión')
  }
}

async function newSession() {
  copilotRef.value?.beginSessionSwitch()
  await NewSession()
  await copilotRef.value?.resetAfterNewSession()
  currentSessionID.value = (await ListeningStatus()).sessionId
  await activateSessionProfile(currentSessionID.value)
  await refreshSessions()
}

async function loadState() {
  try {
    endpoint.value = await AIEndpoint()
    await setAudioLanguage(audioLanguage.value)
    await setMode(assistantMode.value)
    const state = await ListeningStatus()
    selectedAudioSource.value = state.audioSource || ''
    currentSessionID.value = state.sessionId
    await migrateLegacySessionProfiles()
    await activateSessionProfile(currentSessionID.value)
    await refreshAudioSources()
    await refreshSessions()
  } catch {
    endpoint.value = 'Servicio local no disponible'
  }
}

window.addEventListener('mouseenter', () => {
  void ClaimWindowFocus()
})

loadState()

async function toggleAlwaysOnTop() {
  const target = !alwaysOnTop.value
  WindowSetAlwaysOnTop(target)
  if (await ToggleAlwaysOnTop()) {
    alwaysOnTop.value = target
  }
}
</script>

<template>
  <main
    class="shell"
    :class="{ compact, ghost, 'is-analyzing': analyzingCapture }"
    :style="{
      '--surface-opacity': ghost ? opacityPercent / 100 : 1,
      '--code-font-size': `${codeFontSize}px`,
    }"
  >
    <TopBar
      :view="view"
      :assistant-mode="assistantMode"
      :status-class="statusClass"
      :status-label="statusLabel"
      :always-on-top="alwaysOnTop"
      :audio-guessing="audioGuessing"
      :audio-candidates="audioCandidates"
      :audio-sources="audioSources"
      :selected-audio-source="selectedAudioSource"
      :audio-source-message="audioSourceMessage"
      v-model:compact="compact"
      v-model:ghost="ghost"
      v-model:opacity-percent="opacityPercent"
      @select-mode="selectMode"
      @show-meeting="view = 'meeting'"
      @show-practice="view = 'practice'"
      @open-settings="showSettings = true"
      @toggle-always-on-top="toggleAlwaysOnTop"
      @refresh-audio-sources="refreshAudioSources"
      @select-audio-source="selectAudioSource"
    />

    <section
      class="workspace"
      :class="{ 'resizing-sidebar': sidebarResizing, 'resizing-panels': panelsResizing }"
      :style="{ '--sidebar-width': `${sidebarWidth}px` }"
    >
      <SessionSidebar
        :assistant-mode-label="assistantModeLabel"
        :started-at="startedAt"
        :sessions="sessions"
        :current-session-id="currentSessionID"
        :view="view"
        :meeting-session-id="meetingSessionID"
        :session-message="sessionMessage"
        v-model:sidebar-width="sidebarWidth"
        v-model:resizing="sidebarResizing"
        @new-session="newSession"
        @open-session="openSession"
        @remove-session="removeSession"
        @refresh-sessions="refreshSessions"
      />

      <div class="content">
        <MeetingView
          v-if="view === 'meeting'"
          :session-id="meetingSessionID"
          @opened="meetingSessionID = $event"
          @sessions-changed="refreshSessions"
        />
        <PracticeView v-if="view === 'practice'" @open-session-setup="showSessionSetup = true" />
        <div v-show="view === 'copilot'" style="display: contents">
          <CopilotView
            ref="copilotRef"
            :assistant-mode="assistantMode"
            v-model:listening="listening"
            v-model:started-at="startedAt"
            v-model:capture-status="captureStatus"
            v-model:analyzing-capture="analyzingCapture"
            v-model:audio-guessing="audioGuessing"
            v-model:audio-candidates="audioCandidates"
            v-model:code-font-size="codeFontSize"
            v-model:panels-resizing="panelsResizing"
            @open-session-setup="showSessionSetup = true"
          />
        </div>

        <footer class="footer">
          <span v-if="view === 'copilot'">{{ captureStatus }} · Ctrl+Alt+C</span
          ><span class="footer-right"
            ><span
              class="status-dot"
              :class="endpoint.includes('Falta') ? 'is-idle' : 'is-live'"
            ></span>
            IA: {{ endpoint || 'sin conectar' }}</span
          >
        </footer>
      </div>
    </section>

    <SettingsDialog
      v-model:show="showSettings"
      v-model:endpoint="endpoint"
      :audio-sources="audioSources"
      :selected-audio-source="selectedAudioSource"
      :audio-guessing="audioGuessing"
      :audio-candidates="audioCandidates"
      :audio-source-message="audioSourceMessage"
      :audio-language="audioLanguage"
      @refresh-audio-sources="refreshAudioSources"
      @select-audio-source="selectAudioSource"
      @set-audio-language="setAudioLanguage"
    />

    <SessionSetupDialog
      v-model:show="showSessionSetup"
      v-model:session-scenario="sessionScenario"
      v-model:session-company="sessionCompany"
      v-model:session-role="sessionRole"
      v-model:session-objective="sessionObjective"
      v-model:session-notes="sessionNotes"
      :session-setup-message="sessionSetupMessage"
      @save="saveSessionSetup"
    />
  </main>
</template>
