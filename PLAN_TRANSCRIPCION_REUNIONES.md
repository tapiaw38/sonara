# Plan: transcripción de reuniones con participantes

Fecha de investigación: 2026-11-16. Estado: fases 1, 1b, 2 y 3 implementadas en `feat/meeting-transcript`, más sugerencia de nombres por texto y Azure OpenAI como proveedor adicional. **Se reemplazó el enrutamiento con fallback automático (DeepSeek→OpenAI) por selección explícita sin cadena** — ver abajo. Rama: `feat/meeting-transcript` desde `develop`.

## Azure OpenAI y selección de proveedores sin fallback (2026-11-16) — implementado

El usuario pidió explícitamente **sacar los fallbacks automáticos** (DeepSeek→OpenAI incluido, ya commiteado antes) y que la configuración de Azure vaya por la interfaz, nunca por variable de entorno.

- **Patrón:** Strategy + registro de proveedores (`backend/internal/adapters/web/integrations/providers/providers.go`), reemplaza por completo al paquete `router` anterior (eliminado). `Registry.Get(nombre)` devuelve exactamente ese proveedor o un error — nunca intenta otro. `Selector` resuelve texto/visión por nombre explícito (visión según si la petición trae imagen); `Transcriber` queda atado a un solo nombre. Sin reintento, sin cadena, en ningún punto del código.
- **Cuatro capacidades, cuatro elecciones independientes:** texto, visión, transcripción en vivo (copiloto) y transcripción de reuniones. Cada una con su proveedor elegido a mano en Configuración → "Proveedores", persistido en la base (`capability_providers`, JSON), por defecto `openai` hasta que se cambie. Cambiar una no toca las otras tres.
- **Azure OpenAI** (`backend/internal/adapters/web/integrations/azureopenai/`): mismo patrón que DeepSeek, pero autenticación `api-key` (no `Authorization: Bearer`) y URL `{endpoint}/openai/deployments/{deployment}/...?api-version=...`. Un solo *deployment* de chat sirve texto y visión (Azure despliega modelos por nombre; visión sólo funciona si ese deployment tiene un modelo con visión); *deployment* de transcripción aparte y opcional. **Dos versiones de API independientes** (`ChatAPIVersion`/`TranscribeAPIVersion`), no una sola: confirmado contra un recurso Azure real del usuario que corre chat y Whisper en versiones distintas a la vez (`2025-04-01-preview` vs `2025-03-01-preview` en su `.env` de otro proyecto). Un solo campo habría roto uno de los dos.
- **Nada por variable de entorno.** Endpoint, versión de API, nombres de *deployment* y clave se escriben en Configuración → "Azure OpenAI" y se guardan en la base como un solo JSON (`azure_openai_config`). La clave nunca se devuelve a la interfaz (mismo criterio que OpenAI/DeepSeek/Gemini); editar el endpoint sin volver a escribir la clave la conserva. "Quitar clave" la borra explícitamente.
- **No verificado con un recurso real de Azure**: igual que Gemini, construido contra la documentación pública de Microsoft (`learn.microsoft.com/azure/ai-services/openai/reference`, consultada 2026-11-16), nunca probado con una clave real. Verificado con un servidor Azure simulado: mensaje de texto plano vs. partes de contenido con imagen, error de Azure propagado, sin "model" en el formulario de transcripción (lo fija el *deployment*), ambos `Configured()`/`TranscribeConfigured()` independientes.
- El registro completo (`Registry`/`Selector`/`Transcriber`) se probó con proveedores simulados: selección exacta, nunca prueba otro ante fallo, los setters cambian la elección activa sin tocar las demás.
- Diarización y eco (Gemini) quedan **fuera** de este sistema de selección: siguen siendo "si hay clave, se intenta; si falla, no pasa nada" — no son una capacidad con alternativas entre sí, son una mejora opcional sobre lo que ya se transcribió.

## Fase 3 implementada (2026-11-16) — detección de eco de la propia voz, vía Gemini

Mismo proveedor y misma estrategia "una sola pasada al terminar" que fase 2, reutilizando su cliente de Gemini y su buffer acumulado de `meeting_output`; no agrega dependencias nuevas.

