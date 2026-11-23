package app

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"sonara/internal/audio"
	"sonara/internal/pipewire"
	"sonara/internal/practice"
)

// PracticeTurn is one exchange in the spoken interview-practice mode. Text is
// empty for the opening question, which has no candidate answer yet.
type PracticeTurn struct {
	Text      string // the candidate's transcribed answer
	ReplyText string // the interviewer's question/comment
	Audio     []byte // wav of ReplyText (Wails marshals []byte as base64 to JS)
}

const (
	// An empty transcript of less than this was noise or echo, not an answer
	// worth asking to repeat: listening just continues, silently.
	practiceRetryMinMS = 2000
)

// practiceRecording tracks the mic capture behind an in-flight PracticeListen
// call, so StopPractice can interrupt it (the user leaving mid-listen).
type practiceRecording struct {
	cancel context.CancelFunc
}

// StartPractice switches to practice mode. The candidate speaks first: the
// interviewer persona introduces itself in its reply to whatever they say.
func (a *App) StartPractice() (PracticeTurn, error) {
	a.SetAssistantMode("practice")
	return PracticeTurn{}, nil
}

// PracticeListen blocks until the candidate speaks and pauses, transcribes
// the answer, asks the interviewer persona for the next turn, and speaks it
// back. The frontend just calls this in a loop while the practice is live.
//
// While the reply is being prepared the microphone keeps listening: if the
// candidate goes on talking, that reply is dropped (nothing was stored yet),
// the new words join the answer and a reply to the whole of it is prepared.
func (a *App) PracticeListen() (PracticeTurn, error) {
	heard := a.recordSpokenAnswer(a.requestContext(), "", nil)
	if heard.err != nil {
		return PracticeTurn{}, heard.err
	}
	a.emit("practice:phase", "transcribing")
	answer, err := a.practiceAnswerText(heard)
	if err != nil {
		return PracticeTurn{}, err
	}
	answerMS := heard.ms()
	// After a stretch with no words (a breath, a keyboard) the reply is
	// prepared without listening again: noise can cost one retry, never a loop.
	listen := true
	for {
		voiced := make(chan struct{})
		more := make(chan practiceHeard, 1)
		listenCtx, stopListening := context.WithCancel(a.requestContext())
		if listen {
			go func(prefix string) {
				more <- a.recordSpokenAnswer(listenCtx, prefix, func() { close(voiced) })
			}(answer)
		} else {
			more <- practiceHeard{}
		}
		type result struct {
			turn PracticeTurn
			err  error
		}
		sessionCtx, language, answerID := a.nextPracticeAnswer()
		replyCtx, dropReply := context.WithCancel(sessionCtx)
		replied := make(chan result, 1)
		go func(text string, ms int) {
			turn, err := a.practiceAnswer(replyCtx, language, answerID, text, ms)
			replied <- result{turn, err}
		}(answer, answerMS)

		select {
		case r := <-replied:
			stopListening()
			<-more
			dropReply()
			return r.turn, r.err
		case <-voiced:
			// Cancelled and stale: neither stored nor spoken.
			dropReply()
			a.abandonPracticeAnswer(answerID)
			<-replied
			a.emit("practice:discard", answerID)
			extra := <-more
			stopListening()
			if extra.err != nil {
				return PracticeTurn{}, extra.err
			}
			a.emit("practice:phase", "transcribing")
			text, err := a.practiceAnswerText(extra)
			if err != nil {
				return PracticeTurn{}, err
			}
			if !practice.HasWords(text) {
				logf("practice: %dms of noise while preparing the reply, not listening again this turn", extra.ms())
				listen = false
				continue
			}
			logf("practice: answer continued while preparing the reply (+%dms)", extra.ms())
			answer, answerMS = strings.TrimSpace(answer+" "+text), answerMS+extra.ms()
		}
	}
}

// practiceHeard is one recorded stretch of the candidate's answer.
type practiceHeard struct {
	pcm      []byte
	streamed string
	ok       bool // streamed holds the transcript
	err      error
}

func (h practiceHeard) ms() int { return len(h.pcm) * 1000 / (audio.SampleRate * 2) }

