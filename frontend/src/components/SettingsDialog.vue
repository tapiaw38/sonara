<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import {
  GetAzureOpenAISettings,
  GetCapabilityProviders,
  SetAzureOpenAIConfig,
  SetDeepSeekAPIKey,
  SetGeminiAPIKey,
  SetLiveTranscribeMode,
  SetLiveTranscribeProvider,
  SetMeetingProvider,
  SetOpenAIAPIKey,
  SetTextProvider,
  SetVisionProvider,
  AIEndpoint,
  ProviderStatus,
} from '../../wailsjs/go/app/App'
import type { AudioSourceOption } from '../types'

defineProps<{
  audioSources: AudioSourceOption[]
  selectedAudioSource: string
  audioGuessing: boolean
  audioCandidates: number
  audioSourceMessage: string
  audioLanguage: string
}>()

const emit = defineEmits<{
  (event: 'refresh-audio-sources'): void
  (event: 'select-audio-source', target: string): void
  (event: 'set-audio-language', language: string): void
}>()

const show = defineModel<boolean>('show', { default: false })
const endpoint = defineModel<string>('endpoint', { default: '' })

const apiKey = ref('')
const apiKeyMessage = ref('')
const savingApiKey = ref(false)

const deepseekKey = ref('')
const deepseekMessage = ref('')

const azureEndpoint = ref('')
const azureChatApiVersion = ref('')
const azureTranscribeApiVersion = ref('')
const azureChatDeployment = ref('')
const azureTranscribeDeployment = ref('')
const azureLiveDeployment = ref('')
const azureKey = ref('')
const azureHasKey = ref(false)
const azureMessage = ref('')
const savingAzure = ref(false)

const geminiKey = ref('')
const geminiMessage = ref('')

// Tabs: what changes often (Uso) first; keys are set once (Claves).
type SettingsTab = 'uso' | 'claves' | 'audio'
const tabs: { id: SettingsTab; label: string }[] = [
  { id: 'uso', label: 'Uso' },
  { id: 'claves', label: 'Claves' },
  { id: 'audio', label: 'Audio' },
]
const tabStorageKey = 'sonara.settingsTab'
const storedTab = localStorage.getItem(tabStorageKey)
const tab = ref<SettingsTab>(
  tabs.some((t) => t.id === storedTab) ? (storedTab as SettingsTab) : 'uso',
)
watch(tab, (value) => localStorage.setItem(tabStorageKey, value))

// Which providers have a key, so the dropdowns can flag the missing ones.
const ready = ref({
  openai: false,
  deepseek: false,
  azure: false,
  azureTranscribe: false,
  gemini: false,
})
async function loadStatus() {
  try {
    ready.value = await ProviderStatus()
  } catch {
    // Leave every provider unflagged rather than guess.
  }
}
function missing(provider: string, transcribe = false): string {
  const ok =
    provider === 'local' ||
    (provider === 'azure' && transcribe
      ? ready.value.azureTranscribe
      : ready.value[provider as 'openai' | 'deepseek' | 'azure'])
  return ok ? '' : ' (falta configurar)'
}

// One explicit provider per capability, no fallback chain — see
// providers_settings.go. Each dropdown change takes effect immediately.
const providerText = ref('openai')
const providerVision = ref('openai')
const providerLiveTranscribe = ref('openai')
const providerMeeting = ref('openai')
const liveMode = ref('batch')
const providersMessage = ref('')

async function loadAzureSettings() {
  try {
    const form = await GetAzureOpenAISettings()
    azureEndpoint.value = form.endpoint
    azureChatApiVersion.value = form.chatApiVersion
    azureTranscribeApiVersion.value = form.transcribeApiVersion
    azureChatDeployment.value = form.chatDeployment
    azureTranscribeDeployment.value = form.transcribeDeployment
    azureLiveDeployment.value = form.liveDeployment
    azureHasKey.value = form.hasKey
  } catch {
    // Settings dialog just shows blank fields; not worth a user-facing error.
  }
}

async function loadProviders() {
  try {
    const providers = await GetCapabilityProviders()
    providerText.value = providers.text
    providerVision.value = providers.vision
    providerLiveTranscribe.value = providers.liveTranscribe
    providerMeeting.value = providers.meeting
    liveMode.value = providers.liveMode || 'batch'
  } catch {
    // Keep the "openai" defaults the refs already have.
  }
}