- **Muestra de voz voluntaria**: botón "Grabar muestra (8s)" en la vista de reunión (visible sólo si hay clave de Gemini), graba desde el micrófono elegido y la guarda en `voice-sample.wav` junto a la base de datos (0600, nunca dentro de una reunión ni se borra sola). Es la única pieza de audio que la app conserva a propósito; todo lo demás se descarta tras usarse.
- Al terminar una reunión, si hay muestra Y clave de Gemini: se le pide a Gemini (misma llamada `interactions`, ahora con la muestra inline en base64 además del archivo de la reunión subido) que diga en qué tramos de tiempo se escucha esa voz dentro de la pista de la reunión — eco por altavoces, no otra persona que suene parecido.
- Un segmento de `meeting_output` cuyo solapamiento con esos tramos supera el 50% de su duración (`echoOverlapFraction`, sin calibrar con audio real) pasa a estado `echo` — migración `0006_echo_status.sql`. El texto nunca se borra, sólo cambia el estado; se puede reconstruir a mano si el umbral se equivoca (no hay botón de "deshacer" todavía).
- Exportación y UI muestran la línea como "eco de tu propia voz, omitido como duplicado" en vez de hacerla desaparecer sin rastro — mismo principio que huecos y fallos: nunca ocultar en silencio.
- **Sin calibrar con audio real**: el umbral del 50% es un punto de partida razonable, no un valor medido. El propio plan pedía validar esto con 3–5 grabaciones consentidas antes de confiar en él (sección "Prueba de viabilidad" más abajo); eso no se hizo.
- Verificado con un servidor Gemini simulado: detección de eco feliz, propagación de error, sin clave, sin muestra, sin audio acumulado, el buffer compartido sobrevive a ambas pasadas (diarización y eco) y sólo se libera una vez al final.

## Evaluando: Azure OpenAI como proveedor adicional (2026-11-16, sin implementar)

El usuario tiene un recurso de Azure OpenAI Service, hoy usado para transcribir audio fuera de Sonara. Mismo recurso/clave podría cubrir:
- **Texto**: sí, sin pasos extra — mismo tipo de endpoint de chat que DeepSeek/OpenAI.
- **Visión**: sí, pero necesita desplegar un modelo con visión (GPT-4o/GPT-5) como *deployment* aparte bajo el mismo recurso; no se habilita solo por tener ya un deployment de transcripción.
- **Audio**: sí, si tiene un deployment de transcripción (Whisper/`gpt-4o-transcribe`).
- **Diarización/eco**: no — Azure OpenAI no lo hace. Haría falta Azure AI Speech (producto distinto, con diarización incluida en transcripción por lote, ~$0.18/hora) o seguir con Gemini.

Pendiente: escribir el adaptador (`backend/internal/adapters/web/integrations/azureopenai/`, mismo tamaño que el de DeepSeek — difiere en autenticación `api-key` en vez de `Authorization: Bearer` y URL con `.../openai/deployments/{deployment}/...?api-version=...`) y decidir si reemplaza a OpenAI, a DeepSeek, o se suma como una tercera opción en el enrutador.

## Fase 2 implementada (2026-11-16) — revisada el mismo día: de pyannote local a Gemini en la nube

Primera versión de fase 2 usaba pyannote local (servicio Python, PyTorch, token de Hugging Face). El usuario pidió no instalar nada pesado en su máquina; se reemplazó por la API de Gemini (nube), sin tocar casi nada del diseño de arriba.

- **Diarización sigue corriendo una sola vez, al terminar la grabación, no por bloque.** Una llamada a Gemini no recuerda la anterior: diarizar bloque por bloque daría IDs incompatibles entre bloques. Por eso la pista `meeting_output` completa se acumula en memoria mientras dura la grabación (sólo si hay clave de Gemini configurada; nunca el micrófono) y se manda entera en un solo paso cuando se detiene. Los nombres de participantes aparecen recién entonces, no en vivo.
- **Sin instalación local**: se eliminó el servicio Python (`diarize/server.py`, pyannote, PyTorch, token de Hugging Face). La diarización activa ("Configurada" = hay clave de Gemini en Configuración) es sólo una llamada HTTP, igual que DeepSeek/OpenAI.
- Flujo con la API de Gemini (`diarize.go`): subir el WAV con la API de archivos (subida resumible: `POST /upload/v1beta/files` para iniciar, `POST` a la URL devuelta para los bytes), pedir sólo los turnos de habla (sin transcribir texto, Sonara ya lo tiene por su propio ASR) a `POST /v1beta/interactions` con `response_format` JSON y el modelo `gemini-4-hydrogen`, y borrar el archivo subido al terminar (best-effort; Google lo expira solo a las 48h igual).
- Acumulación con tope `diarizeMaxDuration = 3h` (límite de memoria de la app, no de Gemini); pasado ese límite, el resto de la reunión sigue transcribiéndose normal pero sin diarizar. Memoria se libera apenas termina el paso, con éxito o error.
- Esquema sin cambios: migración `0005_diarized_speakers.sql` agrega `speaker_key` a `transcript_segments` y reconstruye `transcript_speakers` con clave `(session_id, track, speaker_key)`. Numeración por defecto "Participante N" según primera aparición; sobrescribible por participante.
- **No verificado con una cuenta real de Gemini**: no hay clave de API en esta máquina. El endpoint, los nombres de campo (`uri`, `mime_type`, `response_format`, `steps[].content[].text`) y las cabeceras (`x-goog-api-key`, `Api-Revision: 2026-05-20`) vienen de la documentación oficial (`aistudio.google.com/docs/*`, consultada 2026-11-16), no de una llamada real. En particular, el endpoint de borrado de archivo (`DELETE /v1beta/files/{name}`) no estaba confirmado en la documentación consultada; si el nombre no es correcto, el borrado falla en silencio (se registra en el log) y Google igual expira el archivo a las 48h.
- Lo que sí se verificó: toda la lógica Go (mapeo de hablantes estable, solapamiento, acumulación/tope, fallback, integración completa con la base de datos, las tres llamadas HTTP de `diarize()`) con tests que simulan el servicio de Gemini (`diarize_test.go`), sin cuenta real.
## Sugerencia de nombres por texto (2026-11-16) — implementada

