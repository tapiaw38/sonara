package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	sonaraBackend "sonara/backend/client"
	"sonara/internal/diarize"
	"sonara/internal/meetbridge"
	"sonara/internal/pipewire"
	"sonara/internal/storage"
)

// App struct
type App struct {
	ctx               context.Context
	backend           *sonaraBackend.Client
	mu                sync.RWMutex
	meetSpeaking      meetbridge.Tracker     // who Google Meet shows speaking
	echoCanceller     pipewire.EchoCanceller // while practicing, so you can talk over the interviewer
	segmentOnce       sync.Once
	segments          chan meetSegment
	voiceContext      context.Context
	voiceCancel       context.CancelFunc
	replyCancel       context.CancelFunc
	sessionGeneration uint64
	// sessionID is the durable identity of the current session. sessionGeneration
	// guards in-flight work but restarts at 1 every run, so it cannot key rows.
	sessionID           string
	turnSequence        uint64
	replySequence       uint64
	droppedSegments     uint64
	selectedAudioSource string
	preferredAudioApp   string
	// audioGuessing and audioCandidates let the UI warn that the current
	// source is a heuristic pick, not a confident match: see audioTarget.
	audioGuessing       bool
	audioCandidates     int
	store               *storage.Store
	listening           bool
	sessionHistory      []storage.Message
	sessionProfile      storage.SessionProfile
	assistantMode       string
	latestVisualContext string
	listenerCancel      context.CancelFunc
	transcript          string
	listenStatus        string
	voiceReply          string
	voiceSequence       uint64
	streaming           bool
	manualSend          bool
	pendingAudio        []byte
	audioLevel          float64
	audioLanguage       string
	translation         string
	overlayMu           sync.Mutex // serialize compositor commands across toggles
	floatedForOverlay   bool
	// Meeting transcription state, independent of the copilot's listening.
	meeting       *meetingRun
	preferredMic  string
	meetingPeople int
	// Conversation memory (copilot_memory.go).
	memorySummary     string
	memoryPending     []storage.Message
	memorySummarizing bool
	// livePartial is the streamed text shown before its turn is final.
	livePartial string
	practiceRec *practiceRecording
	// lastOutputVoice: when the meeting audio last had voice (unix ms), so the
	// microphone can drop the other person leaking through speakers.
	lastOutputVoice   atomic.Int64
	practiceAnswerID  uint64
	emitHook          func(event string, data any)
	meetingTranscribe meetingTranscriber
	saveDialog        func(ctx context.Context, filename string) (string, error)
	// diarizer is phase 2 (speaker diarization): one per App, started lazily.
	diarizer *diarize.Service
	// diarizeNote is how the last separation of participants ended, and for
	// which meeting, so the UI can say why everyone is still "Participante"
	// instead of leaving it to look like a bug. Guarded by mu.
	diarizeNote, diarizeNoteSession string
	// Live copilot transcription (live_transcribe.go).
	live          *liveRuntime
	liveStreaming bool
	openLive      func(ctx context.Context, language string) (liveConn, error)
	replyHook     func(session uint64, language, transcript string, queuedAt time.Time)
	// voiceRetractions counts replies taken back (speculation that did not
	// hold); the UI drops the message it started for each one.
	voiceRetractions uint64
	// suggestNamesHook is the test seam for name_suggestions.go's backend call.
	suggestNamesHook func(ctx context.Context, transcript string) (map[string]string, error)
	// microphonesHook is the test seam for Microphones(), so SetMicrophone can
	// be tested end to end without a real pw-dump call.
	microphonesHook func(ctx context.Context) ([]pipewire.Source, error)
}

// NewApp creates a new App application struct
func NewApp() *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		assistantMode: "interview", sessionGeneration: 1,
		sessionID:    newSessionID(),
		segments:     make(chan meetSegment, meetQueuedSegments),
		voiceContext: ctx, voiceCancel: cancel,
		diarizer: diarize.NewService(diarize.NewGemini(nil, diarize.GeminiBaseURL)),
	}
}

