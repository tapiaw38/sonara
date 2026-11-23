package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	sonaraBackend "sonara/backend/client"
	"sonara/internal/storage"
)

// Live copilot transcription (phase 2) and anticipated replies (phase 3).
//
// In batch mode every turn is recorded, uploaded as a WAV and transcribed
// before anything else starts. Here the audio is streamed to OpenAI's Realtime
// transcription while the other person is still talking: partial text shows up
// immediately, and the reply is started from that partial text a few hundred
// milliseconds into the pause, before the final transcript even arrives.
//
// Anticipation is speculative and always reversible:
//   - 300 ms of silence starts a reply from the text so far (meetSpecFrames);
//   - voice returning before the turn really ends (500 ms) retracts it;
//   - when the final transcript arrives it is compared with what the reply was
//     started from: equal -> the reply stands, different -> it is retracted and
//     restarted from the final text.
// A retracted reply costs tokens, never a wrong answer left on screen.
//
// Live mode is opt-in (Settings) and OpenAI-only; there is no fallback to the
// batch path when it fails — the error is shown, like every other provider.
//
// *** UNVERIFIED against the real service; see the realtime package. ***

const (
	// meetSpecFrames is the silence (300 ms) after which a reply is started
	// speculatively, ahead of the 500 ms that closes the turn.
	meetSpecFrames = 3
	// liveMaxFrames closes a turn that never pauses (60 s): same reasoning as
	// meetMaxParts in batch mode — nobody talks for a minute without a breath.
	liveMaxFrames = 600
)

const (
	liveModeBatch  = "batch"
	liveModeStream = "stream"
)

// liveConn is the part of a streaming session the runtime uses; a fake in
// tests, *sonaraBackend.LiveSession in production.
type liveConn interface {
	Append(pcm16k []byte) error
	Commit() error
	Events() <-chan sonaraBackend.LiveEvent
	Close() error
}

type liveTurn struct {
	seg      meetSegment
	voiceEnd time.Time
	itemID   string
	// ignore marks a burst too short to be speech: it still has to be
	// committed (the audio is already in the server's buffer), but whatever
	// comes back is dropped.
	ignore  bool
	specOn  bool
	specTxt string
	partial bool
}

type liveRuntime struct {
	a        *App
	conn     liveConn
	language string

	mu       sync.Mutex
	open     *liveTurn            // streaming now, not yet committed
	waiting  []*liveTurn          // committed, awaiting their Committed event (FIFO)
	byItem   map[string]*liveTurn // bound by Committed, awaiting Completed
	text     map[string]string    // accumulated delta text per item
	openItem string               // item id of the turn currently streaming
	dead     bool
}

func newLiveRuntime(a *App, conn liveConn, language string) *liveRuntime {
	return &liveRuntime{a: a, conn: conn, language: language,
		byItem: map[string]*liveTurn{}, text: map[string]string{}}
}

func (r *liveRuntime) isDead() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.dead }

func (r *liveRuntime) close() {
	r.mu.Lock()
	r.dead = true
	r.mu.Unlock()
	r.conn.Close()
}

// run consumes server events until the connection ends.
func (r *liveRuntime) run() {
	for ev := range r.conn.Events() {
		r.handle(ev)
	}
	r.mu.Lock()
	r.dead = true
	r.mu.Unlock()
}

// begin starts a turn; preroll is the audio already captured when voice was
// confirmed, so the first syllables are not lost.
func (r *liveRuntime) begin(seg meetSegment, preroll []byte) error {
	r.mu.Lock()
	r.open = &liveTurn{seg: seg}
	r.openItem = ""
	r.mu.Unlock()
	return r.conn.Append(preroll)
}

func (r *liveRuntime) frame(pcm []byte) error { return r.conn.Append(pcm) }

// speculate starts the reply from the text transcribed so far.
func (r *liveRuntime) speculate() {
	r.mu.Lock()
	t := r.open
	if t == nil || t.specOn || t.ignore {
		r.mu.Unlock()
		return
	}
	text := strings.TrimSpace(r.text[r.openItem])
	if text == "" {
		r.mu.Unlock()
		return
	}
	t.specOn, t.specTxt = true, text
	seg := t.seg
	r.mu.Unlock()
	r.a.beginReply(seg.session, seg.language, text, time.Now())
}

