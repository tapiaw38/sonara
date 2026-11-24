<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  entry: {
    original: string
    language: string
    translation?: string
    lines?: { original: string; translation: string }[]
    interrupted?: boolean
  }
}>()

const target = computed(() => (props.entry.language === 'en' ? 'es' : 'en'))
</script>

<template>
  <small v-if="entry.interrupted" class="practice-interrupted">Turno interrumpido</small>
  <template v-if="entry.lines?.length">
    <span class="language-label" aria-label="Idioma de la traducción">
      {{ target.toUpperCase() }}
    </span>
    <div v-for="(line, index) in entry.lines" :key="index" class="sentence-pair">
      <p class="transcript-text" :lang="entry.language">{{ line.original }}</p>
      <p class="interlinear-translation" :lang="target">{{ line.translation }}</p>
    </div>
  </template>
  <template v-else>
    <p class="transcript-text" :lang="entry.language">{{ entry.original }}</p>
    <p v-if="entry.translation" class="transcript-translation" :lang="target">
      <span>{{ target.toUpperCase() }}</span>{{ entry.translation }}
    </p>
  </template>
</template>

<style scoped>
.transcript-text,
.transcript-translation,
.interlinear-translation {
  overflow-wrap: anywhere;
}
.transcript-text {
  color: #c9cad3;
}
.practice-interrupted {
  display: block;
  opacity: 0.75;
}
.language-label {
  display: block;
  margin-bottom: 5px;
  color: #9b9daa;
  font:
    10px 'DM Mono',
    monospace;
}
.sentence-pair + .sentence-pair {
  margin-top: 8px;
}
.interlinear-translation {
  margin: 2px 0 0;
  color: #9b9daa;
  font-size: 11px;
  line-height: 1.55;
}
</style>
