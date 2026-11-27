<script setup lang="ts">
defineProps<{ sessionSetupMessage: string; showPerspective: boolean }>()
const emit = defineEmits<{ (event: 'save'): void }>()

const show = defineModel<boolean>('show', { default: false })
const sessionScenario = defineModel<string>('sessionScenario', { default: 'General' })
const sessionPerspective = defineModel<string>('sessionPerspective', { default: 'candidate' })
const sessionCompany = defineModel<string>('sessionCompany', { default: '' })
const sessionRole = defineModel<string>('sessionRole', { default: '' })
const sessionObjective = defineModel<string>('sessionObjective', { default: '' })
const sessionNotes = defineModel<string>('sessionNotes', { default: '' })
// Same as sessionNotesMaxChars in store.go.
const notesMaxChars = 4000
</script>

<template>
  <div v-if="show" class="modal-overlay" @click.self="show = false" @keydown.esc="show = false">
    <section
      class="settings-dialog session-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="session-setup-title"
    >
      <header class="dialog-header">
        <div>
          <p class="eyebrow">PREPARACIÓN</p>
          <h2 id="session-setup-title">Contexto de entrevista</h2>
          <p>Personaliza las respuestas para esta sesión.</p>
        </div>
        <button class="dialog-close" aria-label="Cerrar contexto" @click="show = false">×</button>
      </header>
      <form class="session-setup-form" @submit.prevent="emit('save')">
        <template v-if="showPerspective">
          <label for="session-perspective">Mi rol en la entrevista</label>
          <select id="session-perspective" v-model="sessionPerspective">
            <option value="candidate">Entrevistado</option>
            <option value="interviewer">Entrevistador</option>
          </select>
        </template>
        <label for="session-scenario">Tipo de entrevista</label
        ><select id="session-scenario" v-model="sessionScenario">
          <option>General</option>
          <option>Conductual</option>
          <option>Técnica</option>
          <option>Diseño de sistemas</option>
          <option>Producto y negocio</option>
          <option>Personalizada</option>
        </select>
        <label for="session-company">Empresa <span>Opcional</span></label
        ><input id="session-company" v-model="sessionCompany" placeholder="Ej. Acme" />
        <label for="session-role">Puesto</label
        ><input id="session-role" v-model="sessionRole" placeholder="Ej. Desarrollador frontend" />
        <label for="session-objective">Objetivo de la entrevista</label
        ><input
          id="session-objective"
          v-model="sessionObjective"
          placeholder="Ej. entrevista técnica, primera ronda…"
        />
        <label for="session-notes"
          >{{
            showPerspective && sessionPerspective === 'interviewer'
              ? 'CV del candidato y competencias a evaluar'
              : 'Tu experiencia o instrucciones'
          }}
          <span>{{ sessionNotes.length }}/{{ notesMaxChars }}</span></label
        ><textarea
          id="session-notes"
          v-model="sessionNotes"
          rows="10"
          :maxlength="notesMaxChars"
          :placeholder="
            showPerspective && sessionPerspective === 'interviewer'
              ? 'CV del candidato, competencias a evaluar y preguntas clave…'
              : 'Trabajos y proyectos (qué era, tu rol, tecnologías, resultado), logros, tono y datos que Sonara debe tener en cuenta…'
          "
        ></textarea>
        <small class="profile-hint"
          >Este contexto se agrega al prompt de la sesión y las sesiones nuevas lo heredan, así no
          hace falta volver a cargarlo.</small
        >
        <div class="dialog-actions">
          <span role="status">{{ sessionSetupMessage }}</span><button
            class="primary-action"
            type="submit"
          >
            Guardar contexto
          </button>
        </div>
      </form>
    </section>
  </div>
</template>
