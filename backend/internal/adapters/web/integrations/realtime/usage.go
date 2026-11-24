package realtime

import (
	"context"
	"sync"

	"sonara/backend/usage"
)

// Live audio is billed per committed item. The send side queues timings
// before writing commit; the server acknowledges them in the same order.
type liveUsage struct {
	ctx             context.Context
	provider, model string
	mu              sync.Mutex
	bytes           int
	commits         []*usage.Meter
	items           map[string]*usage.Meter
}

func (u *liveUsage) commit() {
	m := usage.Start(u.ctx, u.provider, u.model, usage.Live)
	m.AudioSeconds = float64(u.bytes) / 48000
	u.bytes = 0
	u.commits = append(u.commits, m)
}

func (u *liveUsage) receive(event serverEvent, data []byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch event.Type {
	case "input_audio_buffer.committed":
		if len(u.commits) > 0 {
			u.items[event.ItemID] = u.commits[0]
			u.commits[0] = nil
			u.commits = u.commits[1:]
		}
	case "conversation.item.input_audio_transcription.completed", "conversation.item.input_audio_transcription.failed":
		if m := u.items[event.ItemID]; m != nil {
			m.Observe(data)
			m.Finish(event.Type == "conversation.item.input_audio_transcription.completed")
			delete(u.items, event.ItemID)
		}
	}
}

func (u *liveUsage) finish() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, meter := range u.commits {
		meter.Finish(false)
	}
	for _, meter := range u.items {
		meter.Finish(false)
	}
	u.commits, u.items = nil, nil
}