// practiceAnswerText is the text of one stretch: streamed while spoken, or
// uploaded and transcribed now.
func (a *App) practiceAnswerText(heard practiceHeard) (string, error) {
	if heard.ok {
		return strings.TrimSpace(heard.streamed), nil
	}
	if len(heard.pcm) == 0 {
		return "", fmt.Errorf("no se grabó audio")
	}
	if a.backend == nil {
		return "", fmt.Errorf("Sonara backend is not initialized")
	}
	a.mu.RLock()
	ctx, language := a.voiceContext, a.audioLanguage
	a.mu.RUnlock()
	transcript, err := a.backend.TranscribePractice(ctx, audio.WAV(heard.pcm), language)
	if err != nil {
		logf("practice transcription failed: %v", err)
		return "", err
	}
	return strings.TrimSpace(transcript.Text), nil
}

// abandonPracticeAnswer makes answerID stale, so its reply is never stored.
func (a *App) abandonPracticeAnswer(answerID uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.practiceAnswerID == answerID {
		a.practiceAnswerID++
	}
}

// SetPracticePlayback reports whether the interviewer audio is playing.
func (a *App) SetPracticePlayback(playing bool) {
	a.practicePlaying.Store(playing)
}

// StopPractice interrupts an in-flight PracticeListen recording, for when the
// user leaves the practice view mid-listen.
func (a *App) StopPractice() {
	a.SetPracticePlayback(false)
	a.echoCanceller.Stop()
	a.mu.Lock()
	rec := a.practiceRec
	a.practiceRec = nil
	a.practiceAnswerID++
	a.mu.Unlock()
	if rec != nil {
		rec.cancel()
	}
}

// practiceAnswer asks the interviewer persona for the next turn after the
// candidate's answer and speaks it back.
// ctx is the reply's own context: cancelling it drops the reply.
func (a *App) practiceAnswer(ctx context.Context, language string, answerID uint64, candidateText string, answerMS int) (PracticeTurn, error) {
	if a.backend == nil {
		return PracticeTurn{}, fmt.Errorf("Sonara backend is not initialized")
	}
	started := time.Now()
	transcribeTime := time.Since(started)
	if candidateText == "" && answerMS < practiceRetryMinMS {
		// Echo or a noise burst, not an answer: asking to repeat would be
		// heard by the mic again and keep the interviewer talking to itself.
		logf("practice: %dms of audio with no words, listening again", answerMS)
		return PracticeTurn{}, nil
	}
	started = time.Now()
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reply := practice.RetryPrompt(language)
	var speech <-chan practiceSpeech
	commit := func() error { return nil }
	var err error
	if candidateText != "" {
		a.emit("practice:phase", "thinking")
		reply, speech, commit, err = a.requestPracticeTurn(turnCtx, ctx, answerID, candidateText, language)
		if err != nil {
			return PracticeTurn{}, err
		}
	} else {
		speech = a.startPracticeSpeech(turnCtx, ctx, answerID, reply, language)
	}
	replyTime := time.Since(started)
	started = time.Now()
	spoken := <-speech
	replyAudio, err := spoken.audio, spoken.err
	if err != nil {
		logf("practice TTS failed: %v", err)
		return PracticeTurn{}, err
	}
	if !a.currentPracticeAnswer(ctx, answerID) {
		return PracticeTurn{}, fmt.Errorf("la práctica cambió durante la respuesta")
	}
	if err := commit(); err != nil {
		return PracticeTurn{}, err
	}
	logf("practice turn: transcription=%s reply=%s audio_wait=%s empty=%t", transcribeTime.Round(time.Millisecond), replyTime.Round(time.Millisecond), time.Since(started).Round(time.Millisecond), candidateText == "")
	return PracticeTurn{Text: candidateText, ReplyText: reply, Audio: replyAudio}, nil
}

func (a *App) nextPracticeAnswer() (context.Context, string, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.practiceAnswerID++
	return a.voiceContext, a.audioLanguage, a.practiceAnswerID
}

type practiceText struct {
	TurnID  uint64 `json:"turnId"`
	English string `json:"en"`
	Spanish string `json:"es"`
}

type practiceSpeech struct {
	audio []byte
	err   error
}

const practiceSuggestionMarker = "[[SUGGESTION]]"

