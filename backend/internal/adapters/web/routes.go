package web

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	handler "sonara/backend/internal/adapters/web/handlers/assistant"
	"sonara/backend/internal/platform/config"
	"sonara/backend/internal/usecases/assistant"
)

func NewHandler(respond assistant.RespondUsecase, screen assistant.AnalyzeScreenUsecase, transcribe assistant.TranscribeUsecase, cfg config.Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "sonara-backend"})
	})
	mux.HandleFunc("POST /v1/assist/chat", handler.NewRespondHandler(respond))
	mux.HandleFunc("POST /v1/assist/screen", handler.NewAnalyzeScreenHandler(screen))
	mux.HandleFunc("POST /v1/assist/transcription", handler.NewTranscribeHandler(transcribe))
	return localOnly(mux, cfg.AllowedOrigins)
}

func localOnly(next http.Handler, allowedOrigins []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || net.ParseIP(remoteHost) == nil || !net.ParseIP(remoteHost).IsLoopback() {
			http.Error(w, "local connections only", http.StatusForbidden)
			return
		}
		requestHost := r.Host
		if host, _, err := net.SplitHostPort(requestHost); err == nil {
			requestHost = host
		}
		if !strings.EqualFold(requestHost, "localhost") && !strings.EqualFold(requestHost, "127.0.0.1") && !strings.EqualFold(requestHost, "[::1]") && !strings.EqualFold(requestHost, "::1") {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			if !contains(allowedOrigins, origin) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
