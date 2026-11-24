<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  partial?: boolean
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
  <template v-if="!partial && entry.lines?.length">
    <div v-for="(line, index) in entry.lines" :key="index" class="sentence-pair">
      <p class="transcript-text" :lang="entry.language">{{ line.original }}</p>
      <p class="interlinear-translation" :lang="target">{{ line.translation }}</p>
    </div>
  </template>
  <template v-else>
    <p class="transcript-text" :class="{ partial }" :lang="entry.language">{{ entry.original }}</p>
    <p
      v-if="!partial && entry.translation"
      class="interlinear-translation translation-block"
      :lang="target"
    >
      {{ entry.translation }}
    </p>
  </template>
</template>

<style scoped>
.transcript-text,
.interlinear-translation {
  overflow-wrap: anywhere;
}
.transcript-text {
  color: #e3e5ea;
  font-weight: 500;
}
.practice-interrupted {
  display: block;
  opacity: 0.75;
}
.sentence-pair + .sentence-pair {
  margin-top: 8px;
}
.interlinear-translation {
  margin: 2px 0 0;
  color: #b3a6d6;
  font-family: 'DM Mono', monospace;
  font-size: 11px;
  line-height: 1.55;
}
.translation-block {
  margin-top: 8px;
}
</style>