async function saveApiKey() {
  if (savingApiKey.value) return
  savingApiKey.value = true
  apiKeyMessage.value = ''
  try {
    const removing = !apiKey.value
    await SetOpenAIAPIKey(apiKey.value)
    apiKey.value = ''
    await loadStatus()
    await refreshEndpoint()
    apiKeyMessage.value = removing
      ? 'API key eliminada de esta sesión'
      : 'API key configurada para esta sesión'
  } catch (error) {
    apiKeyMessage.value = String(error || 'No se pudo guardar la API key')
  } finally {
    savingApiKey.value = false
  }
}

function removeApiKey() {
  apiKey.value = ''
  void saveApiKey()
}

async function saveDeepSeekKey() {
  if (savingApiKey.value) return
  savingApiKey.value = true
  deepseekMessage.value = ''
  try {
    const removing = !deepseekKey.value
    await SetDeepSeekAPIKey(deepseekKey.value)
    deepseekKey.value = ''
    await loadStatus()
    await refreshEndpoint()
    deepseekMessage.value = removing
      ? 'Clave quitada.'
      : 'Clave guardada. Elegí DeepSeek en "Proveedores" para usarla.'
  } catch (error) {
    deepseekMessage.value = String(error || 'No se pudo guardar la clave')
  } finally {
    savingApiKey.value = false
  }
}

function removeDeepSeekKey() {
  deepseekKey.value = ''
  void saveDeepSeekKey()
}

async function saveAzureConfig(clearKey: boolean) {
  if (savingAzure.value) return
  savingAzure.value = true
  azureMessage.value = ''
  try {
    await SetAzureOpenAIConfig(
      azureEndpoint.value,
      azureChatApiVersion.value,
      azureTranscribeApiVersion.value,
      azureChatDeployment.value,
      azureTranscribeDeployment.value,
      azureLiveDeployment.value,
      azureKey.value,
      clearKey,
    )
    azureKey.value = ''
    await loadAzureSettings() // reload hasKey from the backend rather than guess it here
    await loadStatus()
    await refreshEndpoint()
    azureMessage.value = clearKey
      ? 'Clave quitada.'
      : 'Guardado. Elegí Azure en "Proveedores" para usarlo.'
  } catch (error) {
    azureMessage.value = String(error || 'No se pudo guardar')
  } finally {
    savingAzure.value = false
  }
}

async function setLiveMode(mode: string) {
  providersMessage.value = ''
  try {
    await SetLiveTranscribeMode(mode)
    liveMode.value = mode
  } catch (error) {
    providersMessage.value = String(error || 'No se pudo cambiar el modo')
    await loadProviders()
  }
}

// The footer's "IA: …" names the text provider and whether it is set up.
async function refreshEndpoint() {
  endpoint.value = await AIEndpoint()
}

async function setProvider(
  capability: 'text' | 'vision' | 'liveTranscribe' | 'meeting',
  name: string,
) {
  providersMessage.value = ''
  try {
    if (capability === 'text') {
      await SetTextProvider(name)
      providerText.value = name
    } else if (capability === 'vision') {
      await SetVisionProvider(name)
      providerVision.value = name
    } else if (capability === 'liveTranscribe') {
      await SetLiveTranscribeProvider(name)
      providerLiveTranscribe.value = name
    } else {
      await SetMeetingProvider(name)
      providerMeeting.value = name
    }
    await refreshEndpoint()
  } catch (error) {
    providersMessage.value = String(error || 'No se pudo cambiar el proveedor')
    await loadProviders() // revert the dropdown to what is actually active
  }
}

async function saveGeminiKey() {
  if (savingApiKey.value) return
  savingApiKey.value = true
  geminiMessage.value = ''
  try {
    const removing = !geminiKey.value
    await SetGeminiAPIKey(geminiKey.value)
    geminiKey.value = ''
    await loadStatus()
    geminiMessage.value = removing
      ? 'Clave quitada: las reuniones ya no separan participantes.'
      : 'Clave guardada: al terminar una reunión se intenta separar a los participantes.'
  } catch (error) {
    geminiMessage.value = String(error || 'No se pudo guardar la clave')
  } finally {
    savingApiKey.value = false
  }
}

