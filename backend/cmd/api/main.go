package main

import (
	"log"
	"net/http"
	"strings"
	"time"

	"sonara/backend/internal/adapters/web"
	"sonara/backend/internal/adapters/web/integrations/openai"
	"sonara/backend/internal/platform/config"
	"sonara/backend/internal/usecases/assistant"
)

func main() {
	cfg := config.Load()
	if !isLoopbackAddress(cfg.Address) {
		log.Fatal("SONARA_ADDR must bind to loopback until authenticated deployment is configured")
	}
	provider := openai.NewGateway(cfg)
	server := &http.Server{
		Addr: cfg.Address,
		Handler: web.NewHandler(
			assistant.NewRespondUsecase(provider, cfg),
			assistant.NewAnalyzeScreenUsecase(provider, cfg),
			assistant.NewTranscribeUsecase(provider, cfg.TranscribeModel),
			cfg,
		),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("sonara-backend listening on %s", cfg.Address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func isLoopbackAddress(address string) bool {
	return strings.HasPrefix(address, "127.0.0.1:") || strings.HasPrefix(address, "localhost:") || strings.HasPrefix(address, "[::1]:")
}
