package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	sonaraBackend "sonara/backend/client"
)

type fakeLive struct {
	mu        sync.Mutex
	appends   int
	commits   int
	closed    bool
	events    chan sonaraBackend.LiveEvent
	commitErr error
}

func newFakeLive() *fakeLive { return &fakeLive{events: make(chan sonaraBackend.LiveEvent, 32)} }

func (f *fakeLive) Append([]byte) error                    { f.mu.Lock(); f.appends++; f.mu.Unlock(); return nil }
func (f *fakeLive) Commit() error                          { f.mu.Lock(); f.commits++; f.mu.Unlock(); return f.commitErr }
func (f *fakeLive) Events() <-chan sonaraBackend.LiveEvent { return f.events }
func (f *fakeLive) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.events)
	}
	return nil
}

type replyCall struct {
	session  uint64
	language string
	text     string
}

// liveHarness builds an App listening on session 1 with replies recorded
// instead of sent, and a runtime fed by a fake connection (events handled
// synchronously through rt.handle so tests need no sleeping).
func liveHarness(t *testing.T) (*App, *liveRuntime, *fakeLive, *[]replyCall) {
	t.Helper()
	a := NewApp()
	a.listening = true
	var mu sync.Mutex
	var calls []replyCall
	a.replyHook = func(session uint64, language, transcript string, _ time.Time) {
		mu.Lock()
		calls = append(calls, replyCall{session, language, transcript})
		mu.Unlock()
	}
	conn := newFakeLive()
	rt := newLiveRuntime(a, conn, "en")
	return a, rt, conn, &calls
}

func ev(kind sonaraBackend.LiveKind, item, text string) sonaraBackend.LiveEvent {
	return sonaraBackend.LiveEvent{Kind: kind, ItemID: item, Text: text}
}

func seg(a *App) meetSegment {
	return meetSegment{session: a.sessionGeneration, turn: 1, language: "en"}
}

func TestLivePartialTextShowsAndClearsOldTranslation(t *testing.T) {
	a, rt, _, _ := liveHarness(t)
	a.translation = "traducción vieja"
	rt.begin(seg(a), []byte{1, 2})
	rt.handle(ev(sonaraBackend.LiveDelta, "i1", "How old"))
	if a.transcript != "How old" || a.translation != "" {
		t.Fatalf("transcript=%q translation=%q: first partial must show and clear the old translation", a.transcript, a.translation)
	}
	a.translation = "algo nuevo"
	rt.handle(ev(sonaraBackend.LiveDelta, "i1", " are you"))
	if a.transcript != "How old are you" || a.translation != "algo nuevo" {
		t.Fatalf("later partials must grow the text without wiping the translation: %q / %q", a.transcript, a.translation)
	}
}

func TestSpeculationStartsAtShortSilenceAndIsRetractedWhenVoiceReturns(t *testing.T) {
	a, rt, _, calls := liveHarness(t)
	rt.begin(seg(a), nil)
	rt.speculate() // nothing transcribed yet: must not start anything
	if len(*calls) != 0 {
		t.Fatal("no text yet, no reply")
	}
	rt.handle(ev(sonaraBackend.LiveDelta, "i1", "Tell me about"))
	rt.speculate()
	if len(*calls) != 1 || (*calls)[0].text != "Tell me about" {
		t.Fatalf("calls = %+v", *calls)
	}
	rt.speculate() // idempotent while it stands
	if len(*calls) != 1 {
		t.Fatalf("speculating twice must not start two replies: %+v", *calls)
	}
	before := a.voiceRetractions
	rt.cancelSpeculation()
	if a.voiceRetractions != before+1 {
		t.Error("retracting must be visible to the UI through Retractions")
	}
	rt.cancelSpeculation() // nothing left to retract
	if a.voiceRetractions != before+1 {
		t.Error("retracting twice counted twice")
	}
}