Nombres reales (Juan, Pedro, María) no los resuelve ningún proveedor de diarización — agrupan voces, no identifican personas. Se agregó el atajo barato planteado arriba: leer el texto ya transcripto y sugerir nombres sólo cuando alguien se nombra en la charla ("Hola Pedro", "soy María"), sin depender de Gemini ni de un proveedor nuevo.

- Nuevo caso de uso en el backend (`backend/internal/usecases/assistant/suggest_speaker_names.go`), enrutado igual que cualquier pedido de texto: DeepSeek si hay clave, OpenAI si no. Instrucción explícita de no inventar nombres; ante la duda, omite la etiqueta.
- `name_suggestions.go` arma el texto por hablante (`spk_0`, `spk_1`… o `generico` para el cupo sin diarizar; el micrófono siempre es contexto como "Yo", nunca algo a renombrar) y valida cada sugerencia contra las etiquetas que realmente existen en esa reunión — una etiqueta inventada por el modelo, o un intento de renombrar "Yo", se descarta antes de llegar a la UI.
- **Nunca se aplica solo.** Botón "Sugerir nombres" en la vista de reunión; cada sugerencia aparece como chip con "Aplicar" o "×", igual que cualquier otro cambio de nombre.
- Verificado con un backend simulado (sin llamar a DeepSeek/OpenAI de verdad): parseo de JSON limpio y con cercas de markdown, salida del modelo no-JSON (no debe romper, debe devolver cero sugerencias), error del proveedor (debe propagarse, no silenciarse), y las dos validaciones de seguridad (etiqueta inexistente, intento de renombrar el micrófono).

## Objetivo

Agregar modo **Transcribir reunión**: capturar conversación completa, mostrar intervenciones con hora y hablante, guardar resultado por sesión, **ver transcripciones de reuniones pasadas** y exportar archivo `.txt` UTF-8. Identificar voz del usuario cuando sea posible; permitir corregir nombres y dejar `Hablante desconocido` ante duda. Mantener función de copiloto independiente. **Reducir costo** usando DeepSeek para audio y texto; OpenAI sólo para visión.

## Decisiones tomadas (2026-11-16)

- Fase 1 sin pyannote ni diarización: `Yo` (pista micrófono) y `Participante` (pista salida).
- Audio se descarta tras transcribir; sólo se persiste texto y marcas de huecos.
- Exportación manual con botón y diálogo nativo, desde reunión en curso o reabierta.
- Reuniones visibles en lista de sesiones existente (`mode='meeting'`).
- Proveedores: DeepSeek ASR para transcripción, `deepseek-flash` para texto, OpenAI `/responses` para peticiones con imagen. Fallback a OpenAI si DeepSeek falla.

## Verificación en máquina de desarrollo (2026-11-16)

- `pw-record` (libpipewire 1.6.11) captura micrófono `Audio/Source` por serial: `pw-record --target 2152 --raw --rate 16000 --channels 1 --format s16 -` entregó ~1,8 s en 2 s de prueba (diferencia = arranque). Nodo listado por `pw-dump` como `Audio/Source`, estado `suspended` antes de grabar.
- `parseAudioSources` (`audio_sources.go:180`) filtra sólo `Stream/Output/Audio`; basta aceptar `Audio/Source` y añadir campo `Kind`.
- Wails `v2.15.0` (go.mod) incluye `runtime.SaveFileDialog` y `runtime.EventsEmit`; aún sin uso en repo.
- Migraciones embebidas con `PRAGMA user_version` (`migrations.go`); `0004_meeting_transcript.sql` encaja sin cambios al sistema.
- Cola copiloto `meetQueuedSegments = 4` (`app.go:861`) confirmada: no reutilizar `listenMeetAudio` para reunión.
- `gateway.Transcribe` (`openai.go:258`) reutilizable tal cual; `gpt-transcribe` sólo devuelve `text`, tiempo sale del offset del bloque.
- Imagen sólo viaja cuando `in.ImageData != ""` (`openai.go:91`): criterio directo para enrutar visión a OpenAI y resto a DeepSeek.

