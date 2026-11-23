package audio

import (
	"math"
	"testing"
)

func TestNoiseFloorRisesAboveAmbientHiss(t *testing.T) {
	quiet := &NoiseFloor{}
	if got := quiet.Threshold(); got != MinNoiseFloor {
		t.Errorf("threshold with no samples = %v, want the absolute floor %v", got, MinNoiseFloor)
	}

	// A silent stream must not end up treating every frame as voice.
	for range NoiseWindow {
		quiet.Observe(0)
	}
	if got := quiet.Threshold(); got != MinNoiseFloor {
		t.Errorf("threshold on a silent stream = %v, want %v", got, MinNoiseFloor)
	}

	// A hissy call lifts the bar, so the hiss itself stops counting as voice.
	noisy := &NoiseFloor{}
	for range NoiseWindow {
		noisy.Observe(0.02)
	}
	want := 0.02 * NoiseMargin
	if got := noisy.Threshold(); math.Abs(got-want) > 1e-9 {
		t.Errorf("threshold on a noisy stream = %v, want %v", got, want)
	}
	if noisy.Threshold() <= 0.02 {
		t.Error("threshold must sit above the ambient level it measured")
	}
}

func TestNoiseFloorKeepsOnlyRecentFrames(t *testing.T) {
	floor := &NoiseFloor{}
	for range NoiseWindow * 3 {
		floor.Observe(0.05)
	}
	if len(floor.quiet) != NoiseWindow {
		t.Errorf("window = %d frames, want %d", len(floor.quiet), NoiseWindow)
	}
	// The room went quiet: the old loud samples must age out of the estimate.
	for range NoiseWindow {
		floor.Observe(0)
	}
	if got := floor.Threshold(); got != MinNoiseFloor {
		t.Errorf("threshold after the noise stopped = %v, want %v", got, MinNoiseFloor)
	}
}
