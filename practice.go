package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
	"time"
)

// PracticeTurn is one exchange in the spoken interview-practice mode. Text is
// empty for the opening question, which has no candidate answer yet.
type PracticeTurn struct {
	Text      string // the candidate's transcribed answer
	ReplyText string // the interviewer's question/comment
	Audio     []byte // wav of ReplyText (Wails marshals []byte as base64 to JS)
}

const (
	practiceStartFrames     = 2
	practiceLeadFrames      = 2
	practiceVoiceFrames     = 5
	practiceSilenceFrames   = 10
	practiceInterruptFrames = 6
	practiceInterruptFloor  = 0.012
	practiceEchoTailFrames  = 8 // 800ms ignored right after the interviewer stops
	// An empty transcript of less than this was noise or echo, not an answer
	// worth asking to repeat: listening just continues, silently.
	practiceRetryMinMS = 2000
)

// practiceRecording tracks the mic capture behind an in-flight PracticeListen
// call, so StopPractice can interrupt it (the user leaving mid-listen).
type practiceRecording struct {
	cancel context.CancelFunc
}

// practiceKickoff opens the interview in whichever language is configured
// (SetAudioLanguage), since there is no candidate utterance yet to mirror.
func practiceKickoff(language string) string {
	if language == "en" {
		return "Start the interview with an opening question, in English."
	}
	return "Arrancá la entrevista con una pregunta inicial, en español."
}

func practiceRetryPrompt(language string) string {
	if language == "en" {
		return "I didn't catch that. Could you repeat your answer?"
	}
	return "No te escuché bien. ¿Podés repetir tu respuesta?"
}

// StartPractice switches to practice mode and asks the interviewer persona
// (respond.go's "practice-turn" branch) for an opening question, grounded in the
// session profile already captured by SessionSetupDialog. The kickoff
// instruction itself is never stored; only the interviewer's reply is.
func (a *App) StartPractice() (PracticeTurn, error) {
	a.SetAssistantMode("practice")
	ctx, language, answerID := a.nextPracticeAnswer()
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	reply, speech, err := a.requestPracticeTurn(turnCtx, ctx, answerID, practiceKickoff(language), language, false)
	if err != nil {
		return PracticeTurn{}, err
	}
	spoken := <-speech
	audio, err := spoken.audio, spoken.err
	if err != nil {
		logf("practice TTS failed: %v", err)
		return PracticeTurn{}, err
	}
	return PracticeTurn{ReplyText: reply, Audio: audio}, nil
}

// PracticeListen blocks until the candidate speaks and pauses, transcribes
// the answer, asks the interviewer persona for the next turn, and speaks it
// back. The frontend just calls this in a loop while the practice is live.
func (a *App) PracticeListen() (PracticeTurn, error) {
	pcm, streamed, ok, err := a.recordSpokenAnswer()
	if err != nil {
		return PracticeTurn{}, err
	}
	a.emit("practice:phase", "transcribing")
	answerMS := len(pcm) * 1000 / (meetSampleRate * 2)
	if ok {
		return a.practiceAnswer(nil, &streamed, answerMS)
	}
	return a.practiceAnswer(wavFromPCM(pcm), nil, answerMS)
}

// SetPracticePlayback reports whether the interviewer audio is playing.
func (a *App) SetPracticePlayback(playing bool) {
	a.practicePlaying.Store(playing)
}

// StopPractice interrupts an in-flight PracticeListen recording, for when the
// user leaves the practice view mid-listen.
func (a *App) StopPractice() {
	a.SetPracticePlayback(false)
	a.mu.Lock()
	rec := a.practiceRec
	a.practiceRec = nil
	a.practiceAnswerID++
	a.mu.Unlock()
	if rec != nil {
		rec.cancel()
	}
}

