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

async function save() {
  if (!report.value) return
  saving.value = true
  message.value = ''
  failure.value = ''
  try {
    await SaveAPIUsagePrices(report.value.prices)
    await refresh()
    if (!failure.value) message.value = 'Precios guardados.'
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
        <div class="usage-periods" aria-label="Período">
          <button
            v-for="range in ranges"
            :key="range.id"
            :aria-pressed="periodID === range.id"
            @click="periodID = range.id"
          >
            {{ range.name }}
          </button>
        </div>
        <button :disabled="loading || saving" @click="refresh">
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
            <div class="usage-periods" aria-label="Agrupar consumo">
              <button
                v-for="item in breakdowns"
                :key="item.id"
                :aria-pressed="breakdown === item.id"
                @click="breakdown = item.id"
              >
                {{ item.label }}
              </button>
            </div>
          </div>
          <ul class="usage-groups">
            <li v-for="row in groups" :key="row.name">
              <div>
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
              </div>
              <div class="row-cost">
                <strong>{{
                  row.totals.unpriced === row.totals.calls ? '—' : currency.format(row.totals.usd)
                }}</strong>
                <small v-if="row.totals.unpriced"
                  >{{ number.format(row.totals.unpriced) }} sin estimar</small
                >
              </div>
            </li>
          </ul>
        </section>
        <div v-else class="usage-empty">Todavía no hay llamadas registradas en este período.</div>
        <details class="usage-prices">
          <summary>Precios por modelo</summary>
          <p>
            Los precios se aplican también al historial. Las tarifas faltantes se excluyen del
            costo.
          </p>
          <form v-if="selectedPrice" @submit.prevent="save">
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
              <button type="submit" :disabled="loading || saving">
                {{ saving ? 'Guardando…' : 'Guardar precios' }}
              </button>
            </div>
            <p v-if="message" role="status">{{ message }}</p>
          </form>
        </details>
      </template>
    </section>
  </div>
</template>

<style scoped>
.usage-dialog {
  width: min(820px, 96vw);
  display: grid;
  gap: 16px;
  color: #e8e9ee;
}
.usage-dialog p,
.usage-dialog small {
  color: #a7a9b5;
}
.usage-dialog p {
  margin: 0;
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
  display: inline-flex;
  flex-wrap: wrap;
  gap: 2px;
  padding: 3px;
  border: 1px solid #343643;
  border-radius: 8px;
  background: #18191f;
}
.usage-toolbar button,
.usage-periods button,
.usage-prices button {
  padding: 6px 10px;
  border: 1px solid transparent;
  border-radius: 5px;
  color: #c5c7d2;
  background: transparent;
}
.usage-toolbar > button,
.usage-prices button {
  border-color: #414351;
}
.usage-periods [aria-pressed='true'] {
  color: #b5f3df;
  background: #253a32;
}
.usage-summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 8px;
  margin: 0;
}
.usage-summary > div,
.usage-empty,
.usage-prices {
  padding: 12px;
  border: 1px solid #343643;
  border-radius: 9px;
  background: #191c23;
}
.usage-summary .usage-cost {
  border-color: #36584b;
  background: #1b2926;
}
dt,
.usage-details,
.price-fields small {
  color: #a7a9b5;
  font-size: 11px;
}
dd {
  margin: 5px 0;
  overflow-wrap: anywhere;
  font-size: 20px;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}
.usage-cost dd {
  color: #b5f3df;
}
h3 {
  font-size: 14px;
  margin: 0;
}
.usage-groups {
  margin: 8px 0 0;
  padding: 0;
  list-style: none;
  border: 1px solid #343643;
  border-radius: 8px;
}
.usage-groups li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 10px;
  padding: 9px 12px;
  overflow-wrap: anywhere;
}
.usage-groups li + li {
  border-top: 1px solid #343643;
}
.usage-details {
  display: flex;
  flex-wrap: wrap;
  gap: 3px 10px;
  margin-top: 4px;
}
.row-cost {
  display: grid;
  align-content: start;
  gap: 4px;
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.usage-prices summary {
  cursor: pointer;
  font-weight: 600;
}
.usage-prices[open] summary {
  margin-bottom: 9px;
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
