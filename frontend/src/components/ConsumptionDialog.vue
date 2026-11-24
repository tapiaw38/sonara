<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { GetAPIUsage, SaveAPIUsagePrices } from '../../wailsjs/go/app/App'
import type { storage } from '../../wailsjs/go/models'

const emit = defineEmits<{ close: [] }>()
const report = ref<storage.UsageReport>()
const periodID = ref('today')
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
const metrics = [
  { key: 'calls', name: 'Llamadas' },
  { key: 'errors', name: 'Errores' },
  { key: 'input', name: 'Tokens entrada' },
  { key: 'output', name: 'Tokens salida' },
  { key: 'audioSeconds', name: 'Audio (s)' },
  { key: 'characters', name: 'Caracteres' },
  { key: 'usd', name: 'Estimado (USD)' },
  { key: 'unpriced', name: 'Sin estimar' },
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
const sections = computed(() =>
  period.value
    ? [
        { title: 'Por proveedor', groups: period.value.providers },
        { title: 'Por función', groups: period.value.functions },
        { title: 'Por modelo', groups: period.value.models },
      ].filter((item) => item.groups.length)
    : [],
)

function format(field: (typeof metrics)[number]['key'], value: number) {
  return field === 'usd' ? currency.format(value) : number.format(value)
}

async function refresh() {
  loading.value = true
  failure.value = ''
  try {
    report.value = await GetAPIUsage()
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
      <template v-if="period && report">
        <dl class="usage-totals">
          <div v-for="metric in metrics" :key="metric.key">
            <dt>{{ metric.name }}</dt>
            <dd>{{ format(metric.key, period.totals[metric.key]) }}</dd>
          </div>
        </dl>
        <p v-if="!period.totals.calls">Todavía no hay llamadas registradas en este período.</p>
        <p v-else-if="period.totals.unpriced">
          Las llamadas sin tarifa o sin métricas suficientes quedan fuera del costo.
        </p>
        <section v-for="section in sections" :key="section.title">
          <h3>{{ section.title }}</h3>
          <div class="usage-scroll">
            <table>
              <thead>
                <tr>
                  <th scope="col">{{ section.title }}</th>
                  <th v-for="metric in metrics" :key="metric.key" scope="col">{{ metric.name }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in section.groups" :key="row.name">
                  <th scope="row">{{ labels[row.name] ?? row.name }}</th>
                  <td v-for="metric in metrics" :key="metric.key">
                    {{ format(metric.key, row.totals[metric.key]) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <details class="usage-prices">
          <summary>Precios por modelo</summary>
          <p>
            Tarifas en USD por millón de tokens o caracteres, salvo el minuto de audio. Los precios
            se aplican también al historial.
          </p>
          <form @submit.prevent="save">
            <div class="usage-scroll">
              <table>
                <thead>
                  <tr>
                    <th scope="col">Proveedor y modelo</th>
                    <th scope="col">Activo</th>
                    <th v-for="field in fields" :key="field.key" scope="col">{{ field.label }}</th>
                    <th scope="col">Cobro por duración</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="price in report.prices" :key="price.provider + '/' + price.model">
                    <th scope="row">{{ labels[price.provider] }} · {{ price.model }}</th>
                    <td>
                      <input
                        v-model="price.enabled"
                        type="checkbox"
                        :aria-label="'Activar tarifa para ' + price.model"
                      />
                    </td>
                    <td v-for="field in fields" :key="field.key">
                      <input
                        v-model.number="price[field.key]"
                        type="number"
                        required
                        min="0"
                        step="any"
                        :aria-label="field.label + ' · ' + price.model"
                      />
                    </td>
                    <td>
                      <input
                        v-model="price.durationBilling"
                        type="checkbox"
                        :aria-label="'Priorizar duración para ' + price.model"
                      />
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
            <button type="submit" :disabled="loading || saving">
              {{ saving ? 'Guardando…' : 'Guardar precios' }}
            </button>
            <span v-if="message" role="status">{{ message }}</span>
          </form>
        </details>
      </template>
    </section>
  </div>
</template>

<style scoped>
.usage-dialog {
  width: min(1050px, 96vw);
  display: grid;
  gap: 14px;
}
.usage-dialog p {
  margin: 0;
  color: #a7a9b5;
}
.usage-toolbar,
.usage-periods {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.usage-toolbar {
  justify-content: space-between;
}
.usage-toolbar button,
.usage-prices button {
  padding: 6px 10px;
  border: 1px solid #414351;
  border-radius: 6px;
  background: #232430;
  color: #dedfe6;
}
.usage-periods [aria-pressed='true'] {
  border-color: #62d7b3;
  color: #a2f2d9;
}
.usage-totals {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(105px, 1fr));
  gap: 6px;
  margin: 0;
}
.usage-totals div {
  padding: 8px;
  border: 1px solid #343643;
  border-radius: 6px;
}
dt {
  color: #a7a9b5;
  font-size: 11px;
}
dd {
  margin: 4px 0 0;
  font-variant-numeric: tabular-nums;
}
h3 {
  font-size: 13px;
  margin: 0 0 5px;
}
.usage-scroll {
  overflow-x: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
th,
td {
  padding: 6px;
  border-bottom: 1px solid #343643;
  text-align: right;
}
th:first-child {
  text-align: left;
  min-width: 135px;
}
.usage-prices summary {
  cursor: pointer;
  font-weight: 600;
}
.usage-prices p,
.usage-prices form > button,
.usage-prices form > span {
  margin-top: 8px;
}
.usage-prices form > span {
  margin-left: 8px;
}
input[type='number'] {
  width: 65px;
  padding: 4px;
  border: 1px solid #414351;
  border-radius: 4px;
  background: #161720;
  color: inherit;
}
</style>
