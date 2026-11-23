// Package meeting holds the meeting-transcription logic that needs no app
// state: cutting each track into blocks, and rendering a stored meeting.
package meeting

import (
	"slices"

	"sonara/internal/audio"
)

const (
	FrameMS = 100 // one audio.FrameBytes frame
	// A block is cut at a pause once it is at least 3s long, at any quiet frame
	// once it reaches 5s, and unconditionally at 8s, so text shows up while
	// people are still talking. All of it sits far under DeepSeek's 120s /
	// 8 MiB limit per request.
	minBlockFrames  = 30
	softBlockFrames = 50
	hardBlockFrames = 80
	quietCutFrames  = 4
)

// Block is a stretch of one track, cut at a pause, ready to transcribe.
type Block struct {
	Track   string
	StartMS int64
	EndMS   int64
	PCM     []byte
	// Voiced is false for a block that never rose above the ambient level:
	// uploading silence would only cost money.
	Voiced bool
}

// BlockCutter turns a stream of 100ms frames into transcribable blocks.
type BlockCutter struct {
	track   string
	pcm     []byte
	startMS int64
	frames  int
	voiced  int
	quiet   int
	ambient audio.NoiseFloor
	// recent holds every frame's level until ambient has its first quiet
	// sample: a mic whose hiss already sits above meetNoiseFloor never yields
	// one, so after cutterSeedFrames the quietest frame seeds it instead.
	recent []float64
	// speech is the voice detector (nil: loudness only, as in tests).
	speech func([]byte) bool
}

const cutterSeedFrames = 20

// minVoicedFrames: a block needs half a second of actual voice to be worth
// transcribing.
const minVoicedFrames = 5

// NewBlockCutter cuts one track. speech is the voice detector; nil counts
// every loud frame as voice (tests).
func NewBlockCutter(track string, speech func([]byte) bool) *BlockCutter {
	return &BlockCutter{track: track, speech: speech}
}

func (c *BlockCutter) threshold(level float64) float64 {
	if c.ambient.Empty() {
		c.recent = append(c.recent, level)
		if len(c.recent) >= cutterSeedFrames {
			c.ambient.Observe(slices.Min(c.recent))
			c.recent = nil
		}
	}
	return c.ambient.Threshold()
}

// Add feeds one frame starting at frameStartMS and returns a block when one
// is complete.
func (c *BlockCutter) Add(frame []byte, frameStartMS int64) (Block, bool) {
	level := audio.RMS(frame)
	loud := level >= c.threshold(level)
	if !loud {
		c.ambient.Observe(level)
	}
	voice := audio.IsVoice(c.speech, frame, loud)
	if c.frames == 0 {
		c.startMS = frameStartMS
	}
	c.pcm = append(c.pcm, frame...)
	c.frames++
	if voice {
		c.voiced++
		c.quiet = 0
	} else {
		c.quiet++
	}
	cut := c.frames >= hardBlockFrames ||
		(c.frames >= softBlockFrames && c.quiet >= 1) ||
		(c.frames >= minBlockFrames && c.quiet >= quietCutFrames)
	if !cut {
		return Block{}, false
	}
	return c.Flush()
}

// Empty reports whether no frame is buffered since the last block.
func (c *BlockCutter) Empty() bool { return c.frames == 0 }

// Flush returns whatever is buffered as a block, or false when nothing is.
func (c *BlockCutter) Flush() (Block, bool) {
	if c.frames == 0 {
		return Block{}, false
	}
	block := Block{
		Track: c.track, StartMS: c.startMS, EndMS: c.startMS + int64(c.frames)*FrameMS,
		PCM: c.pcm, Voiced: c.voiced >= minVoicedFrames,
	}
	c.pcm, c.frames, c.voiced, c.quiet = nil, 0, 0, 0
	return block, true
}