Ejemplo de exportación:

```text
Reunión: 2026-11-16 10:30
Idioma: español

[00:00:12] Yo: Buenos días. ¿Empezamos?
[00:00:16] Participante 2: Sí, comparto pantalla.
```

## Estado actual de Sonara

- `app.go:967-1109` graba con `pw-record` un único nodo de **salida** de navegador o Teams, 16 kHz mono. No graba micrófono local. Un canal ya mezclado tampoco permite obtener nombres de participantes desde metadatos de la aplicación.
- Detector de voz corta por 500 ms de silencio, sube fragmentos de hasta 15 s y cierra intervención tras cuatro fragmentos continuos (`app.go:840-859`). Cola de cuatro fragmentos puede descartar audio bajo carga.
- `audio_pipeline.go:22-94` envía cada fragmento a `backend.Transcribe`, une texto y solicita respuesta del copiloto. No conserva audio ni tiempos exactos. `audio_pipeline.go:230-248` guarda mensaje del usuario sólo después de respuesta exitosa.
- `backend/internal/adapters/web/integrations/openai/openai.go:258-333` usa `/v1/audio/transcriptions` con `gpt-transcribe`, lee sólo `text`. Modelo por defecto en `backend/internal/platform/config/config.go:32`.
- `store.go` persiste sesiones y mensajes del copiloto en SQLite. No existe tabla de intervenciones ni exportación. UI Vue en `frontend/src/App.vue` muestra última transcripción.

**Consecuencia:** exportar mensajes actuales no produciría transcripción fiel: faltaría voz propia, fallas del copiloto omitirían intervenciones y no habría hablantes ni tiempos.

## Opciones técnicas verificadas

| Opción | Qué entrega | Evaluación |
| --- | --- | --- |
| OpenAI `gpt-transcribe` | Texto de audio grabado; ya integrado | Mantener para texto. No entrega etiquetas de hablantes. |
| OpenAI `gpt-4o-transcribe-diarize` | Segmentos con `speaker`, `start`, `end`; hasta cuatro referencias de voz de 2–10 s con `diarized_json` | Útil para prototipo comparativo, **no base de producto nuevo**: retirado el 26-02-2027. Requiere `chunking_strategy=auto` para entradas mayores de 30 s; límite de archivo 25 MB. |
| OpenAI `gpt-live-transcribe` | Texto de audio en vivo | Alternativa futura para baja latencia. No entrega etiquetas de hablantes; integración WebSocket y audio PCM 24 kHz elevan cambio respecto de flujo actual. |
| `pyannote.audio` + modelo `community-1` | Diarización local, segmentos y embeddings por hablante | Candidato principal para separar voces remotas. Servicio Python/ML adicional, descarga de modelo y medición en hardware real. Biblioteca MIT; modelo CC BY 4.0 y acceso sujeto a condiciones de Hugging Face. |
| SpeechBrain `spkrec-ecapa-voxceleb` | Verificación de voz mediante referencia | Candidato para reconocer usuario en audio mezclado; requiere enrolamiento, umbral calibrado y salida `desconocido`. |
| WhisperX | Transcripción local, alineación y diarización apoyada en pyannote | Alternativa si se quiere reemplazar ASR de OpenAI. Más dependencias y trabajo; redundante si se mantiene `gpt-transcribe`. |
| `diart` | Diarización incremental en vivo | Investigar sólo si luego se necesitan nombres mientras hablan. MVP puede procesar por bloques o al finalizar. |