function removeGeminiKey() {
  geminiKey.value = ''
  void saveGeminiKey()
}

onMounted(async () => {
  await Promise.all([loadAzureSettings(), loadProviders(), loadStatus()])
})
</script>

<template>
  <div v-if="show" class="modal-overlay" @click.self="show = false" @keydown.esc="show = false">
    <section
      class="settings-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-title"
    >
      <header class="dialog-header">
        <div>
          <p class="eyebrow">SONARA</p>
          <h2 id="settings-title">Configuración</h2>
        </div>
        <button class="dialog-close" aria-label="Cerrar configuración" @click="show = false">
          ×
        </button>
      </header>
      <nav class="settings-tabs" role="tablist" aria-label="Secciones de configuración">
        <button
          v-for="item in tabs"
          :id="`settings-tab-${item.id}`"
          :key="item.id"
          role="tab"
          :aria-selected="tab === item.id"
          :aria-controls="`settings-panel-${item.id}`"
          :class="{ active: tab === item.id }"
          @click="tab = item.id"
        >
          {{ item.label }}
        </button>
      </nav>
      <div
        v-show="tab === 'uso'"
        :id="'settings-panel-uso'"
        class="settings-section"
        role="tabpanel"
        :aria-labelledby="'settings-tab-uso'"
      >
        <h3>Proveedores</h3>
        <small class="provider-hint"
          >Un proveedor exacto por capacidad. Si falla, se muestra el error — nunca se prueba otro
          en silencio.</small
        >
        <label for="provider-text">Texto (copiloto)</label>
        <select
          id="provider-text"
          :value="providerText"
          @change="setProvider('text', ($event.target as HTMLSelectElement).value)"
        >
          <option value="openai">OpenAI{{ missing('openai') }}</option>
          <option value="deepseek">DeepSeek{{ missing('deepseek') }}</option>
          <option value="azure">Azure OpenAI{{ missing('azure') }}</option>
        </select>
        <label for="provider-vision">Visión (capturas de pantalla)</label>
        <select
          id="provider-vision"
          :value="providerVision"
          @change="setProvider('vision', ($event.target as HTMLSelectElement).value)"
        >
          <option value="openai">OpenAI{{ missing('openai') }}</option>
          <option value="deepseek">DeepSeek (no soporta visión){{ missing('deepseek') }}</option>
          <option value="azure">Azure OpenAI{{ missing('azure') }}</option>
        </select>
        <label for="provider-live-transcribe">Transcripción en vivo (copiloto)</label>
        <select
          id="provider-live-transcribe"
          :value="providerLiveTranscribe"
          @change="setProvider('liveTranscribe', ($event.target as HTMLSelectElement).value)"
        >
          <option value="openai">OpenAI{{ missing('openai') }}</option>
          <option value="deepseek">DeepSeek{{ missing('deepseek') }}</option>
          <option value="azure">Azure OpenAI{{ missing('azure', true) }}</option>
          <option value="local">Whisper local (gratis)</option>
        </select>
        <label for="provider-meeting">Transcripción de reuniones</label>
        <select
          id="provider-meeting"
          :value="providerMeeting"
          @change="setProvider('meeting', ($event.target as HTMLSelectElement).value)"
        >
          <option value="openai">OpenAI{{ missing('openai') }}</option>
          <option value="deepseek">DeepSeek{{ missing('deepseek') }}</option>
          <option value="azure">Azure OpenAI{{ missing('azure', true) }}</option>
          <option value="local">Whisper local (gratis)</option>
        </select>
        <label for="live-mode">Modo de transcripción en vivo (copiloto)</label>
        <select
          id="live-mode"
          :value="liveMode"
          @change="setLiveMode(($event.target as HTMLSelectElement).value)"
        >
          <option value="batch">Por lotes (grabar, subir, transcribir)</option>
          <option value="stream">
            En vivo (OpenAI o Azure, experimental): texto mientras hablan
          </option>
        </select>
        <small class="provider-hint"
          >En vivo requiere OpenAI o Azure OpenAI como proveedor de transcripción en vivo (Azure usa
          su deployment de transcripción en vivo, o el de transcripción), envía audio a 24 kHz y no
          admite el modo manual.</small
        >
        <small v-if="providersMessage" role="status" class="provider-error">{{
          providersMessage
        }}</small>
      </div>
      <div
        v-show="tab === 'claves'"
        :id="'settings-panel-claves'"
        class="settings-section"
        role="tabpanel"
        :aria-labelledby="'settings-tab-claves'"
      >
        <small class="provider-hint"
          >Se guardan localmente. Configurá una vez; después elegí el proveedor en "Uso".</small
        >
        <details class="key-group">
          <summary>
            OpenAI
            <span :class="['key-state', ready.openai ? 'ok' : 'missing']">{{
              ready.openai ? '✓ configurado' : 'sin configurar'
            }}</span>
          </summary>
          <form class="api-key-form" @submit.prevent="saveApiKey">
            <label for="openai-key">API key</label
            ><input
              id="openai-key"
              v-model="apiKey"
              type="password"
              autocomplete="off"
              spellcheck="false"
              placeholder="sk-…"
            /><small>Se guarda localmente en la configuración de Sonara.</small>
            <div class="api-key-actions">
              <button type="submit" :disabled="savingApiKey || !apiKey">
                {{ savingApiKey ? 'Guardando…' : 'Guardar clave' }}</button><button
                type="button"
                :disabled="savingApiKey"
                @click="removeApiKey"
              >
                Quitar
              </button>
            </div>
            <small v-if="apiKeyMessage" role="status">{{ apiKeyMessage }}</small><small
              class="endpoint-status"
              >{{ endpoint || 'Sin conectar' }}</small
            >
          </form>
        </details>
        <details class="key-group">
          <summary>
            Azure OpenAI
            <span :class="['key-state', ready.azure ? 'ok' : 'missing']">{{
              ready.azure ? '✓ configurado' : 'sin configurar'
            }}</span>
          </summary>
          <form class="api-key-form" @submit.prevent="saveAzureConfig(false)">
            <label for="azure-endpoint">Endpoint</label
            ><input
              id="azure-endpoint"
              v-model="azureEndpoint"
              autocomplete="off"
              spellcheck="false"
              placeholder="https://tu-recurso.openai.azure.com"
            />
            <label for="azure-chat-api-version">Versión de API (chat/visión)</label
            ><input
              id="azure-chat-api-version"
              v-model="azureChatApiVersion"
              autocomplete="off"
              spellcheck="false"
              placeholder="vacío = 2025-04-01-preview"
            />
            <label for="azure-transcribe-api-version">Versión de API (transcripción)</label
            ><input
              id="azure-transcribe-api-version"
              v-model="azureTranscribeApiVersion"
              autocomplete="off"
              spellcheck="false"
              placeholder="vacío = 2025-03-01-preview"
            />
            <label for="azure-chat-deployment">Deployment de chat/visión</label
            ><input
              id="azure-chat-deployment"
              v-model="azureChatDeployment"
              autocomplete="off"
              spellcheck="false"
              placeholder="Nombre del deployment"
            />
            <label for="azure-transcribe-deployment">Deployment de transcripción</label
            ><input
              id="azure-transcribe-deployment"
              v-model="azureTranscribeDeployment"
              autocomplete="off"
              spellcheck="false"
              placeholder="Nombre del deployment (opcional)"
            />
            <label for="azure-live-deployment">Deployment de transcripción en vivo</label
            ><input
              id="azure-live-deployment"
              v-model="azureLiveDeployment"
              autocomplete="off"
              spellcheck="false"
              placeholder="gpt-live-transcribe (vacío = el de transcripción)"
            />
            <label for="azure-key">API key</label
            ><input
              id="azure-key"
              v-model="azureKey"
              type="password"
              autocomplete="off"
              spellcheck="false"
              :placeholder="
                azureHasKey ? 'Ya guardada — dejar en blanco para mantenerla' : 'Clave del recurso'
              "
            />
            <small
              >Mismo recurso que ya usás para transcribir. La visión sólo funciona si el deployment
              de chat tiene un modelo con visión (GPT-4o o similar). Nada de esto se manda a
              OpenAI/DeepSeek/Gemini.</small
            >
            <div class="api-key-actions">
              <button type="submit" :disabled="savingAzure">
                {{ savingAzure ? 'Guardando…' : 'Guardar' }}</button><button
                type="button"
                :disabled="savingAzure || !azureHasKey"
                @click="saveAzureConfig(true)"
              >
                Quitar clave
              </button>
            </div>
            <small v-if="azureMessage" role="status">{{ azureMessage }}</small>
          </form>
        </details>
        <details class="key-group">
          <summary>
            DeepSeek
            <span :class="['key-state', ready.deepseek ? 'ok' : 'missing']">{{
              ready.deepseek ? '✓ configurado' : 'sin configurar'
            }}</span>
          </summary>
          <form class="api-key-form" @submit.prevent="saveDeepSeekKey">
            <label for="deepseek-key">API key</label
            ><input
              id="deepseek-key"
              v-model="deepseekKey"
              type="password"
              autocomplete="off"
              spellcheck="false"
              placeholder="sk-…"
            /><small
              >Modelo más barato para texto, visión o transcripción. Guardar la clave no cambia nada
              por sí solo: elegí DeepSeek en "Proveedores" para cada capacidad donde lo quieras
              usar.</small
            >
            <div class="api-key-actions">
              <button type="submit" :disabled="savingApiKey || !deepseekKey">
                {{ savingApiKey ? 'Guardando…' : 'Guardar clave' }}</button><button
                type="button"
                :disabled="savingApiKey"
                @click="removeDeepSeekKey"
              >
                Quitar
              </button>
            </div>
            <small v-if="deepseekMessage" role="status">{{ deepseekMessage }}</small>
          </form>
        </details>
        <details class="key-group">
          <summary>
            Gemini (experimental)
            <span :class="['key-state', ready.gemini ? 'ok' : 'missing']">{{
              ready.gemini ? '✓ configurado' : 'sin configurar'
            }}</span>
          </summary>
          <form class="api-key-form" @submit.prevent="saveGeminiKey">
            <label for="gemini-key">API key</label
            ><input
              id="gemini-key"
              v-model="geminiKey"
              type="password"
              autocomplete="off"
              spellcheck="false"
              placeholder="AI…"
            /><small
              >Separa a los participantes de una reunión al terminar de grabarla: el audio de esa
              pista se envía a Gemini. Sin clave, quedan como "Participante". No instala nada en tu
              máquina.</small
            >
            <div class="api-key-actions">
              <button type="submit" :disabled="savingApiKey || !geminiKey">
                {{ savingApiKey ? 'Guardando…' : 'Guardar clave' }}</button><button
                type="button"
                :disabled="savingApiKey"
                @click="removeGeminiKey"
              >
                Quitar
              </button>
            </div>
            <small v-if="geminiMessage" role="status">{{ geminiMessage }}</small>
          </form>
        </details>
      </div>
      <div
        v-show="tab === 'audio'"
        :id="'settings-panel-audio'"
        class="settings-section"
        role="tabpanel"
        :aria-labelledby="'settings-tab-audio'"
      >
        <h3>Audio</h3>
        <label for="settings-audio-source">Fuente de captura</label>
        <div class="audio-source-controls">
          <select
            id="settings-audio-source"
            :value="selectedAudioSource"
            @change="emit('select-audio-source', ($event.target as HTMLSelectElement).value)"
          >
            <option value="">Automática</option>
            <option v-for="source in audioSources" :key="source.target" :value="source.target">
              {{ source.label }} · #{{ source.target }}{{ source.running ? ' · activo' : '' }}
            </option></select
          ><button
            title="Actualizar fuentes"
            aria-label="Actualizar fuentes de audio"
            @click="emit('refresh-audio-sources')"
          >
            ↻
          </button>
        </div>
        <small v-if="audioGuessing" class="audio-source-hint guessing"
          >Hay {{ audioCandidates }} fuentes posibles; revisa cuál corresponde a la reunión.</small
        ><small v-if="audioSourceMessage" class="audio-source-hint">{{ audioSourceMessage }}</small
        ><label>Idioma hablado</label>
        <div class="language-switch">
          <button
            :class="{ active: audioLanguage === 'es' }"
            @click="emit('set-audio-language', 'es')"
          >
            Español</button
          ><button
            :class="{ active: audioLanguage === 'en' }"
            @click="emit('set-audio-language', 'en')"
          >
            English
          </button>
        </div>
      </div>
    </section>
  </div>
</template>
