// Package usage records only numeric usage and routing metadata.
package usage

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"time"
)

const (
	Copilot     = "copilot"
	Practice    = "practice"
	Live        = "live-transcription"
	Meeting     = "meeting-transcription"
	TTS         = "tts"
	Translation = "translation"
	Summary     = "summary"
	Names       = "names"
	Diarization = "diarization"
)

type Entry struct {
	At                                                 time.Time
	Provider, Function, Model                          string
	Input, Output, AudioInput, AudioOutput, Characters int64
	AudioSeconds                                       float64
	TokensKnown, OK                                    bool
}

type Recorder func(Entry)

type route struct {
	record   Recorder
	function string
}
type routeKey struct{}

func With(ctx context.Context, record Recorder, function string) context.Context {
	return context.WithValue(ctx, routeKey{}, route{record, function})
}

type Meter struct {
	Entry
	record Recorder
}

func Start(ctx context.Context, provider, model, fallback string) *Meter {
	r, _ := ctx.Value(routeKey{}).(route)
	if r.function == "" {
		r.function = fallback
	}
	return &Meter{Entry: Entry{At: time.Now(), Provider: provider, Function: r.function, Model: model}, record: r.record}
}

func (m *Meter) Finish(ok bool) {
	if m.record != nil {
		m.OK = ok
		m.record(m.Entry)
	}
}

type tokens struct {
	GeminiThoughts int64   `json:"total_thought_tokens"`
	Duration       float64 `json:"duration"`
	Input          *int64  `json:"input_tokens"`
	Output         *int64  `json:"output_tokens"`
	Prompt         *int64  `json:"prompt_tokens"`
	Completion     *int64  `json:"completion_tokens"`
	GeminiInput    *int64  `json:"total_input_tokens"`
	GeminiOutput   *int64  `json:"total_output_tokens"`
	Seconds        float64 `json:"seconds"`
	InputDetails   struct {
		Audio int64 `json:"audio_tokens"`
	} `json:"input_token_details"`
	OutputDetails struct {
		Audio int64 `json:"audio_tokens"`
	} `json:"output_token_details"`
}

func (m *Meter) Observe(data []byte) {
	if m.record == nil || !bytes.Contains(data, []byte(`"usage"`)) {
		return
	}
	var frame struct {
		Usage    *tokens `json:"usage"`
		Response struct {
			Usage *tokens `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &frame) != nil {
		return
	}
	t := frame.Usage
	if t == nil {
		t = frame.Response.Usage
	}
	if t == nil {
		return
	}
	for _, value := range []*int64{t.Input, t.Prompt, t.GeminiInput} {
		if value != nil {
			m.Input, m.TokensKnown = *value, true
			break
		}
	}
	for _, value := range []*int64{t.Output, t.Completion, t.GeminiOutput} {
		if value != nil {
			m.Output, m.TokensKnown = *value, true
			break
		}
	}
	if t.GeminiOutput != nil {
		m.Output += t.GeminiThoughts
	}
	m.AudioInput, m.AudioOutput = t.InputDetails.Audio, t.OutputDetails.Audio
	if t.Seconds > 0 {
		m.AudioSeconds = t.Seconds
	} else if t.Duration > 0 {
		m.AudioSeconds = t.Duration
	}
}

func ObserveSSE(line []byte, meters ...*Meter) {
	if len(meters) == 0 {
		return
	}
	if body, ok := bytes.CutPrefix(line, []byte("data:")); ok {
		meters[0].Observe(bytes.TrimSpace(body))
	}
}

// WAVSeconds reads PCM byte rate and data size, including WAVs with extra chunks.
func WAVSeconds(wav []byte) float64 {
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return 0
	}
	var rate uint32
	for off := 12; off+8 <= len(wav); {
		size := int(binary.LittleEndian.Uint32(wav[off+4:]))
		start := off + 8
		if size > len(wav)-start {
			return 0
		}
		switch string(wav[off : off+4]) {
		case "fmt ":
			if size >= 16 {
				rate = binary.LittleEndian.Uint32(wav[start+8:])
			}
		case "data":
			if rate > 0 {
				return float64(size) / float64(rate)
			}
		}
		off = start + size + size%2
	}
	return 0
}
