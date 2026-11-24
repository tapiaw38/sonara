<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { GetAPIUsage, SaveAPIUsagePrices } from '../../wailsjs/go/app/App'
import type { storage } from '../../wailsjs/go/models'

const emit = defineEmits<{ close: [] }>()
const report = ref<storage.UsageReport>()
const periodID = ref('today')
const breakdown = ref<'providers' | 'functions' | 'models'>('providers')
const priceKey = ref('')
const loading = ref(false)
const saving = ref(false)
const message = ref('')
const failure = ref('')
const closeButton = ref<HTMLButtonElement>()
const period = computed(() => report.value?.periods.find((row) => row.id === periodID.value))
const ranges = [
  { id: 'today', name: 'Hoy' },
  { id: 'week', name: 'Últimos 7 días' },
  { id: 'month', name: 'Este mes' },
]
const labels: Record<string, string> = {
  openai: 'OpenAI',
  azure: 'Azure OpenAI',
  deepseek: 'DeepSeek',
  local: 'Local',
  gemini: 'Gemini',
  copilot: 'Copiloto',
  practice: 'Práctica',
  'live-transcription': 'Transcripción en vivo',
  'meeting-transcription': 'Transcripción de reunión',
  tts: 'Voz TTS',
  translation: 'Traducción',
  summary: 'Resumen y memoria',
  names: 'Nombres',
  diarization: 'Separación de participantes y eco',
}
const breakdowns = [
  { id: 'providers', label: 'Proveedor' },
  { id: 'functions', label: 'Función' },
  { id: 'models', label: 'Modelo' },
] as const
const fields = [
  { key: 'input', label: 'Entrada' },
  { key: 'output', label: 'Salida' },
  { key: 'audioInput', label: 'Entrada audio' },
  { key: 'audioOutput', label: 'Salida audio' },
  { key: 'perMinute', label: 'Minuto de audio' },
  { key: 'perMillionChars', label: 'Caracteres' },
] as const
const number = new Intl.NumberFormat('es', { maximumFractionDigits: 2 })
const currency = new Intl.NumberFormat('es', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 6,
})
const groups = computed(() =>
  [...(period.value?.[breakdown.value] ?? [])].sort(
    (a, b) => b.totals.usd - a.totals.usd || b.totals.calls - a.totals.calls,
  ),
)
const selectedPrice = computed(() =>
  report.value?.prices.find((price) => modelKey(price) === priceKey.value),
)

function modelKey(price: storage.UsagePrice) {
  return price.provider + '/' + price.model
}

function groupName(name: string) {
  const [provider, model] = name.split(' · ', 2)
  return model ? `${labels[provider] ?? provider} · ${model}` : (labels[name] ?? name)
}

function audioTime(seconds: number) {
  if (seconds < 60) return `${number.format(seconds)} s`
  if (seconds < 3600) return `${number.format(seconds / 60)} min`
  return `${number.format(seconds / 3600)} h`
}

async function refresh() {
  loading.value = true
  failure.value = ''
  try {
    report.value = await GetAPIUsage()
    if (!selectedPrice.value) {
      const price = report.value.prices.find((row) => !row.enabled) ?? report.value.prices[0]
      priceKey.value = price ? modelKey(price) : ''
    }
  } catch (error) {
    failure.value = String(error)
  } finally {
    loading.value = false
  }
}

async function save(reset = false) {
  if (!selectedPrice.value) return
  saving.value = true
  message.value = ''
  failure.value = ''
  try {
    await SaveAPIUsagePrices([{ ...selectedPrice.value, source: reset ? 'reset' : 'manual' }])
    await refresh()
    if (!failure.value) message.value = reset ? 'Tarifa automática restaurada.' : 'Tarifa guardada.'
  } catch (error) {
    failure.value = String(error)
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  closeButton.value?.focus()
  void refresh()
})
</script>