Fuentes: [OpenAI, transcripción de archivos](https://developers.openai.com/api/docs/guides/speech-to-text), [OpenAI, referencia de transcripciones](https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create), [OpenAI, obsolescencia](https://developers.openai.com/api/docs/deprecations), [OpenAI, transcripción en vivo](https://developers.openai.com/api/docs/guides/realtime-transcription), [pyannote.audio](https://github.com/pyannote/pyannote-audio), [modelo community-1](https://huggingface.co/pyannote/speaker-diarization-community-1), [SpeechBrain, verificación](https://github.com/speechbrain/speechbrain/blob/develop/recipes/VoxCeleb/SpeakerRec/README.md), [WhisperX](https://github.com/m-bain/WhisperX), [diart](https://github.com/juanmc2005/diart).

## Diseño recomendado

1. **Captura doble y consentimiento visible.** Crear control `Iniciar/Detener transcripción` separado de `Escuchar` del copiloto. Grabar simultáneamente micrófono elegido y salida de aplicación elegida en pistas distintas, con reloj común. No asumir que salida de reunión contiene voz propia. Indicar grabación activa y permitir borrar material.
2. **Texto durable.** Enviar bloques acotados de ambas pistas a `gpt-transcribe` mediante adaptador existente. Guardar cada resultado al llegar, antes de cualquier respuesta del copiloto, con `session_id`, pista, tiempo inicial/final, texto, estado y orden. Conservar audio temporal cifrado o protegido por permisos sólo hasta completar proceso o hasta retención elegida; evitar archivos interminables en memoria. Registrar huecos si captura o cola fallan.
3. **Hablantes.** Etiquetar pista de micrófono como `Yo` por origen físico, sin biometría. Aplicar `pyannote community-1` a bloques de salida con contexto suficiente o a grabación final; mantener IDs estables entre bloques. Comparar embeddings de voces remotas con referencia voluntaria de usuario sólo para detectar eco/voz propia presente también en salida. No unir voces por semejanza sin umbral calibrado. Otros participantes reciben `Participante 1/2…`; nombres asignados manualmente por usuario. Voz y etiqueta pueden corregirse tras reunión.
4. **Unión y exportación.** Fusionar segmentos por tiempo; marcar solapamientos y eliminar duplicados de voz propia entre pistas si hay eco. Guardar texto y etiquetas en SQLite; ofrecer `Exportar .txt` con encabezado, horas relativas, nombres corregidos y nota de huecos. Escritura atómica a ruta elegida mediante diálogo nativo; exportación repetible desde sesión reabierta.
5. **Desacoplamiento.** API interna nueva para transcripción de reunión y diarización, sin cambiar contrato del copiloto. UI con estado de grabación, lista de segmentos y corrección de hablantes. Cierre de sesión espera trabajo pendiente o muestra estado `procesando`; errores no deben hacer desaparecer texto ya obtenido.

## Implementación concreta en este repositorio

### 1. Fuentes PipeWire

`availableAudioSources` en `audio_sources.go` ejecuta `pw-dump` y filtra `media.class == Stream/Output/Audio`; esa misma inspección puede enumerar entradas `Audio/Source` (micrófonos) como flujo aparte, con tipo explícito `input/output`. No reutilizar `AudioSource.Target` sin tipo: UI debe evitar seleccionar una salida como micrófono. Verificar qué propiedades exponen `pw-dump` en las máquinas soportadas y cómo se comporta `pw-record --target SERIAL` con nodos fuente. El proceso debe recibir context cancelable y reportar salida/reconexión igual que `listenMeetAudio`.

Iniciar dos lectores bajo un solo contexto de grabación con reloj monotónico de inicio compartido. Si el microfono elegido desaparece, no adivinar otro: pausar esa pista y emitir evento/estado de error para evitar capturar un dispositivo inesperado. Selección de micrófono debe persistir separada de `preferred_audio_app`. **No usar loopback del monitor del sistema como sustituto**, pues podría incluir notificaciones y otros sonidos.

### 2. Protocolo Go/UI y estado

Agregar comandos Wails `StartMeetingTranscript`, `StopMeetingTranscript`, `MeetingTranscriptStatus`, `MeetingTranscriptSegments(sessionID)` y `ExportMeetingTranscript(sessionID)`. La captura de reunión tiene estado independiente de `listening` del copiloto y se detiene al cambiar sesión o cerrar aplicación; la transcripción ya guardada queda consultable al reabrir.

Wails runtime events (`runtime.EventsEmit`) pueden notificar segmento insertado y cambio de estado. La UI carga estado al abrir sesión y se suscribe a eventos; así no depende de polling de `ListeningStatus`, que hoy está acoplado a una respuesta inmediata del copiloto. Reusar el selector de fuentes con secciones `Salida de reunión` y `Micrófono`, botón visible de grabación y señal de pista ausente.

### 3. Audio y peticiones OpenAI

El formato PCM actual, 16 kHz mono s16, puede convertirse a WAV con `wavFromPCM`. Para reunión, usar bloques de tamaño/tiempo fijo, no sólo detección de silencios: voz continua no debe quedar sin transcribir. Cada pista se sube por separado a `/audio/transcriptions` con `model=gpt-transcribe`, `language=es|en`, y guardar intervalo absoluto del bloque para reubicar palabras/segmentos. Si hay que transcribir semanas de audio, respetar límite de 25 MB por archivo; bloques cortos quedan muy por debajo. Enviar contexto del bloque previo mediante `prompt` sólo si pruebas confirman continuidad y calidad: no enviar nombres de hablantes como afirmación.

`backend/internal/adapters/web/integrations/openai/openai.go` hoy construye multipart con `model`, `file`, idioma opcional y decodifica `text`; añadir una operación de reunión o ampliar tipos para retener segmentos/timestamps sin alterar `Transcribe` usado por copiloto. La referencia actual de OpenAI admite `gpt-transcribe` como modelo recomendado para archivos y limita cada carga a 25 MB. [Guía de archivos y límites](https://developers.openai.com/api/docs/guides/speech-to-text), [modelo gpt-transcribe](https://developers.openai.com/api/docs/models/gpt-transcribe).

### 4. Almacenamiento y exportación

Crear migración `0004_meeting_transcript.sql`: tabla `transcript_segments` con `id`, `session_id` FK cascade, `track` (`microphone`/`meeting_output`), `start_ms`, `end_ms`, `speaker_id`, `text`, `status` y orden de inserción; índices `(session_id, start_ms)` y `(session_id, track, start_ms)`. Tabla `transcript_speakers` con ID, nombre visible, origen (`self`/`diarized`/`manual`) y speaker ID de diarización. Persistir cada segmento inmediatamente; la falla de OpenAI crea estado fallido/hueco legible y reintentable, nunca un borrado silencioso.

La exportación debe consultar SQLite y componer contenido por tiempo, no reconstruir desde Vue. Usar diálogo nativo Wails para elegir destino. Escribir primero archivo temporal en directorio destino y renombrar al `.txt`; formatear texto como UTF-8, tiempos relativos `HH:MM:SS` y nombres editados. Wails tiene `SaveFileDialog` para obtener la ruta. No exportar ni guardar audio fuente en MVP: persistir texto y marcas de huecos reduce retención de voz; almacenar PCM por toda la reunión crecería a unos 115 MB/h por pista en mono 16 kHz s16 antes de WAV/comprimido.

### 5. Diarización y «reconoce mi voz»

Primero probar vía de menor complejidad: micrófono aislado da certeza de que esa intervención provino del usuario; diarizar únicamente salida de reunión da IDs anónimos para interlocutores. Alinear ambas pistas en reloj común y detectar eco con similitud/ventana temporal. No etiquetar por la voz de salida si el usuario está remoto y su micrófono también se reproduce allí sin verificar eco.

Si se requiere identificar `Yo` dentro de salida mezclada, permitir grabar muestra voluntaria (idealmente varias frases de 5–10 s), extraer embedding local y comparar con segmentos largos/limpios; muestras cortas, solapadas o con ruido quedan `desconocido`. En pyannote Community-1 `output.speaker_embeddings` entrega un embedding por ID diarizado, útil para matching experimental; el modelo no asigna nombres civiles. El pipeline admite fijar min/max speakers, produce diarización normal y exclusiva, y su modelo requiere aceptar términos/acceso en Hugging Face. [Salida y embeddings de pyannote](https://github.com/pyannote/pyannote-audio/blob/main/src/pyannote/audio/pipelines/speaker_diarization.py), [ficha del modelo](https://huggingface.co/pyannote/speaker-diarization-community-1). Ejecutar como servicio local Python separado del backend Go, iniciado bajo demanda; health check, timeout y versión fijada. No instalar PyTorch/modelos al iniciar la app ni bloquear interfaz. Evaluar tamaño de paquete y uso de CPU/GPU antes de incluirlo en instalador.

La referencia de OpenAI diarize admite muestras conocidas, pero el modelo se retira el 26-02-2027; usarlo sólo para comparar calidad de un prototipo, no como requisito de ejecución. [Fecha de retirada y reemplazos](https://developers.openai.com/api/docs/deprecations). La transcripción Realtime actual tampoco resuelve diarización: `gpt-live-transcribe` no devuelve etiquetas de hablante. [Limitaciones de Realtime](https://developers.openai.com/api/docs/guides/realtime-transcription).

### 6. Ver transcripciones de reuniones

- Reunión = sesión con `mode='meeting'` en tabla `sessions` existente. Aparece en lista actual (`Sessions()`/`recentSessions`) con icono propio y filtro opcional; sin pantalla nueva de listado.
- Abrir sesión de reunión: `OpenSession` detecta modo y UI carga `MeetingTranscriptSegments(id)`; muestra `[HH:MM:SS] Yo|Participante: texto`, huecos marcados (`[00:12:40–00:13:00] (sin transcribir)`) y estado por pista.
- Reunión en curso: UI se suscribe a `transcript:segment` y `transcript:status` (`runtime.EventsEmit`); lista crece en vivo sin polling.
- Botón `Exportar .txt` en vista de reunión, activo en curso y en sesiones reabiertas.
- `DeleteSession` existente borra segmentos por `ON DELETE CASCADE`.
- Fuera de fase 1: edición de texto en app, búsqueda entre reuniones, exportar PDF/Markdown.

### 7. Proveedores de IA: DeepSeek para audio/texto, OpenAI para visión

Objetivo: bajar costo sin tocar calidad de visión. Precios verificados 2026-11-16; confirmar en páginas oficiales antes de producción.

| Uso | Proveedor | Endpoint | Precio | Limitaciones |
| --- | --- | --- | --- | --- |
| Transcripción | **DeepSeek ASR** | `POST https://api.deepseek.com/v1/tools/asr`, multipart campo `file` | $0.116/h (oferta temporal $0.058/h), facturado por segundo | Máx. 120 s y 8 MiB por petición; WAV PCM 16-bit u Ogg Opus; mono o estéreo (sólo canal izquierdo); **sin parámetro idioma** (autodetecta 21 idiomas); respuesta `{request_id, text, duration_ms}` sin timestamps ni hablantes; sin streaming; máx. 50 peticiones concurrentes (luego `429`). |
| Transcripción (alternativa) | Groq `whisper-large-v3-turbo` | `https://api.groq.com/openai/v1/audio/transcriptions` (compatible OpenAI) | $0.04/h | Acepta `language`; `verbose_json` con timestamps por segmento; 25 MB (free) / 100 MB (dev); sin diarización. |
| Texto (copiloto, títulos, traducción) | **DeepSeek `deepseek-flash`** (V4.1-Flash) | `POST https://api.deepseek.com/chat/completions`, `stream: true` | Fuentes de terceros: ~$0.15 entrada / $0.60 salida por 1M tokens fuera de hora pico, doble en pico | **No soporta `/responses`**; requiere adaptador `chat/completions`. `deepseek-v4-pro` para modo experto si hace falta. |
| Visión (captura de pantalla) | OpenAI (actual) | `/responses` con `input_image` | Sin cambio | — |

Costo estimado reunión 1 h con dos pistas: DeepSeek ~$0.12–0.23, Groq ~$0.08, OpenAI `whisper-1` ~$0.72 (`gpt-transcribe` sin precio verificado).

**Privacidad:** audio de reuniones (voz propia y de terceros) se envía a servidores de DeepSeek. Mostrar proveedor activo en UI de grabación y permitir elegir otro en configuración.

Implementación en `backend/`:

- **Enrutamiento:** en `Respond`/`RespondStream`, `in.ImageData != ""` → OpenAI `/responses` (código actual); sin imagen → DeepSeek `/chat/completions`. Adaptador nuevo para SSE de `chat/completions` (`choices[0].delta.content`, fin con `[DONE]`), ~80 líneas, mismo `onDelta`. Mapear `instructions` a mensaje `system` e historial a `messages`.
- **Transcripción DeepSeek:** `Transcribe` alternativo (~40 líneas) que cumple misma firma y devuelve `domain.Transcript`; ignora `language`, rechaza bloques > 120 s / 8 MiB antes de subir. Bloques de reunión (~20 s) caben con holgura.
- **Configuración** (`backend/internal/platform/config/config.go`): `DEEPSEEK_API_KEY`, `DEEPSEEK_BASE_URL` (default `https://api.deepseek.com`), `SONARA_TEXT_PROVIDER` y `SONARA_TRANSCRIBE_PROVIDER` (`openai|deepseek`), `DEEPSEEK_MODEL_FAST` (default `deepseek-flash`). Clave guardada igual que `SetOpenAIAPIKey`.
- **Fallback:** error de red/5xx/`429` de DeepSeek → reintentar una vez con OpenAI y registrar en log; nunca perder segmento (queda `failed` si ambos fallan).
- **Riesgo idioma:** autodetección DeepSeek puede fallar en frases cortas o mezcla es/en. Probar con audio real antes de pasar copiloto en vivo; si falla, usar Groq (acepta `language`) para copiloto y DeepSeek sólo para reunión.
- **Rollout:** primero DeepSeek ASR en transcripción de reunión (tolera latencia, mayor ahorro). Después `deepseek-flash` para texto. Copiloto en vivo sigue en OpenAI hasta comparar latencia.

### 8. Referencias open source

Ningún proyecto en Go/Wails; código no reutilizable directo, sí como diseño. Todos MIT salvo indicación.

- [Quinoa](https://github.com/thisisryanswift/quinoa) (Python/PyQt6 + Rust): micrófono y sistema en pistas separadas vía PipeWire, SQLite, transcripción con Gemini. Arquitectura más parecida; ver `quinoa_audio/src/device/` (descubrimiento y hot-plug de dispositivos) y `quinoa/storage/database.py`.
- [heed](https://github.com/isjunrod/heed) (TypeScript + Python): diarización por canal micrófono/sistema; Linux con faster-whisper + pyannote 3.1, sin diarización en vivo en Linux.
- [Meetily](https://github.com/Zackriya-Solutions/meetily), [Millet](https://github.com/pretyflaco/meetscribe), [Tapeback](https://github.com/yastcher/tapeback): locales con Whisper + pyannote; referencia para fase 2.

### Orden recomendado de trabajo

1. Probar enumeración y captura real de micrófono en PipeWire, incluyendo cancelación y desaparición de nodo. (Captura básica ya verificada.)
2. Añadir tabla, cola por pista, segmentos persistidos y exportación sin diarización; validar el `.txt` de sesión cerrada/reabierta.
3. Añadir UI de selección, inicio/pausa, estado por pista, vista de reunión en lista de sesiones y botón exportar.
4. Añadir proveedor DeepSeek ASR para transcripción de reunión con fallback OpenAI; comparar calidad es/en con 3–5 grabaciones.
5. Añadir adaptador `chat/completions` y enrutamiento texto→DeepSeek / imagen→OpenAI; medir latencia de copiloto antes de activarlo por defecto.
6. ~~Integrar pyannote como proceso opcional~~ — superado: se usó la API de Gemini en la nube en vez de un proceso local (ver "Fase 2 implementada" arriba). Falta medir con cuenta real: 3–5 grabaciones consentidas, calidad, eco y voces solapadas.
7. Sólo tras métrica aceptable, añadir enrolamiento/matching de voz, o el atajo más simple de inferir nombres leyendo el texto de la transcripción; calibrar falso positivo frente a fallback `desconocido`.

## Detalles a validar en máquina objetivo

- Sintaxis/propiedades de `pw-record` para capturar un `Audio/Source` y el formato de `pw-dump` en la versión instalada; documentación upstream: [pw-record](https://docs.pipewire.org/page_man_pw-record_1.html), [pw-dump](https://docs.pipewire.org/page_man_pw-dump_1.html).
- Si Wails permite `SaveFileDialog` en los targets de empaquetado configurados; API: [Wails dialogs](https://wails.io/docs/reference/runtime/dialog/).
- Costo/latencia real de diarización CPU. La cifra publicada de pyannote Community-1 (31 segundos por hora de audio) corresponde a H100, no predice rendimiento en portátil. Tratarlo como indicador, no como promesa.

## Fases y esfuerzo orientativo

| Fase | Entrega verificable | Esfuerzo estimado |
| --- | --- | --- |
| 1. Captura y texto | Dos pistas, persistencia independiente, vista de reuniones, `.txt` con `Yo` y `Participante` genérico | 3–5 días de desarrollo |
| 1b. Proveedores baratos | DeepSeek ASR + `deepseek-flash` con fallback OpenAI, visión en OpenAI, comparación de calidad/latencia | 2–3 días |
| 2. Diarización remota | IDs estables, segmentos con tiempo, corrección de nombres, comparación con audio real | 4–8 días |
| 3. Reconocimiento de voz | Referencia opcional, detección de eco propio, calibración y manejo de desconocidos | 2–5 días adicionales |
| 4. Robustez | Reconexión de nodos, colas, solapamientos, exportación al cerrar, retención/borrado | 3–5 días |

Complejidad: **media** para texto y exportación; **alta** para hablantes fiables. Estimaciones condicionadas por pruebas en llamadas reales y capacidad del equipo para ejecutar modelo local. No prometer reconocimiento nominal de terceros desde audio solo: diarización separa voces; identidad necesita referencia o asignación humana.

## Prueba de viabilidad antes de elegir motor de hablantes

- Reunir, con permiso, 3–5 grabaciones breves representativas: dos y tres participantes, español/inglés, interrupciones, auriculares/altavoces y micrófono habitual. Mantener muestras fuera de repositorio.
- Medir captura de ambas pistas, desfase, segmentos perdidos, latencia, calidad de texto, errores de separación y falsos `Yo`.
- Comparar `pyannote community-1` con salida temporal de OpenAI diarize sólo como referencia; verificar licencia y recursos CPU/GPU del equipo objetivo. Si diarización local falla en hardware objetivo, ofrecer etiquetas manuales y evaluar servicio externo vigente.
- Criterios de aceptación: ningún tramo se pierde sin aviso ante carga o reconexión; exportación conserva orden, horas y correcciones; voz propia aparece al hablar por micrófono; voces inciertas quedan sin nombre; sesión puede cerrarse y exportarse luego.

## Decisiones pendientes de producto

- Retención de audio: decidido borrar tras procesar en fase 1; revisar si se pide conservar grabación.
- Ruta por defecto del diálogo de exportación (p. ej. `~/Documentos`).
- Aceptación de envío de audio a DeepSeek: aviso único o selector de proveedor visible por reunión.
- Reconocimiento opcional de otras personas mediante muestras consentidas, o sólo nombres manuales.
- Soporte inicial exclusivo Linux/PipeWire, coherente con captura actual, o plan adicional multiplataforma.