// requestPracticeTurn streams the interviewer's next turn. Nothing is stored
// until the caller runs commit, once the audio is ready: a reply dropped
// before that (the candidate kept talking) leaves no trace in the history.
func (a *App) requestPracticeTurn(turnCtx, sessionCtx context.Context, answerID uint64, content, language string) (string, <-chan practiceSpeech, func() error, error) {
	if a.backend == nil {
		return "", nil, nil, fmt.Errorf("Sonara backend is not initialized")
	}
	a.mu.RLock()
	sessionContext, generation := a.buildSessionContextLocked(), a.sessionGeneration
	request := practice.FrameAnswer(practice.LastInterviewerQuestion(a.sessionHistory), content, language)
	a.mu.RUnlock()

	var streamed strings.Builder
	var speech <-chan practiceSpeech
	output, err := a.backend.RespondStream(turnCtx, request, "practice-turn", sessionContext, func(delta string) {
		streamed.WriteString(delta)
		if !a.currentPracticeAnswer(sessionCtx, answerID) {
			return
		}
		question, suggestion, found := strings.Cut(streamed.String(), practiceSuggestionMarker)
		question = strings.TrimSpace(question)
		// Shown as it is written; the translation arrives once each part is done.
		if !found {
			a.emit("practice:question", partialPracticeText(answerID, question, language))
			return
		}
		if speech == nil && question != "" {
			speech = a.startPracticeSpeech(turnCtx, sessionCtx, answerID, question, language)
		}
		if suggestion = strings.TrimSpace(suggestion); suggestion != "" {
			a.emit("practice:suggestion", partialPracticeText(answerID, suggestion, language))
		}
	})
	if err != nil {
		return "", nil, nil, err
	}
	question, suggestion, _ := strings.Cut(output.Text, practiceSuggestionMarker)
	question, suggestion = strings.TrimSpace(question), strings.TrimSpace(suggestion)
	if question == "" {
		return "", nil, nil, fmt.Errorf("el simulador no generó una pregunta")
	}
	commit := func() error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.sessionGeneration != generation || a.practiceAnswerID != answerID {
			return fmt.Errorf("la práctica cambió durante la respuesta")
		}
		a.appendSessionMessageLocked("user", content)
		a.appendSessionMessageLocked("assistant", question)
		return nil
	}
	if !a.currentPracticeAnswer(sessionCtx, answerID) {
		return "", nil, nil, fmt.Errorf("la práctica cambió durante la respuesta")
	}
	if speech == nil {
		speech = a.startPracticeSpeech(turnCtx, sessionCtx, answerID, question, language)
	}
	if suggestion != "" {
		a.publishPracticeText(sessionCtx, answerID, "practice:suggestion", suggestion, language)
	}
	return question, speech, commit, nil
}

func (a *App) startPracticeSpeech(turnCtx, sessionCtx context.Context, answerID uint64, question, language string) <-chan practiceSpeech {
	a.publishPracticeText(sessionCtx, answerID, "practice:question", question, language)
	a.emit("practice:phase", "synthesizing")
	result := make(chan practiceSpeech, 1)
	go func() {
		audio, err := a.backend.Speak(turnCtx, question)
		result <- practiceSpeech{audio: audio, err: err}
	}()
	return result
}

func (a *App) currentPracticeAnswer(ctx context.Context, answerID uint64) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.practiceAnswerID == answerID && ctx.Err() == nil
}

func partialPracticeText(answerID uint64, original, language string) practiceText {
	if language == "en" {
		return practiceText{TurnID: answerID, English: original}
	}
	return practiceText{TurnID: answerID, Spanish: original}
}

func (a *App) publishPracticeText(ctx context.Context, answerID uint64, event, original, language string) {
	text := practiceText{TurnID: answerID, Spanish: original}
	target := "en"
	if language == "en" {
		text = practiceText{TurnID: answerID, English: original}
		target = "es"
	}
	a.emit(event, text)
	go func() {
		translationCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		translated, err := a.backend.TranslateTo(translationCtx, original, target)
		translated = strings.TrimSpace(translated)
		if err != nil || translated == "" {
			translated = "Traducción no disponible."
		}
		if target == "es" {
			text.Spanish = translated
		} else {
			text.English = translated
		}
		if a.currentPracticeAnswer(ctx, answerID) {
			a.emit(event, text)
		}
	}()
}