<template>
  <div class="modal-overlay" @click.self="emit('close')" @keydown.esc="emit('close')">
    <section
      class="settings-dialog usage-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="usage-title"
    >
      <header class="dialog-header">
        <div>
          <h2 id="usage-title">Consumo de APIs</h2>
          <p>
            Los costos son estimados en USD. Consultá la factura real en el portal del proveedor.
          </p>
        </div>
        <button
          ref="closeButton"
          class="dialog-close"
          aria-label="Cerrar consumo"
          @click="emit('close')"
        >
          ×
        </button>
      </header>
      <div class="usage-toolbar">
        <div class="usage-periods" role="group" aria-label="Período">
          <button
            v-for="range in ranges"
            :key="range.id"
            class="speaker-chip"
            :aria-pressed="periodID === range.id"
            @click="periodID = range.id"
          >
            {{ range.name }}
          </button>
        </div>
        <button class="meeting-secondary" :disabled="loading || saving" @click="refresh">
          {{ loading ? 'Cargando…' : 'Actualizar' }}
        </button>
      </div>
      <p v-if="failure" role="alert">{{ failure }}</p>
      <p v-if="loading && !report" role="status">Cargando consumo…</p>
      <template v-if="period && report">
        <dl class="usage-summary">
          <div class="usage-cost">
            <dt>Costo estimado</dt>
            <dd>{{ currency.format(period.totals.usd) }}</dd>
            <small v-if="period.totals.unpriced">
              {{ number.format(period.totals.unpriced) }} sin estimar
            </small>
          </div>
          <div>
            <dt>Llamadas</dt>
            <dd>{{ number.format(period.totals.calls) }}</dd>
            <small v-if="period.totals.errors"
              >{{ number.format(period.totals.errors) }} con error</small
            >
          </div>
          <div>
            <dt>Tokens</dt>
            <dd>{{ number.format(period.totals.input + period.totals.output) }}</dd>
            <small
              >{{ number.format(period.totals.input) }} entrada ·
              {{ number.format(period.totals.output) }} salida</small
            >
          </div>
          <div>
            <dt>Audio</dt>
            <dd>{{ audioTime(period.totals.audioSeconds) }}</dd>
            <small v-if="period.totals.characters"
              >{{ number.format(period.totals.characters) }} caracteres TTS</small
            >
          </div>
        </dl>
        <section
          v-if="groups.length"
          class="usage-breakdown"
          aria-labelledby="usage-breakdown-title"
        >
          <div class="breakdown-header">
            <h3 id="usage-breakdown-title">Desglose</h3>
            <div class="usage-periods" role="group" aria-label="Agrupar consumo">
              <button
                v-for="item in breakdowns"
                :key="item.id"
                class="speaker-chip"
                :aria-pressed="breakdown === item.id"
                @click="breakdown = item.id"
              >
                {{ item.label }}
              </button>
            </div>
          </div>
          <table class="usage-groups" aria-labelledby="usage-breakdown-title">
            <thead>
              <tr>
                <th scope="col">{{ breakdowns.find((item) => item.id === breakdown)?.label }}</th>
                <th scope="col" class="row-cost">Costo estimado</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in groups" :key="row.name">
                <td>
                  <strong>{{ groupName(row.name) }}</strong>
                  <span class="usage-details">
                    <span>{{ number.format(row.totals.calls) }} llamadas</span>
                    <span v-if="row.totals.input || row.totals.output"
                      >{{ number.format(row.totals.input) }} entrada ·
                      {{ number.format(row.totals.output) }} salida</span
                    >
                    <span v-if="row.totals.audioSeconds"
                      >{{ audioTime(row.totals.audioSeconds) }} de audio</span
                    >
                    <span v-if="row.totals.characters"
                      >{{ number.format(row.totals.characters) }} caracteres</span
                    >
                    <span v-if="row.totals.errors"
                      >{{ number.format(row.totals.errors) }} con error</span
                    >
                  </span>
                </td>
                <td class="row-cost">
                  <strong>{{
                    row.totals.unpriced === row.totals.calls ? '—' : currency.format(row.totals.usd)
                  }}</strong>
                  <small v-if="row.totals.unpriced"
                    >{{ number.format(row.totals.unpriced) }} sin estimar</small
                  >
                </td>
              </tr>
            </tbody>
          </table>
        </section>
        <div v-else class="empty-state">
          <span class="empty-icon" aria-hidden="true">✦</span>
          <p>Todavía no hay llamadas registradas en este período.</p>
        </div>
        <details class="key-group usage-prices">
          <summary>Precios por modelo</summary>
          <div class="price-body">
            <p>
              Los precios se aplican también al historial. Las tarifas faltantes se excluyen del
              costo.
            </p>
            <p v-if="report.catalogUpdated">
              Catálogo LiteLLM actualizado el
              {{ new Date(report.catalogUpdated).toLocaleString('es') }}.
            </p>
            <p v-else>Consumo usa las tarifas de referencia mientras se descarga el catálogo.</p>
            <form v-if="selectedPrice" @submit.prevent="save()">
              <label class="price-model">
                Modelo
                <select v-model="priceKey">
                  <option
                    v-for="price in report.prices"
                    :key="modelKey(price)"
                    :value="modelKey(price)"
                  >
                    {{ labels[price.provider] ?? price.provider }} · {{ price.model
                    }}{{ price.enabled ? '' : ' (sin tarifa)' }}
                  </option>
                </select>
              </label>
              <div class="price-fields">
                <label v-for="field in fields" :key="field.key">
                  {{ field.label }}
                  <input
                    v-model.number="selectedPrice[field.key]"
                    type="number"
                    required
                    min="0"
                    step="any"
                  />
                  <small>{{
                    field.key === 'perMinute'
                      ? 'USD/min'
                      : field.key === 'perMillionChars'
                        ? 'USD/millón de caracteres'
                        : 'USD/millón de tokens'
                  }}</small>
                </label>
              </div>
              <div class="price-footer">
                <label><input v-model="selectedPrice.enabled" type="checkbox" /> Usar tarifa</label>
                <label
                  ><input v-model="selectedPrice.durationBilling" type="checkbox" /> Priorizar
                  minutos</label
                >
                <small>{{
                  selectedPrice.source === 'manual'
                    ? 'Tarifa manual'
                    : selectedPrice.source === 'catalog'
                      ? 'Fuente: LiteLLM'
                      : selectedPrice.source === 'default'
                        ? 'Tarifa de referencia'
                        : 'Sin tarifa disponible'
                }}</small>
                <button
                  class="meeting-secondary"
                  v-if="selectedPrice.source === 'manual'"
                  type="button"
                  :disabled="loading || saving"
                  @click="save(true)"
                >
                  Restaurar automática
                </button>
                <button class="meeting-secondary" type="submit" :disabled="loading || saving">
                  {{ saving ? 'Guardando…' : 'Guardar precios' }}
                </button>
              </div>
              <p v-if="message" role="status">{{ message }}</p>
            </form>
          </div>
        </details>
      </template>
    </section>
  </div>
