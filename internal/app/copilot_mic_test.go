package app

import "testing"

func TestKeepMicBlock(t *testing.T) {
	if !keepMicBlock(1, true) {
		t.Error("with echo cancellation, talking over the meeting must be kept")
	}
	if keepMicBlock(0.8, false) {
		t.Error("without echo cancellation, a block mostly over meeting voice is echo")
	}
	if !keepMicBlock(0.1, false) {
		t.Error("a block barely overlapping the meeting is the user")
	}
}