// practiceMicrophone returns a stable PipeWire node name that pw-record can target.
func (a *App) practiceMicrophone(ctx context.Context) (string, error) {
	if selected := a.SelectedMicrophone(); selected != "" {
		return selected, nil
	}
	sources, err := a.microphones(ctx)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.preferredMic != "" {
		return a.preferredMic, nil
	}
	for _, source := range sources {
		if source.App != "" {
			a.setMicrophoneLocked(source.App)
			return source.App, nil
		}
	}
	return "", fmt.Errorf("no hay micrófono disponible; revisá que PipeWire esté activo")
}

// recordSpokenAnswer captures one answer from the chosen microphone. When
// streaming works it also returns the answer's transcript (ok).
// recordSpokenAnswer records one stretch of the answer until a pause. prefix
// is what the candidate already said this turn (shown before the new words);
// voiced, if set, runs once when voice is first detected.
func (a *App) recordSpokenAnswer(parent context.Context, prefix string, voiced func()) practiceHeard {
	pcm, streamed, ok, err := a.recordSpokenStretch(parent, prefix, voiced)
	return practiceHeard{pcm: pcm, streamed: streamed, ok: ok, err: err}
}

func (a *App) recordSpokenStretch(parent context.Context, prefix string, voiced func()) (pcm []byte, streamed string, ok bool, err error) {
	recordCtx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	rec := &practiceRecording{cancel: cancel}
	a.mu.Lock()
	if a.practiceRec != nil {
		a.mu.Unlock()
		return nil, "", false, fmt.Errorf("ya se está escuchando")
	}
	a.practiceRec = rec
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.practiceRec == rec {
			a.practiceRec = nil
		}
		a.mu.Unlock()
	}()

	probeCtx, cancelProbe := context.WithTimeout(recordCtx, 5*time.Second)
	target, err := a.practiceMicrophone(probeCtx)
	cancelProbe()
	if err != nil {
		return nil, "", false, fmt.Errorf("el micrófono no está disponible: %w", err)
	}

	target = a.echoCanceller.Source(recordCtx, target)
	interruptFrames := practice.InterruptFrames
	if target == pipewire.EchoCancelSource {
		interruptFrames = practice.InterruptFramesAEC
	}
	command := exec.CommandContext(recordCtx, "pw-record", "--target", target, "--latency", "40ms", "--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, "", false, fmt.Errorf("no se pudo escuchar: %w", err)
	}
	if err := command.Start(); err != nil {
		stdout.Close()
		return nil, "", false, fmt.Errorf("no se pudo escuchar: %w", err)
	}
	defer func() {
		cancel()
		command.Wait()
	}()

	var input io.Reader = stdout
	if filter, filtered := practice.NoiseFilter(recordCtx, stdout); filter != nil {
		defer func() {
			cancel()
			filter.Wait()
		}()
		input = filtered
	}
	input = practice.NewLevelReader(input, func(level float64) { a.emit("practice:level", level) })
	a.mu.RLock()
	language := a.audioLanguage
	a.mu.RUnlock()
	stream := a.openPracticeStream(language, prefix)
	var voicedOnce sync.Once
	capturing := func() {
		a.emit("practice:phase", "capturing")
		if prefix != "" {
			a.emit("practice:partial", prefix)
		}
		if voiced != nil {
			voicedOnce.Do(voiced)
		}
	}
	onVoice := func([]byte) { capturing() }
	if stream != nil {
		input = io.TeeReader(input, stream)
		onVoice = func(soFar []byte) {
			stream.VoiceStarted(soFar)
			capturing()
		}
	}
	pcm, err = practice.ReadAnswer(recordCtx, interruptFrames, input, a.practicePlaying.Load, audio.NewSpeechDetector(), onVoice)
	if stream == nil {
		return pcm, "", false, err
	}
	if err != nil {
		stream.Close()
		return nil, "", false, err
	}
	streamed, ok = stream.Finish()
	return pcm, streamed, ok, nil
}

// openPracticeStream opens live transcription for one answer, or returns nil
// when it is unavailable and the answer will be uploaded instead.
func (a *App) openPracticeStream(language, prefix string) *practice.Stream {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()
	if backend == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	session, err := backend.OpenLive(ctx, language)
	if err != nil {
		logDedup("practice-stream", "practice streaming off, uploading answers: %v", err)
		return nil
	}
	return practice.NewStream(session, prefix, func(text string) { a.emit("practice:partial", text) })
}
