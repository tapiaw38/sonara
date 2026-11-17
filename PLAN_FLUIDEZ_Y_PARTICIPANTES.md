# Plan: fluidez en inglés y detección de participantes

Fecha: 2026-11-17. Estado: implementado todo el plan: aviso de participantes, fase 1 (traducción en paralelo, métrica, sondeo a 100 ms), fase 2 (transcripción en vivo por WebSocket), fase 3 (respuesta anticipada) y pasada periódica de participantes. **Sin verificar contra los servicios reales** (ver abajo).

Dos problemas reportados:

1. En inglés, la transcripción y la respuesta sugerida tardan: se siente poco fluido.
2. En "Transcribir", todos los que hablan en la reunión salen como `Participante`.

## 1. Participantes: por qué no se detectan hoy

Causa encontrada en la base y el log de esta máquina, no es un fallo del código de separación:

- **No hay clave de Gemini guardada.** La separación de participantes sólo corre si hay clave de Gemini, y sólo una vez, al detener la grabación. Sin clave, `runDiarization` no hace nada y todo queda como `Participante`. La reunión del 2026-11-16 21:03 (39 fragmentos) tiene `speaker_key` vacío en todos.
- **La app no lo avisa.** La vista no dice que la separación está apagada, así que parece un fallo.
- Aun con clave, los nombres aparecen **recién al terminar**, nunca durante la reunión.
- Aparte: la configuración de Azure guardada tiene las dos versiones de API vacías y no hay proveedores elegidos, así que hoy todo sigue yendo por OpenAI.

### Opciones

| Opción | Qué da | Costo / riesgo |
| --- | --- | --- |
| **A. Poner la clave de Gemini** (sin código) | `Participante 1/2/…` al detener la reunión | Nunca se probó con una cuenta real; calidad desconocida. |
| **B. Aviso en la vista** | La vista dice "separación de participantes apagada: falta clave de Gemini" y, al terminar, si falló y por qué | Cambio chico de interfaz. |
| **C. Pasada periódica con Gemini** | Etiquetas durante la reunión: repetir la separación cada 2–3 min sobre el audio acumulado | Reutiliza lo construido. Cada pasada vuelve a subir todo el audio (crece con la duración) y la numeración puede cambiar entre pasadas. |
| **D. OpenAI `gpt-4o-transcribe-diarize` por bloque** | Hablante casi en vivo por bloque; admite hasta 4 muestras de voz de referencia (2–10 s) para mantener el mismo hablante entre bloques | Modelo en retirada: se apaga el 2027-02-26 (quedan ~3 meses). Máximo 4 hablantes de referencia. Sólo por `/v1/audio/transcriptions`. |
| **E. Azure AI Speech** (recurso nuevo) | Servicio hecho para esto, con separación en vivo | Otro recurso de Azure distinto del que ya tenés, protocolo propio, más trabajo. |

### Recomendación

1. **A + B primero**: es la única forma de saber si la separación con Gemini sirve. Sin esa prueba, cualquier otra inversión es a ciegas.
2. Si la calidad es aceptable: **C**, para que las etiquetas aparezcan durante la reunión.
3. Si la calidad es mala: evaluar **E**. No recomiendo **D** como base: el modelo se apaga en febrero.

Límite que no cambia con ninguna opción: separar voces no da nombres reales. Para eso ya están el renombrado manual y "Sugerir nombres".

## 2. Fluidez en inglés: dónde se va el tiempo

Medido en el log real de la sesión en inglés del 2026-11-16 20:53–20:57 (15 turnos):

| Etapa | Tiempo típico | Rango |
| --- | --- | --- |
| Silencio para cerrar el turno | 0,5 s | fijo |
| Transcripción (espera total) | 1,1 s | 0,5–2,6 s |
| — de eso, trabajo del modelo | 0,47 s | 0,3–0,7 s |
| Primer token de la respuesta | 1,9 s | 1,5–3,2 s |
| Sondeo de la interfaz | hasta 0,3 s | fijo |
| **Total hasta ver el primer texto** | **≈ 3,5–3,8 s** | hasta ≈ 6,5 s |

Tres cosas que explican la sensación de lentitud:

- **No se ve nada mientras la otra persona habla.** El texto aparece recién cuando termina, se sube el audio y vuelve la transcripción.
- **La transcripción pierde ~0,65 s fuera del modelo** (espera 1,1 s contra 0,47 s de proceso): conexión, subida y cola.
- **En inglés la respuesta sale detrás de la traducción.** El formato pide primero "lo que dijo, traducido", después la respuesta. El primer token medido es de la traducción; la respuesta sugerida empieza más tarde todavía.

### Mejoras, de menor a mayor esfuerzo

**Fase 1: sin cambiar de arquitectura**

- **Separar la traducción de la respuesta en dos pedidos en paralelo.** Uno traduce lo que dijo la otra persona; el otro genera la respuesta (y su traducción). La respuesta deja de esperar a la traducción. Cuesta un pedido más por turno.
- **Empujar el texto a la interfaz con eventos** (ya se usan en "Transcribir") en vez de sondear cada 300 ms.
- **Abrir la conexión cuando empieza la voz**, no cuando termina: ataca parte de los 0,65 s perdidos.
- **Probar Azure como proveedor** de transcripción y texto: el log ya mide espera y primer token, así que la comparación sale gratis una vez cargadas las versiones de API.
- **Probar esfuerzo de razonamiento mínimo** en el modelo rápido, si lo admite: el primer token de 1,5–3,2 s es alto para respuestas cortas.