func TestFinalTranscriptEqualKeepsTheAnticipatedReply(t *testing.T) {
	a, rt, _, calls := liveHarness(t)
	rt.begin(seg(a), nil)
	rt.handle(ev(sonaraBackend.LiveDelta, "i1", "how are you"))
	rt.speculate()
	rt.end(time.Now(), false)
	rt.handle(ev(sonaraBackend.LiveCommitted, "i1", ""))
	rt.handle(ev(sonaraBackend.LiveCompleted, "i1", "How are you?"))
	if len(*calls) != 1 {
		t.Fatalf("punctuation/case differences must not restart the reply: %+v", *calls)
	}
	if a.transcript != "How are you?" {
		t.Errorf("the panel should settle on the final text, got %q", a.transcript)
	}
	if a.voiceRetractions != 0 {
		t.Error("nothing should have been retracted")
	}
}

func TestFinalTranscriptDifferentRetractsAndRestarts(t *testing.T) {
	a, rt, _, calls := liveHarness(t)
	rt.begin(seg(a), nil)
	rt.handle(ev(sonaraBackend.LiveDelta, "i1", "I want to hire"))
	rt.speculate()
	rt.end(time.Now(), false)
	rt.handle(ev(sonaraBackend.LiveCommitted, "i1", ""))
	rt.handle(ev(sonaraBackend.LiveCompleted, "i1", "I want to higher the price"))
	if len(*calls) != 2 || (*calls)[1].text != "I want to higher the price" {
		t.Fatalf("calls = %+v, want a restart from the final text", *calls)
	}
	if a.voiceRetractions != 1 {
		t.Errorf("retractions = %d, the first reply must be taken back", a.voiceRetractions)
	}
}

