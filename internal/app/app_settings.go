package app

import (
	"fmt"
	"strings"

	"sonara/internal/storage"
)

// GetUIPreferences loads saved panel dimensions from the shared settings store.
func (a *App) GetUIPreferences() (storage.UIPreferences, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return storage.UIPreferences{}, fmt.Errorf("sin persistencia disponible")
	}
	return database.UIPreferences()
}

// SaveUIPreferences persists layout dimensions in the shared settings store.
func (a *App) SaveUIPreferences(preferences storage.UIPreferences) error {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return fmt.Errorf("sin persistencia disponible")
	}
	return database.SaveUIPreferences(preferences)
}

func (a *App) AIEndpoint() string {
	if a.backend == nil {
		return "Backend no inicializado"
	}
	name := map[string]string{"openai": "OpenAI", "deepseek": "DeepSeek", "azure": "Azure OpenAI", "local": "Whisper local"}[a.backend.TextProvider()]
	if name == "" {
		name = a.backend.TextProvider()
	}
	if !a.backend.TextProviderReady() {
		return name + " · Falta configurar"
	}
	return name + " · API key configurada"
}

// SetOpenAIAPIKey configures OpenAI and stores the key so it survives a restart.
// An empty key clears it from both the process and the store.
func (a *App) SetOpenAIAPIKey(key string) error {
	if a.backend == nil {
		return fmt.Errorf("backend no inicializado")
	}
	if err := a.backend.SetAPIKey(key); err != nil {
		return err
	}
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return nil
	}
	if err := database.SetSetting(storage.SettingAPIKey, strings.TrimSpace(key)); err != nil {
		// The key works for this run; only persistence failed, so say so
		// instead of making it look like the key was rejected.
		logf("could not persist the API key: %v", err)
		return fmt.Errorf("la key quedó activa pero no se pudo guardar: %w", err)
	}
	return nil
}

// SetDeepSeekAPIKey sets the DeepSeek key. Whether DeepSeek actually serves
// anything depends on SetTextProvider/SetLiveTranscribeProvider/
// SetMeetingProvider naming it explicitly — this alone does not switch
// anything over.
func (a *App) SetDeepSeekAPIKey(key string) error {
	if a.backend == nil {
		return fmt.Errorf("backend no inicializado")
	}
	if err := a.backend.SetDeepSeekKey(key); err != nil {
		return err
	}
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return nil
	}
	if err := database.SetSetting(storage.SettingDeepSeekKey, strings.TrimSpace(key)); err != nil {
		logf("could not persist the DeepSeek key: %v", err)
		return fmt.Errorf("la key quedó activa pero no se pudo guardar: %w", err)
	}
	return nil
}

// MeetingProvider names who transcribes meeting audio ("deepseek" or
// "openai"), so the UI can say where the audio goes before recording.
func (a *App) MeetingProvider() string {
	if a.backend == nil {
		return ""
	}
	return a.backend.MeetingProvider()
}

// SetGeminiAPIKey configures phase-2 diarization. An empty key turns it off:
// meetings keep transcribing, just without per-participant names.
func (a *App) SetGeminiAPIKey(key string) error {
	key = strings.TrimSpace(key)
	if key != "" && (len(key) < 10 || len(key) > 500 || strings.ContainsAny(key, " \t\r\n")) {
		return fmt.Errorf("clave de Gemini inválida")
	}
	a.diarizer.SetAPIKey(key)
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return nil
	}
	if err := database.SetSetting(storage.SettingGeminiKey, key); err != nil {
		logf("could not persist the Gemini key: %v", err)
		return fmt.Errorf("la key quedó activa pero no se pudo guardar: %w", err)
	}
	return nil
}

// ProviderStatus says which providers are set up, so Settings can mark the
// missing ones. Never the keys themselves.
type ProviderStatus struct {
	OpenAI          bool `json:"openai"`
	DeepSeek        bool `json:"deepseek"`
	Azure           bool `json:"azure"`
	AzureTranscribe bool `json:"azureTranscribe"`
	Gemini          bool `json:"gemini"`
}

func (a *App) ProviderStatus() ProviderStatus {
	a.mu.RLock()
	backend := a.backend
	a.mu.RUnlock()
	status := ProviderStatus{Gemini: a.diarizer.HasCloudKey()}
	if backend != nil {
		status.OpenAI = backend.ProviderReady("openai")
		status.DeepSeek = backend.ProviderReady("deepseek")
		status.Azure = backend.ProviderReady("azure")
		status.AzureTranscribe = backend.AzureTranscribeReady()
	}
	return status
}
