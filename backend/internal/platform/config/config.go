package config

import (
	"os"
	"slices"
	"strings"
	"time"
)

type Config struct {
	Address         string
	AllowedOrigins  []string
	OpenAIKey       string
	OpenAIBaseURL   string
	FastModel       string
	ReasoningModel  string
	ExpertModel     string
	TranscribeModel string
	MaxOutputTokens int
	RequestTimeout  time.Duration
}

func Load() Config {
	return Config{
		Address:         value("SONARA_ADDR", "127.0.0.1:8777"),
		AllowedOrigins:  listValue("SONARA_ALLOWED_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173,http://wails.localhost"),
		OpenAIKey:       strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		OpenAIBaseURL:   strings.TrimRight(value("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/"),
		FastModel:       value("OPENAI_MODEL_FAST", "gpt-6-luna"),
		ReasoningModel:  value("OPENAI_MODEL_REASONING", "gpt-6.1-sol"),
		ExpertModel:     value("OPENAI_MODEL_EXPERT", "gpt-6-astra"),
		TranscribeModel: value("OPENAI_MODEL_TRANSCRIBE", "gpt-transcribe"),
		MaxOutputTokens: intValue("OPENAI_MAX_OUTPUT_TOKENS", 1200),
		RequestTimeout:  durationValue("OPENAI_TIMEOUT", 90*time.Second),
	}
}

func listValue(key, fallback string) []string {
	raw := value(key, fallback)
	items := strings.Split(raw, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" && !slices.Contains(result, item) {
			result = append(result, item)
		}
	}
	return result
}

func value(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func intValue(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed := 0
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return fallback
		}
		parsed = parsed*10 + int(digit-'0')
	}
	if parsed < 1 || parsed > 8192 {
		return fallback
	}
	return parsed
}

func durationValue(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < time.Second || parsed > 5*time.Minute {
		return fallback
	}
	return parsed
}
