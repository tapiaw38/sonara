package app

import (
	"context"
	"fmt"
	"time"

	"sonara/backend/usage"
	"sonara/internal/storage"
)

func recordAPIUsage(database *storage.Store) usage.Recorder {
	if database == nil {
		return nil
	}
	return func(entry usage.Entry) {
		if err := database.RecordUsage(entry); err != nil {
			logDedup("api-usage", "could not record API usage: %v", err)
		}
	}
}

func (a *App) apiUsageContext(ctx context.Context) context.Context {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	return usage.With(ctx, recordAPIUsage(database), usage.Diarization)
}

func (a *App) GetAPIUsage() (storage.UsageReport, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return storage.UsageReport{}, fmt.Errorf("consumo no disponible")
	}
	return database.APIUsage(time.Now())
}

func (a *App) SaveAPIUsagePrices(prices []storage.UsagePrice) error {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return fmt.Errorf("consumo no disponible")
	}
	return database.SaveUsagePrices(prices)
}
