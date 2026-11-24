package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sonara/backend/usage"
	"time"
)

const voiceModel = "gpt-realtime-2.1-mini"

type VoiceEvent struct {
	Type         string `json:"type"`
	ResponseID   string `json:"response_id"`
	ItemID       string `json:"item_id"`
	ContentIndex int    `json:"content_index"`
	Text         string `json:"-"`
	Status       string `json:"-"`
	Audio        []byte `json:"-"`
	Err          error  `json:"-"`
}

// VoiceSession carries conversational audio independently of transcription sessions.
type VoiceSession struct {
	transport *Session
	events    chan VoiceEvent
}

func DialVoice(ctx context.Context, baseURL, key, instructions string) (*VoiceSession, error) {
	if key == "" {
		return nil, fmt.Errorf("falta la clave de OpenAI para voz a voz")
	}
	endpoint, err := EndpointFor(baseURL)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	u.RawQuery = url.Values{"model": {voiceModel}}.Encode()
	conn, err := dialWithRetry(ctx, u.String(), http.Header{"Authorization": {"Bearer " + key}})
	if err != nil {
		usage.Start(ctx, "openai", voiceModel, usage.Practice).Finish(false)
		return nil, fmt.Errorf("voz a voz: %w", err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	wire := &Session{conn: conn, cancel: cancel, writeTimeout: 5 * time.Second}
	context.AfterFunc(runCtx, func() { conn.Close() })
	format := map[string]any{"type": "audio/pcm", "rate": 24000}
	if err := wire.send(map[string]any{
		"type": "session.update",
		"session": map[string]any{
			"type": "realtime", "model": voiceModel, "instructions": instructions, "output_modalities": []string{"audio"},
			"audio": map[string]any{
				"input": map[string]any{
					"format":        format,
					"transcription": map[string]any{"model": "gpt-transcribe"},
					"turn_detection": map[string]any{
						"type": "semantic_vad", "eagerness": "low", "create_response": false, "interrupt_response": true,
					},
				},
				"output": map[string]any{"format": format, "voice": "marin"},
			},
		},
	}); err != nil {
		usage.Start(ctx, "openai", voiceModel, usage.Practice).Finish(false)
		wire.Close()
		return nil, err
	}
	s := &VoiceSession{transport: wire, events: make(chan VoiceEvent, 32)}
	go s.readVoice(runCtx)
	return s, nil
}

func (s *VoiceSession) Append(pcm []byte) error   { return s.transport.Append(pcm) }
func (s *VoiceSession) Events() <-chan VoiceEvent { return s.events }
func (s *VoiceSession) Close() error              { return s.transport.Close() }

// Respond starts one response after the application has classified the input.
func (s *VoiceSession) Respond() error {
	return s.transport.send(map[string]any{"type": "response.create"})
}

// DeleteInput removes a spoken control phrase from the model's conversation.
func (s *VoiceSession) DeleteInput(itemID string) error {
	return s.transport.send(map[string]any{"type": "conversation.item.delete", "item_id": itemID})
}

func (s *VoiceSession) Truncate(itemID string, contentIndex, playedMS int) error {
	return s.transport.send(map[string]any{
		"type": "conversation.item.truncate", "item_id": itemID,
		"content_index": contentIndex, "audio_end_ms": playedMS,
	})
}

func (s *VoiceSession) readVoice(ctx context.Context) {
	defer close(s.events)
	responses := make(map[string]*usage.Meter)
	transcripts := make(map[string]*usage.Meter)
	var pendingSeconds float64
	defer func() {
		for _, m := range responses {
			m.Finish(false)
		}
		for _, m := range transcripts {
			m.Finish(false)
		}
	}()
	emit := func(event VoiceEvent) bool {
		select {
		case s.events <- event:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		var frame struct {
			VoiceEvent
			Delta      string `json:"delta"`
			StartMS    int64  `json:"audio_start_ms"`
			EndMS      int64  `json:"audio_end_ms"`
			Transcript string `json:"transcript"`
			Error      *struct {
				Code string `json:"code"`
			} `json:"error"`
			Response struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"response"`
		}
		_, data, err := s.transport.conn.ReadMessage()
		if err != nil {
			if ctx.Err() == nil {
				emit(VoiceEvent{Err: fmt.Errorf("se cerró la conexión de voz a voz")})
			}
			return
		}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		event := frame.VoiceEvent
		switch event.Type {
		case "response.output_audio.delta":
			event.Audio, event.Err = base64.StdEncoding.DecodeString(frame.Delta)
			if m := responses[event.ResponseID]; m != nil {
				m.AudioSeconds += float64(len(event.Audio)) / 48000
			}
		case "response.created":
			event.ResponseID = frame.Response.ID
			m := usage.Start(ctx, "openai", voiceModel, usage.Practice)
			m.AudioSeconds, pendingSeconds = pendingSeconds, 0
			responses[event.ResponseID] = m
		case "response.done":
			event.ResponseID, event.Status = frame.Response.ID, frame.Response.Status
			if m := responses[event.ResponseID]; m != nil {
				m.Observe(data)
				m.Finish(event.Status == "completed")
				delete(responses, event.ResponseID)
			}
			if frame.Response.Status == "failed" || frame.Response.Status == "incomplete" {
				event.Err = fmt.Errorf("OpenAI no completó la respuesta de voz (%s)", frame.Response.Status)
			}
		case "error":
			code := "sin código"
			if frame.Error != nil && frame.Error.Code != "" {
				code = frame.Error.Code
			}
			event.Err = fmt.Errorf("OpenAI Realtime: %s", code)
		case "conversation.item.input_audio_transcription.delta", "response.output_audio_transcript.delta":
			event.Text = frame.Delta
		case "conversation.item.input_audio_transcription.completed", "conversation.item.input_audio_transcription.failed":
			event.Text = frame.Transcript
			if m := transcripts[event.ItemID]; m != nil {
				m.Observe(data)
				m.Finish(event.Type == "conversation.item.input_audio_transcription.completed")
				delete(transcripts, event.ItemID)
			}
		case "response.output_audio_transcript.done":
			event.Text = frame.Transcript
		case "input_audio_buffer.speech_started":
			m := usage.Start(ctx, "openai", "gpt-transcribe", usage.Practice)
			m.AudioSeconds = -float64(frame.StartMS) / 1000
			transcripts[event.ItemID] = m
		case "input_audio_buffer.speech_stopped":
			if m := transcripts[event.ItemID]; m != nil {
				m.AudioSeconds = max(0, m.AudioSeconds+float64(frame.EndMS)/1000)
				pendingSeconds += m.AudioSeconds
			}
		case "session.updated", "input_audio_buffer.committed", "response.output_audio.done":
		default:
			continue
		}
		if !emit(event) {
			return
		}
	}
}