func TestReplyStartsAtCommitWhenTextAlreadyArrivedAndAtCompletedWhenNot(t *testing.T) {
	a, rt, _, calls := liveHarness(t)
	// Turn 1: deltas arrive before the commit confirmation.
	rt.begin(seg(a), nil)
	rt.handle(ev(sonaraBackend.LiveDelta, "i1", "first turn"))
	rt.end(time.Now(), false)
	rt.handle(ev(sonaraBackend.LiveCommitted, "i1", ""))
	if len(*calls) != 1 || (*calls)[0].text != "first turn" {
		t.Fatalf("a reply should start as soon as the commit is confirmed: %+v", *calls)
	}
	rt.handle(ev(sonaraBackend.LiveCompleted, "i1", "First turn."))

	// Turn 2: no deltas at all, only the final transcript.
	rt.begin(seg(a), nil)
	rt.end(time.Now(), false)
	rt.handle(ev(sonaraBackend.LiveCommitted, "i2", ""))
	if len(*calls) != 1 {
		t.Fatal("no text yet, so no reply at commit")
	}
	rt.handle(ev(sonaraBackend.LiveCompleted, "i2", "Second turn"))
	if len(*calls) != 2 || (*calls)[1].text != "Second turn" {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestCommittedEventsAlignWithTurnsEvenAfterAnIgnoredBurst(t *testing.T) {
	a, rt, _, calls := liveHarness(t)
	// A burst too short to be speech is still committed (its audio is already
	// in the server buffer), and its item must not steal the next real turn.
	rt.begin(seg(a), nil)
	rt.end(time.Now(), true)
	rt.begin(seg(a), nil)
	rt.end(time.Now(), false)
	rt.handle(ev(sonaraBackend.LiveCommitted, "noise", ""))
	rt.handle(ev(sonaraBackend.LiveCommitted, "real", ""))
	rt.handle(ev(sonaraBackend.LiveCompleted, "noise", "uh"))
	rt.handle(ev(sonaraBackend.LiveCompleted, "real", "Real question"))
	if len(*calls) != 1 || (*calls)[0].text != "Real question" {
		t.Fatalf("calls = %+v, the noise burst must be dropped and the real turn answered", *calls)
	}
}

func TestRejectedCommitLeavesTheFIFOSoLaterTurnsStayAligned(t *testing.T) {
	a, rt, _, calls := liveHarness(t)
	rt.begin(seg(a), nil)
	rt.end(time.Now(), false) // the server will reject this commit
	rt.begin(seg(a), nil)
	rt.end(time.Now(), false)
	rt.handle(sonaraBackend.LiveEvent{Kind: sonaraBackend.LiveFailed, Err: errors.New("input_audio_buffer_commit_empty: too small")})
	rt.handle(ev(sonaraBackend.LiveCommitted, "i2", ""))
	rt.handle(ev(sonaraBackend.LiveCompleted, "i2", "Hello there"))
	if len(*calls) != 1 || (*calls)[0].text != "Hello there" {
		t.Fatalf("calls = %+v, the surviving turn must get its own transcript", *calls)
	}
}

func TestEmptyFinalTranscriptRetractsAndSaysSo(t *testing.T) {
	a, rt, _, calls := liveHarness(t)
	rt.begin(seg(a), nil)
	rt.handle(ev(sonaraBackend.LiveDelta, "i1", "mm"))
	rt.speculate()
	rt.end(time.Now(), false)
	rt.handle(ev(sonaraBackend.LiveCommitted, "i1", ""))
	rt.handle(ev(sonaraBackend.LiveCompleted, "i1", "  "))
	if a.voiceRetractions != 1 || !strings.Contains(a.listenStatus, "No se entendió") {
		t.Errorf("retractions=%d status=%q", a.voiceRetractions, a.listenStatus)
	}
	_ = calls
}

func TestCommitFailureIsReturnedAndTheTurnLeavesTheQueue(t *testing.T) {
	a, rt, conn, _ := liveHarness(t)
	conn.commitErr = errors.New("socket closed")
	rt.begin(seg(a), nil)
	if err := rt.end(time.Now(), false); err == nil {
		t.Fatal("a failed commit must surface")
	}
	if len(rt.waiting) != 0 {
		t.Error("a turn that never reached the server must not wait for an item")
	}
}

func TestLiveEquivalent(t *testing.T) {
	if !liveEquivalent("how are you", "How are you?") {
		t.Error("case and punctuation must not matter")
	}
	if liveEquivalent("hire", "higher") {
		t.Error("different words must differ")
	}
}

func TestEnsureLiveReusesTheSessionAndReopensOnLanguageChange(t *testing.T) {
	a := NewApp()
	var opened []string
	var conns []*fakeLive
	a.openLive = func(_ context.Context, language string) (liveConn, error) {
		opened = append(opened, language)
		c := newFakeLive()
		conns = append(conns, c)
		return c, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.audioLanguage = "en"
	first, err := a.ensureLive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := a.ensureLive(ctx); again != first || len(opened) != 1 {
		t.Fatalf("a healthy session must be reused, opened=%v", opened)
	}
	a.audioLanguage = "es"
	second, _ := a.ensureLive(ctx)
	if second == first || len(opened) != 2 || opened[1] != "es" || !conns[0].closed {
		t.Fatalf("a language change must close the old session and open a new one: opened=%v closed=%v", opened, conns[0].closed)
	}
	cancel()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !second.isDead() {
		time.Sleep(5 * time.Millisecond)
	}
	if !second.isDead() {
		t.Error("stopping listening must close the streaming connection")
	}
}

func TestEnsureLiveSurfacesAnOpenError(t *testing.T) {
	a := NewApp()
	a.openLive = func(context.Context, string) (liveConn, error) { return nil, errors.New("sin clave") }
	if _, err := a.ensureLive(context.Background()); err == nil || !strings.Contains(err.Error(), "sin clave") {
		t.Fatalf("err = %v: a failed connection is shown, never replaced by batch transcription", err)
	}
}

func TestSetLiveTranscribeModePersists(t *testing.T) {
	a, _ := testMeetingApp(t)
	if err := a.SetLiveTranscribeMode("stream"); err != nil || !a.liveStreamingEnabled() {
		t.Fatalf("err=%v enabled=%v", err, a.liveStreamingEnabled())
	}
	got, _ := a.GetCapabilityProviders()
	if got.LiveMode != "stream" {
		t.Errorf("stored mode = %q", got.LiveMode)
	}
	if err := a.SetLiveTranscribeMode("bogus"); err == nil {
		t.Error("an unknown mode must be rejected")
	}
	a.SetLiveTranscribeMode("batch")
	if a.liveStreamingEnabled() {
		t.Error("batch must turn streaming off")
	}
}
