package main

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	sonaraBackend "sonara/backend/client"
)

type turnAccumulator struct {
	session    uint64
	turn       uint64
	nextPart   uint64
	transcript string
	audio      []byte
	failed     bool
}

// One worker survives recorder restarts. It transcribes in capture order and
// starts a reply only after the final chunk of an intervention arrives.
func (a *App) runSegmentWorker() {
	var turn turnAccumulator
	for item := range a.segments {
		a.mu.RLock()
		current := item.session == a.sessionGeneration
		ctx := a.voiceContext
		backend := a.backend
		a.mu.RUnlock()
		if !current {
			continue
		}
		if turn.session != item.session || turn.turn != item.turn {
			turn = turnAccumulator{session: item.session, turn: item.turn, nextPart: 1}
		}
		if item.part != turn.nextPart {
			turn.failed = true
		}
		turn.nextPart = item.part + 1
		if turn.failed {
			if item.final {
				logf("turn %d/%d discarded: a chunk was lost before part %d", item.session, item.turn, item.part)
				a.setSessionStatus(item.session, "Audio incompleto: se perdió un fragmento")
			}
			continue
		}
		if item.manual {
			turn.audio = appendPCMChunk(turn.audio, item.pcm)
			if item.final {
				a.holdPendingAudio(item.session, turn.audio)
			}
			continue
		}
		if backend == nil {
			a.setSessionStatus(item.session, "Backend no inicializado")
			turn.failed = true
			continue
		}
		a.setSessionStatus(item.session, "Sonara transcribiendo…")
		started := time.Now()
		output, err := backend.Transcribe(ctx, wavFromPCM(item.pcm), "meet-segment.wav", item.language)
		if err != nil {
			// A cancelled context is the user pausing or starting a new session,
			// not a fault, so it is not worth a log line.
			if ctx.Err() == nil {
				logf("transcription FAILED turn=%d/%d part=%d audio=%.1fs lang=%s %s: %v",
					item.session, item.turn, item.part,
					float64(len(item.pcm))/float64(meetSampleRate*2), item.language,
					transcribeDiagnostics(output, started), err)
				a.setSessionStatus(item.session, "Error de transcripción: "+err.Error())
			}
			turn.failed = true
			continue
		}
		// Every dimension needed to classify a latency outlier without guessing:
		// how much audio, how big the upload, how long OpenAI said it spent, how
		// long the headers took versus the body, and the request id to hand them.
		logf("transcribed turn=%d/%d part=%d audio=%.1fs chars=%d %s",
			item.session, item.turn, item.part,
			float64(len(item.pcm))/float64(meetSampleRate*2),
			len(strings.TrimSpace(output.Text)),
			transcribeDiagnostics(output, started))
		turn.transcript = joinTranscriptPieces(turn.transcript, strings.TrimSpace(output.Text))
		if !item.final {
			continue
		}
		if turn.transcript == "" {
			a.setSessionStatus(item.session, "No se entendió la intervención")
			continue
		}
		a.startVoiceResponse(item.session, item.language, turn.transcript, item.queuedAt)
	}
}

// transcribeDiagnostics renders one call's measurements in a stable key=value
// shape so a run of outliers can be grepped and correlated.
//
// wait is time to the response headers and total adds reading the body, so a
// slow network and a queue wait on OpenAI's side stop looking alike. proc is
// OpenAI's own figure: wait well above proc points at the network or a queue,
// wait close to proc points at the model. There is no retry anywhere in the
// gateway, so every line is a single attempt by construction.
func transcribeDiagnostics(out sonaraBackend.Transcript, started time.Time) string {
	return fmt.Sprintf("upload=%dKB status=%d proc=%dms wait=%dms total=%dms wall=%dms req=%s",
		out.UploadBytes/1024, out.Status, out.ProcessingMS, out.WaitMS, out.TotalMS,
		time.Since(started).Milliseconds(), orDash(out.RequestID))
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func appendPCMChunk(existing, chunk []byte) []byte {
	if len(existing) == 0 {
		return append([]byte(nil), chunk...)
	}
	overlap := meetOverlapFrames * meetFrameBytes
	if overlap > len(chunk) {
		overlap = len(chunk)
	}
	return append(existing, chunk[overlap:]...)
}

func joinTranscriptPieces(previous, next string) string {
	if previous == "" {
		return next
	}
	if next == "" {
		return previous
	}
	left, right := strings.Fields(previous), strings.Fields(next)
	for count := min(8, len(left), len(right)); count > 0; count-- {
		match := true
		for i := range count {
			if normalizeTranscriptWord(left[len(left)-count+i]) != normalizeTranscriptWord(right[i]) {
				match = false
				break
			}
		}
		if match {
			return previous + " " + strings.Join(right[count:], " ")
		}
	}
	return previous + " " + next
}

func normalizeTranscriptWord(word string) string {
	return strings.ToLower(strings.TrimFunc(word, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}))
}