</template>

<style scoped>
.usage-dialog {
  width: min(760px, 96vw);
  display: grid;
  gap: 10px;
  font-size: 12px;
}
.usage-dialog p {
  margin: 0;
}
.usage-dialog small,
.usage-summary dt,
.usage-groups th,
.usage-details,
.price-body p,
.price-body label {
  color: #9b9daa;
  font-size: 11px;
}
.usage-toolbar,
.breakdown-header,
.price-footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.usage-periods {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.usage-periods button {
  font-family: inherit;
}
.usage-periods [aria-pressed='true'] {
  color: #b9f2df;
  background: rgb(27 57 47 / var(--surface-opacity));
  box-shadow: inset 0 0 0 1px rgb(62 126 108 / var(--surface-opacity));
}
.usage-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin: 0;
}
.usage-summary > div {
  padding: 8px 10px;
  border: 1px solid rgb(41 42 53 / var(--surface-opacity));
  border-radius: 7px;
}
.usage-summary dd {
  margin: 4px 0 0;
  overflow-wrap: anywhere;
  font-size: 13px;
}
.usage-cost dd {
  color: #b9f2df;
}
.usage-summary dd,
.usage-summary small,
.usage-details,
.row-cost strong,
.row-cost small,
.price-fields input {
  font-family: 'DM Mono', monospace;
}
.breakdown-header h3 {
  font-size: 12px;
  font-weight: 600;
  margin: 0;
}
.usage-groups {
  width: 100%;
  margin-top: 4px;
  border-collapse: collapse;
  text-align: left;
}
.usage-groups th,
.usage-groups td {
  padding: 5px 8px;
  border-bottom: 1px solid rgb(41 42 53 / var(--surface-opacity));
  overflow-wrap: anywhere;
  vertical-align: top;
}
.usage-groups th {
  font-weight: 600;
}
.usage-details {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 8px;
  margin-top: 2px;
}
.row-cost {
  text-align: right;
}
.row-cost small {
  display: block;
}
.usage-prices {
  margin-top: 0;
}
.price-body {
  display: grid;
  gap: 6px;
  padding: 0 13px 13px;
}
.usage-prices form,
.price-fields,
.price-fields label,
.price-model {
  display: grid;
  gap: 6px;
}
.usage-prices form {
  gap: 12px;
  margin-top: 10px;
}
.price-fields {
  grid-template-columns: repeat(auto-fit, minmax(135px, 1fr));
}
.price-fields input,
.price-model select {
  min-width: 0;
  padding: 5px 7px;
  border: 1px solid #414351;
  border-radius: 5px;
  background: #161720;
  color: inherit;
}
@media (max-width: 650px) {
  .usage-summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