// newSessionID is sortable and unique per session: the timestamp orders the
// list, and the random suffix keeps two sessions created in the same clock
// tick apart.
func newSessionID() string {
	timestamp := time.Now().UTC().Format("20060102T150405.000000000")
	var suffix [3]byte
	rand.Read(suffix[:])
	return timestamp + "-" + hex.EncodeToString(suffix[:])
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.startMeetBridge()
	database, err := storage.Open()
	if err != nil {
		// A broken store must not take the app down: without it the key has to
		// be retyped and sessions are not kept, but the interview still works.
		logf("store unavailable, running without persistence: %v", err)
	}
	a.mu.Lock()
	a.ctx = ctx
	a.backend = sonaraBackend.NewWithUsage(recordAPIUsage(database))
	a.store = database
	a.voiceCancel()
	a.voiceContext, a.voiceCancel = context.WithCancel(ctx)
	sessionID, mode, language := a.sessionID, a.assistantMode, a.audioLanguage
	a.mu.Unlock()

	if database != nil {
		// Restoring the key here is what makes it survive a restart; it reaches
		// the gateway before the UI can send its first request.
		if key := database.Setting(storage.SettingAPIKey); key != "" {
			if err := a.backend.SetAPIKey(key); err != nil {
				logf("stored API key rejected: %v", err)
			}
		}
		// Restoring the preferred application is what stops every restart from
		// asking for the audio source again.
		if app := database.Setting(storage.SettingAudioApp); app != "" {
			a.mu.Lock()
			a.preferredAudioApp = app
			a.mu.Unlock()
			logf("preferred audio application restored: %q", app)
		}
		if key := database.Setting(storage.SettingDeepSeekKey); key != "" {
			if err := a.backend.SetDeepSeekKey(key); err != nil {
				logf("stored DeepSeek key rejected: %v", err)
			}
		}
		if mic := database.Setting(storage.SettingMicrophone); mic != "" {
			a.mu.Lock()
			a.preferredMic = mic
			a.mu.Unlock()
		}
		if people, err := strconv.Atoi(database.Setting(storage.SettingMeetingPeople)); err == nil {
			a.mu.Lock()
			a.meetingPeople = people
			a.mu.Unlock()
		}
		if key := database.Setting(storage.SettingGeminiKey); key != "" {
			a.diarizer.SetAPIKey(key)
		}
		if azure, err := database.AzureOpenAISettings(); err != nil {
			logf("stored Azure OpenAI settings unreadable: %v", err)
		} else if azure.APIKey != "" {
			if err := a.backend.SetAzureOpenAIConfig(sonaraBackend.AzureConfig{
				APIKey: azure.APIKey, Endpoint: azure.Endpoint,
				ChatAPIVersion: azure.ChatAPIVersion, TranscribeAPIVersion: azure.TranscribeAPIVersion,
				ChatDeployment: azure.ChatDeployment, TranscribeDeployment: azure.TranscribeDeployment,
				LiveDeployment: azure.LiveDeployment,
			}); err != nil {
				logf("stored Azure OpenAI settings rejected: %v", err)
			}
		}
		if chosen, err := database.CapabilityProviders(); err != nil {
			logf("stored provider selection unreadable: %v", err)
		} else {
			chosen = defaultedProviders(chosen)
			if err := a.backend.SetTextProvider(chosen.Text); err != nil {
				logf("stored text provider rejected: %v", err)
			}
			if err := a.backend.SetVisionProvider(chosen.Vision); err != nil {
				logf("stored vision provider rejected: %v", err)
			}
			if err := a.backend.SetLiveTranscribeProvider(chosen.LiveTranscribe); err != nil {
				logf("stored live transcription provider rejected: %v", err)
			}
			if err := a.backend.SetMeetingProvider(chosen.MeetingProvider); err != nil {
				logf("stored meeting provider rejected: %v", err)
			}
			a.mu.Lock()
			a.liveStreaming = chosen.LiveMode == liveModeStream
			a.mu.Unlock()
		}
		if language == "" {
			language = "es"
		}
		if err := database.SaveSession(sessionID, mode, language); err != nil {
			logf("could not record session %s: %v", sessionID, err)
		}
		a.inheritLastProfile(database, sessionID)
	}
	a.mu.RLock()
	status := a.backend.Status()
	a.mu.RUnlock()
	// Opens the log and stamps a header, so every run is separated and the
	// model tiers in use are on record next to that run's errors.
	logf("sonara started: %s", status)
}

// shutdown closes the store so SQLite can finish its journal cleanly.
func (a *App) shutdown(context.Context) {
	// Before the store closes: pending meeting blocks still have to be written.
	a.stopMeetingAndWait(15 * time.Second)
	a.echoCanceller.Stop()
	if a.backend != nil {
		a.backend.Close()
	}
	a.mu.Lock()
	database := a.store
	a.store = nil
	a.mu.Unlock()
	if database != nil {
		if err := database.Close(); err != nil {
			logf("closing the store failed: %v", err)
		}
	}
}

func (a *App) requestContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