func (a *App) holdPendingAudio(session uint64, audio []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionGeneration != session {
		return
	}
	a.pendingAudio = append([]byte(nil), audio...)
	a.listenStatus = fmt.Sprintf("Fragmento de %.1fs listo para enviar", float64(len(audio))/float64(meetSampleRate*2))
}

func (a *App) setSessionStatus(session uint64, status string) {
	a.mu.Lock()
	if a.sessionGeneration == session {
		a.listenStatus = status
	}
	a.mu.Unlock()
}

// queuedAt is when the final segment of the turn left the recorder: the clock
// for the one number that matters, voice end -> first token.
func (a *App) startVoiceResponse(session uint64, language, transcript string, queuedAt time.Time) {
	a.mu.Lock()
	if a.sessionGeneration != session {
		a.mu.Unlock()
		return
	}
	if a.replyCancel != nil {
		a.replyCancel()
	}
	ctx, cancel := context.WithCancel(a.voiceContext)
	a.replyCancel = cancel
	a.replySequence++
	replyID := a.replySequence
	a.transcript = transcript
	a.translation, a.voiceReply = "", ""
	a.voiceSequence++
	a.streaming = true
	a.listenStatus = "Sonara respondiendo…"
	mode := a.assistantMode
	contextText := a.buildSessionContextLocked()
	backend := a.backend
	a.mu.Unlock()
	go func() {
		defer cancel()
		if backend == nil {
			a.finishVoiceResponse(session, replyID, transcript, "", fmt.Errorf("backend no inicializado"))
			return
		}
		if language == "en" {
			// What they said, in Spanish, runs beside the reply instead of in
			// front of it. A failure here only loses the translation line.
			go a.streamTranslation(ctx, backend, session, replyID, transcript, queuedAt)
		}
		var streamed strings.Builder
		asked := time.Now()
		var firstToken time.Duration
		output, err := backend.RespondStream(ctx, voicePrompt(language, transcript), mode, contextText, func(delta string) {
			streamed.WriteString(delta)
			if firstToken == 0 {
				firstToken = time.Since(asked)
			}
			translation, reply := splitTranslation(streamed.String())
			a.mu.Lock()
			if a.sessionGeneration == session && a.replySequence == replyID {
				if translation != "" {
					a.translation = translation
				}
				a.voiceReply = reply
			}
			a.mu.Unlock()
		})
		if err == nil {
			// Time to first token is the number the user actually feels; the
			// total only says how long the full answer took to finish writing.
			logf("reply for session %d reply %d (%s/%s): first token %dms, complete %dms, %d chars, %d ctx chars, voice end -> first token %dms",
				session, replyID, mode, language, firstToken.Milliseconds(),
				time.Since(asked).Milliseconds(), len(output.Text), len(contextText),
				sinceMS(queuedAt, asked.Add(firstToken)))
		}
		a.finishVoiceResponse(session, replyID, transcript, output.Text, err)
	}()
}

// sinceMS is end-start in ms, or -1 when start was never recorded (manual
// sends and tests have no queue time).
func sinceMS(start, end time.Time) int64 {
	if start.IsZero() {
		return -1
	}
	return end.Sub(start).Milliseconds()
}

// streamTranslation fills a.translation with the Spanish version of what the
// other person said. Guarded by session and reply id like the reply itself, so
// a late translation can never land on the next turn.
func (a *App) streamTranslation(ctx context.Context, backend *sonaraBackend.Client, session, replyID uint64, transcript string, queuedAt time.Time) {
	asked := time.Now()
	var first time.Duration
	var streamed strings.Builder
	_, err := backend.TranslateStream(ctx, transcript, func(delta string) {
		streamed.WriteString(delta)
		if first == 0 {
			first = time.Since(asked)
		}
		a.mu.Lock()
		if a.sessionGeneration == session && a.replySequence == replyID {
			a.translation = strings.TrimSpace(streamed.String())
		}
		a.mu.Unlock()
	})
	if err != nil {
		if ctx.Err() == nil {
			logf("translation failed for turn %d reply %d: %v", session, replyID, err)
		}
		return
	}
	logf("translation for session %d reply %d: first token %dms, voice end -> first token %dms",
		session, replyID, first.Milliseconds(), sinceMS(queuedAt, asked.Add(first)))
}

func (a *App) finishVoiceResponse(session, replyID uint64, transcript, response string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionGeneration != session || a.replySequence != replyID {
		return
	}
	a.streaming = false
	a.replyCancel = nil
	if err != nil {
		logf("reply failed for turn %d (mode %s, %d chars streamed): %v", session, a.assistantMode, len(response), err)
		a.listenStatus = "Error de respuesta: " + err.Error()
		return
	}
	translation, reply := splitTranslation(response)
	// The translation of what they said arrives on its own request now; only a
	// model that still emitted the old marker line may overwrite it here.
	if translation != "" {
		a.translation = translation
	}
	a.voiceReply = reply
	a.appendSessionMessageLocked("user", transcript)
	a.appendSessionMessageLocked("assistant", reply)
	a.listenStatus = "Respuesta sugerida lista"
}