**Fase 2: transcripción en vivo**

- OpenAI `gpt-live-transcribe` por WebSocket: devuelve texto parcial mientras la persona habla y el texto final al cerrar el turno. Elimina la subida del audio (~1,1 s) y deja ver el texto en el momento.
- Requisitos verificados en la documentación: PCM a 24 kHz (hoy se graba a 16 kHz), el corte de turno lo hace la app (ya existe), idioma por `languages`, no da hablantes ni tiempos por palabra.
- Necesita una biblioteca de WebSocket para Go (la biblioteca estándar no trae).
- Azure tiene transcripción en vivo con `gpt-4o-mini-transcribe`, pero ese modelo está en retirada; no encontré confirmación de `gpt-live-transcribe` en Azure.

**Fase 3: respuesta anticipada**

- Empezar a generar la respuesta con el texto parcial a los ~300 ms de silencio y cancelarla si la persona sigue hablando. Sólo tiene sentido con la fase 2. Gasta más tokens.

### Objetivo y medición

- Texto de lo que dicen: visible en menos de 0,5 s (fase 2).
- Respuesta sugerida: primer token a 1,5 s o menos del fin de la frase (fases 1–2).
- Agregar al log una sola cifra por turno, "fin de la voz → primer token de la respuesta", para comparar antes y después.

## Orden propuesto

1. Clave de Gemini + aviso en la vista (1A + 1B).
2. Fase 1 de fluidez, midiendo cada cambio contra los números de arriba.
3. Según el resultado de 1: pasada periódica (1C) o evaluar Azure AI Speech (1E).
4. Fase 2 (transcripción en vivo).

## Fuentes

- [OpenAI, transcripción en vivo](https://developers.openai.com/api/docs/guides/realtime-transcription)
- [OpenAI, transcripción de archivos y separación de hablantes](https://developers.openai.com/api/docs/guides/speech-to-text)
- [OpenAI, modelos en retirada](https://developers.openai.com/api/docs/deprecations)
- [Microsoft, transcripción en vivo con gpt-4o-transcribe por WebSocket](https://techcommunity.microsoft.com/blog/azure-ai-foundry-blog/real-time-speech-transcription-with-gpt-4o-transcribe-and-gpt-4o-mini-transcribe/4410353)
- [Microsoft Q&A, gpt-4o-transcribe-diarize en Azure](https://learn.microsoft.com/en-us/answers/questions/5864686/azure-openai-realtime-api-gpt-4o-transcribe-diariz) (fechas de retirada sin confirmar)

## Implementado (2026-11-17)

- **Fase 2, transcripción en vivo** (`backend/.../realtime`, `live_transcribe.go`): modo opt-in en Configuración → Proveedores → "Modo de transcripción en vivo". Streaming a `wss://api.openai.com/v1/realtime?intent=transcription` con `gpt-live-transcribe` (PCM 24 kHz, `delay: minimal`, sin detección de turno del servidor: el corte de turno sigue siendo el de la app, con el commit explícito). Texto parcial en pantalla mientras hablan. Sólo con OpenAI como proveedor de transcripción en vivo; si falla se muestra el error, **no hay vuelta al modo por lotes**. No admite el modo manual.
- **Fase 3, respuesta anticipada**: a los 300 ms de silencio se empieza la respuesta con el texto parcial; si vuelve la voz antes de los 500 ms se retira; cuando llega la transcripción final, si dice lo mismo (ignorando mayúsculas y puntuación) la respuesta sigue, si no se retira y se reinicia con el texto final. La interfaz borra el mensaje de toda respuesta retirada (`Retractions`). Una respuesta retirada cuesta tokens, nunca deja una respuesta equivocada en pantalla.
- **Participantes durante la reunión**: cada 3 min, si hay más de 1 min de audio nuevo, se vuelve a separar toda la grabación hasta ese momento y se etiquetan los fragmentos cubiertos. La pasada final al detener sigue teniendo la última palabra. Límite conocido: cada pasada vuelve a subir todo el audio (el costo crece con la duración) y la numeración puede cambiar entre pasadas; un nombre escrito para "Participante 2" sigue al número, no a la persona.

### Verificado y no verificado
- Verificado con servidores falsos: el protocolo del cliente (sesión, append en 24 kHz, commit, eventos, cierre), la alineación de turnos con los eventos `committed`, la anticipación y su retiro, el reinicio con texto distinto, y la pasada periódica.
- **No verificado**: ninguna conexión real a OpenAI Realtime. Los nombres de eventos y el `session.update` salen de la documentación oficial; en particular, que lleguen deltas antes del commit y cómo responde el servidor a un commit con poco audio son suposiciones razonables, no hechos comprobados. Probá primero una sesión corta y mirá el log (`live transcription …`).
- Sin hacer: empujar el texto con eventos en vez de sondear (con 100 ms de sondeo no hace falta), probar Azure como proveedor (faltan las versiones de API en tu configuración).