// cancelSpeculation retracts a speculative reply when voice resumes.
func (r *liveRuntime) cancelSpeculation() {
	r.mu.Lock()
	t := r.open
	if t == nil || !t.specOn {
		r.mu.Unlock()
		return
	}
	t.specOn, t.specTxt = false, ""
	session := t.seg.session
	r.mu.Unlock()
	r.a.cancelReply(session)
}

// end commits the turn. voiceEnd is when the voice actually stopped (not when
// the silence was long enough to be sure), the clock for the latency metric.
func (r *liveRuntime) end(voiceEnd time.Time, ignore bool) error {
	r.mu.Lock()
	t := r.open
	r.open = nil
	if t == nil {
		r.mu.Unlock()
		return nil
	}
	t.voiceEnd, t.ignore = voiceEnd, ignore
	r.waiting = append(r.waiting, t)
	r.mu.Unlock()
	if !ignore {
		r.a.setSessionStatus(t.seg.session, "Sonara transcribiendo…")
	}
	if err := r.conn.Commit(); err != nil {
		r.mu.Lock()
		for i, w := range r.waiting {
			if w == t {
				r.waiting = append(r.waiting[:i], r.waiting[i+1:]...)
				break
			}
		}
		r.mu.Unlock()
		return err
	}
	return nil
}

func (r *liveRuntime) handle(ev sonaraBackend.LiveEvent) {
	switch ev.Kind {
	case sonaraBackend.LiveDelta:
		r.mu.Lock()
		r.text[ev.ItemID] += ev.Text
		text := r.text[ev.ItemID]
		t := r.byItem[ev.ItemID]
		if t == nil {
			r.openItem = ev.ItemID
			t = r.open
		}
		first := false
		if t != nil && !t.ignore && !t.partial {
			t.partial, first = true, true
		}
		var session uint64
		show := t != nil && !t.ignore
		if show {
			session = t.seg.session
		}
		r.mu.Unlock()
		if show {
			r.a.setLivePartial(session, text, first)
		}

	case sonaraBackend.LiveCommitted:
		r.mu.Lock()
		if len(r.waiting) == 0 {
			r.mu.Unlock()
			return
		}
		t := r.waiting[0]
		r.waiting = r.waiting[1:]
		t.itemID = ev.ItemID
		r.byItem[ev.ItemID] = t
		if r.openItem == ev.ItemID {
			r.openItem = ""
		}
		text := strings.TrimSpace(r.text[ev.ItemID])
		start := !t.ignore && !t.specOn && text != ""
		if start {
			t.specOn, t.specTxt = true, text
		}
		seg := t.seg
		r.mu.Unlock()
		if start {
			r.a.beginReply(seg.session, seg.language, text, time.Now())
		}

	case sonaraBackend.LiveCompleted:
		r.mu.Lock()
		t := r.byItem[ev.ItemID]
		delete(r.byItem, ev.ItemID)
		delete(r.text, ev.ItemID)
		r.mu.Unlock()
		if t == nil || t.ignore {
			return
		}
		r.a.finishLiveTurn(t, strings.TrimSpace(ev.Text))

	case sonaraBackend.LiveFailed:
		r.mu.Lock()
		var t *liveTurn
		if ev.ItemID != "" {
			t = r.byItem[ev.ItemID]
			delete(r.byItem, ev.ItemID)
			delete(r.text, ev.ItemID)
		} else if ev.Err != nil && strings.Contains(ev.Err.Error(), "commit") && len(r.waiting) > 0 {
			// A commit the server rejected (e.g. too little audio) produces no
			// Committed event, so its turn has to leave the FIFO here or every
			// later turn would be matched to the wrong item.
			t = r.waiting[0]
			r.waiting = r.waiting[1:]
		}
		r.mu.Unlock()
		logf("live transcription error: %v", ev.Err)
		if t != nil && !t.ignore {
			if t.specOn {
				r.a.cancelReply(t.seg.session)
			}
			r.a.setSessionStatus(t.seg.session, fmt.Sprintf("Error de transcripción en vivo: %v", ev.Err))
		}

	case sonaraBackend.LiveClosed:
		r.mu.Lock()
		r.dead = true
		r.mu.Unlock()
		if ev.Err != nil {
			logf("live transcription connection lost: %v", ev.Err)
			r.a.setListenStatus("Conexión en vivo perdida; se reabre con la próxima intervención")
		}
	}
}

// liveEquivalent reports whether two transcripts say the same thing: case,
// punctuation and spacing differences (the final pass restores them) do not
// make the reply that was started from the earlier text wrong.
func liveEquivalent(a, b string) bool {
	norm := func(s string) string {
		var out strings.Builder
		for _, r := range strings.ToLower(s) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out.WriteRune(r)
			}
		}
		return out.String()
	}
	return norm(a) == norm(b)
}