// practiceAnswer transcribes the candidate's recorded answer, asks the
// interviewer persona for the next turn, and speaks it back.
// practiceAnswer replies to one answer: streamed is its transcript when it was
// streamed while spoken, otherwise audio is uploaded and transcribed here.
func (a *App) practiceAnswer(audio []byte, streamed *string, answerMS int) (PracticeTurn, error) {
	if streamed == nil && len(audio) == 0 {
		return PracticeTurn{}, fmt.Errorf("no se grabó audio")
	}
	if a.backend == nil {
		return PracticeTurn{}, fmt.Errorf("Sonara backend is not initialized")
	}
	ctx, language, answerID := a.nextPracticeAnswer()
	started := time.Now()
	var candidateText string
	if streamed != nil {
		candidateText = strings.TrimSpace(*streamed)
	} else {
		transcript, err := a.backend.TranscribePractice(ctx, audio, language)
		if err != nil {
			logf("practice transcription failed: %v", err)
			return PracticeTurn{}, err
		}
		candidateText = strings.TrimSpace(transcript.Text)
	}
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
	reply := practiceRetryPrompt(language)
	var speech <-chan practiceSpeech
	var err error
	if candidateText != "" {
		a.emit("practice:phase", "thinking")
		reply, speech, err = a.requestPracticeTurn(turnCtx, ctx, answerID, candidateText, language, true)
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

func (a *App) requestPracticeTurn(turnCtx, sessionCtx context.Context, answerID uint64, content, language string, storeAnswer bool) (string, <-chan practiceSpeech, error) {
	if a.backend == nil {
		return "", nil, fmt.Errorf("Sonara backend is not initialized")
	}
	a.mu.RLock()
	sessionContext, generation := a.buildSessionContextLocked(), a.sessionGeneration
	a.mu.RUnlock()

	var streamed strings.Builder
	var speech <-chan practiceSpeech
	output, err := a.backend.RespondStream(turnCtx, content, "practice-turn", sessionContext, func(delta string) {
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
		return "", nil, err
	}
	question, suggestion, _ := strings.Cut(output.Text, practiceSuggestionMarker)
	question, suggestion = strings.TrimSpace(question), strings.TrimSpace(suggestion)
	if question == "" {
		return "", nil, fmt.Errorf("el simulador no generó una pregunta")
	}
	a.mu.Lock()
	if a.sessionGeneration != generation || a.practiceAnswerID != answerID {
		a.mu.Unlock()
		return "", nil, fmt.Errorf("la práctica cambió durante la respuesta")
	}
	if storeAnswer {
		a.appendSessionMessageLocked("user", content)
	}
	a.appendSessionMessageLocked("assistant", question)
	a.mu.Unlock()
	if speech == nil {
		speech = a.startPracticeSpeech(turnCtx, sessionCtx, answerID, question, language)
	}
	if suggestion != "" {
		a.publishPracticeText(sessionCtx, answerID, "practice:suggestion", suggestion, language)
	}
	return question, speech, nil
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
func (a *App) recordSpokenAnswer() (pcm []byte, streamed string, ok bool, err error) {
	recordCtx, cancel := context.WithTimeout(a.requestContext(), 45*time.Second)
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

	var audio io.Reader = stdout
	if filter, filtered := startPracticeNoiseFilter(recordCtx, stdout); filter != nil {
		defer func() {
			cancel()
			filter.Wait()
		}()
		audio = filtered
	}
	audio = &practiceLevelReader{reader: audio, emit: func(level float64) { a.emit("practice:level", level) }}
	a.mu.RLock()
	language := a.audioLanguage
	a.mu.RUnlock()
	stream := a.openPracticeStream(language)
	onVoice := func([]byte) { a.emit("practice:phase", "capturing") }
	if stream != nil {
		audio = practiceTee{reader: audio, stream: stream}
		onVoice = func(soFar []byte) {
			stream.voiceStarted(soFar)
			a.emit("practice:phase", "capturing")
		}
	}
	pcm, err = readPracticeAnswer(recordCtx, audio, a.practicePlaying.Load, newSpeechDetector(), onVoice)
	if stream == nil {
		return pcm, "", false, err
	}
	if err != nil {
		stream.session.Close()
		return nil, "", false, err
	}
	streamed, ok = stream.finish()
	return pcm, streamed, ok, nil
}

// Filter before VAD so steady fan or room noise is less likely to start a turn.
// Keep raw capture available on installations without FFmpeg.
func startPracticeNoiseFilter(ctx context.Context, input io.Reader) (*exec.Cmd, io.Reader) {
	filter := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-probesize", "32", "-analyzeduration", "0",
		"-f", "s16le", "-ar", "16000", "-ac", "1", "-blocksize", "3200", "-i", "pipe:0",
		"-af", "afftdn=nr=12:nf=-45:tn=1:ad=0.7",
		"-flush_packets", "1", "-f", "s16le", "pipe:1")
	filter.Stdin = input
	output, err := filter.StdoutPipe()
	if err != nil {
		return nil, nil
	}
	if err := filter.Start(); err != nil {
		output.Close()
		return nil, nil
	}
	return filter, output
}

// speech is the voice detector; nil counts every loud frame as speech.
func readPracticeAnswer(ctx context.Context, audio io.Reader, playing func() bool, speech func([]byte) bool, onVoice func(soFar []byte)) ([]byte, error) {
	frame := make([]byte, meetFrameBytes)
	segment := make([]byte, 0, meetSampleRate*2*12)
	ambient, echo := &noiseFloor{}, &noiseFloor{}
	var interruption []byte
	var wasPlaying bool
	var echoTail int
	var recording bool
	var startFrames, voicedFrames, silenceFrames int
	for {
		if _, err := io.ReadFull(audio, frame); err != nil {
			switch ctx.Err() {
			case context.DeadlineExceeded:
				return nil, fmt.Errorf("se agotó el tiempo de escucha; revisá el micrófono y probá de nuevo")
			case context.Canceled:
				return nil, ctx.Err()
			}
			if recording && voicedFrames >= practiceVoiceFrames {
				return segment, nil
			}
			return nil, fmt.Errorf("se cortó la escucha antes de captar una respuesta")
		}
		level := pcmRMS(frame)
		if !recording && playing() {
			wasPlaying = true
			echo.observe(level)
			if !isVoice(speech, frame, level > math.Max(practiceInterruptFloor, echo.threshold())) {
				interruption = interruption[:0]
				continue
			}
			interruption = append(interruption, frame...)
			if len(interruption) == practiceInterruptFrames*meetFrameBytes {
				segment = append(segment[:0], interruption...)
				recording, voicedFrames = true, practiceInterruptFrames
				onVoice(segment)
			}
			continue
		}
		if wasPlaying {
			wasPlaying = false
			echoTail = practiceEchoTailFrames
			if !recording && len(interruption) > 0 {
				segment = append(segment[:0], interruption...)
				startFrames = len(interruption) / meetFrameBytes
				if startFrames >= practiceStartFrames {
					recording, voicedFrames = true, startFrames
					if voicedFrames >= practiceVoiceFrames {
						onVoice(segment)
					}
				}
			}
		}
		// The interviewer's voice keeps ringing in the room (and in the output
		// buffer) after playback "ended": that tail is not the candidate.
		if echoTail > 0 && !recording {
			echoTail--
			continue
		}
		loud := level >= ambient.threshold()
		if !loud {
			ambient.observe(level)
		}
		voice := isVoice(speech, frame, loud)
		if !recording {
			if voice {
				startFrames++
				segment = append(segment, frame...)
				if startFrames >= practiceStartFrames {
					recording, voicedFrames, silenceFrames = true, startFrames, 0
				}
			} else {
				startFrames = 0
				segment = append(segment, frame...)
				if keep := practiceLeadFrames * meetFrameBytes; len(segment) > keep {
					segment = append(segment[:0], segment[len(segment)-keep:]...)
				}
			}
			continue
		}
		segment = append(segment, frame...)
		if voice {
			voicedFrames++
			if voicedFrames == practiceVoiceFrames {
				onVoice(segment)
			}
			silenceFrames = 0
		} else {
			silenceFrames++
		}
		ended := silenceFrames >= practiceSilenceFrames
		if !ended && len(segment)/meetFrameBytes < meetMaximumFrames {
			continue
		}
		if voicedFrames >= practiceVoiceFrames {
			return trimTrailingSilence(segment, silenceFrames), nil
		}
		// Discard short bursts so the next answer starts a fresh segment.
		recording = false
		startFrames, voicedFrames, silenceFrames = 0, 0, 0
		keep := practiceLeadFrames * meetFrameBytes
		segment = append(segment[:0], segment[len(segment)-keep:]...)
	}
}
