package domain

type Task string

const (
	TaskChat          Task = "chat"
	TaskScreen        Task = "screen_analysis"
	TaskTranscription Task = "transcription"
)

type Request struct {
	Task      Task
	Mode      string
	Prompt    string
	ImageData string
	Language  string
}

type Response struct {
	Text  string `json:"response"`
	Agent string `json:"agent"`
	Model string `json:"model"`
}

type Transcript struct {
	Text  string `json:"text"`
	Model string `json:"model"`
	// Diagnostics for one transcription call. Latency outliers cannot be
	// classified from a single duration: a slow upload, a queue wait on
	// OpenAI's side and a slow body read all look identical from outside.
	RequestID    string `json:"requestId,omitempty"`
	Status       int    `json:"status,omitempty"`
	UploadBytes  int    `json:"uploadBytes,omitempty"`
	ProcessingMS int    `json:"processingMs,omitempty"`
	// WaitMS is time until the response headers arrive: upload plus whatever
	// the request waited server-side. TotalMS adds reading the body.
	WaitMS  int `json:"waitMs,omitempty"`
	TotalMS int `json:"totalMs,omitempty"`
}