// finishLiveTurn applies the final transcript of a turn.
func (a *App) finishLiveTurn(t *liveTurn, final string) {
	seg := t.seg
	a.mu.RLock()
	current := a.sessionGeneration == seg.session
	a.mu.RUnlock()
	if !current {
		return
	}
	if final == "" {
		if t.specOn {
			a.cancelReply(seg.session)
		}
		a.setSessionStatus(seg.session, "No se entendió la intervención")
		return
	}
	if t.specOn && liveEquivalent(t.specTxt, final) {
		logf("live turn %d/%d: reply started ahead of the final transcript stands (%d chars)", seg.session, seg.turn, len(final))
		a.setLiveFinalText(seg.session, final)
		return
	}
	if t.specOn {
		logf("live turn %d/%d: final transcript differs from the text the reply started from; restarting it", seg.session, seg.turn)
		a.cancelReply(seg.session)
	}
	a.beginReply(seg.session, seg.language, final, t.voiceEnd)
}

// beginReply starts a reply; a test hook replaces it.
func (a *App) beginReply(session uint64, language, transcript string, queuedAt time.Time) {
	a.mu.RLock()
	hook := a.replyHook
	a.mu.RUnlock()
	if hook != nil {
		hook(session, language, transcript, queuedAt)
		return
	}
	a.startVoiceResponse(session, language, transcript, queuedAt)
}

// cancelReply retracts the reply in flight for this session and tells the UI
// (Retractions) to drop the message it already started showing.
func (a *App) cancelReply(session uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionGeneration != session {
		return
	}
	if a.replyCancel != nil {
		a.replyCancel()
		a.replyCancel = nil
	}
	a.replySequence++
	a.streaming = false
	a.translation, a.voiceReply = "", ""
	a.voiceRetractions++
}

// setLivePartial shows the text transcribed so far. The first partial of a
// turn also clears the previous turn's translation, so the panel never pairs
// a new phrase with an old translation.
func (a *App) setLivePartial(session uint64, text string, first bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionGeneration != session || !a.listening {
		return
	}
	a.transcript, a.livePartial = text, text
	if first {
		a.translation = ""
	}
}

func (a *App) setLiveFinalText(session uint64, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sessionGeneration == session {
		a.transcript, a.livePartial = text, ""
	}
}

func (a *App) liveStreamingEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.liveStreaming
}

// ensureLive returns the open streaming session for the current language,
// connecting (or reconnecting) when there is none. The connection is closed
// when listening stops (ctx).
func (a *App) ensureLive(ctx context.Context) (*liveRuntime, error) {
	a.mu.RLock()
	rt, backend, open := a.live, a.backend, a.openLive
	language := a.audioLanguage
	a.mu.RUnlock()
	if language == "" {
		language = "es"
	}
	if rt != nil && !rt.isDead() && rt.language == language {
		return rt, nil
	}
	if rt != nil {
		rt.close()
	}
	if open == nil {
		if backend == nil {
			return nil, fmt.Errorf("backend no inicializado")
		}
		open = func(ctx context.Context, language string) (liveConn, error) {
			return backend.OpenLive(ctx, language)
		}
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := open(dialCtx, language)
	if err != nil {
		return nil, err
	}
	rt = newLiveRuntime(a, conn, language)
	go rt.run()
	go func() { <-ctx.Done(); rt.close() }()
	a.mu.Lock()
	a.live = rt
	a.mu.Unlock()
	logf("live transcription connected (%s)", language)
	return rt, nil
}

// SetLiveTranscribeMode chooses how the copilot transcribes: "batch" (record,
// upload, transcribe: works with every provider) or "stream" (OpenAI Realtime:
// text while they speak, reply started ahead of the final transcript).
func (a *App) SetLiveTranscribeMode(mode string) error {
	if mode != liveModeBatch && mode != liveModeStream {
		return fmt.Errorf("modo desconocido: %q", mode)
	}
	a.mu.Lock()
	a.liveStreaming = mode == liveModeStream
	a.mu.Unlock()
	if err := a.updateCapabilityProviders(func(p *storage.CapabilityProviders) { p.LiveMode = mode }); err != nil {
		logf("could not persist the live transcription mode: %v", err)
		return fmt.Errorf("el modo quedó activo pero no se pudo guardar: %w", err)
	}
	return nil
}
