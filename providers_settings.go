package main

import (
	"fmt"
	"strings"

	sonaraBackend "sonara/backend/client"
)

// Which provider serves each capability (text, vision, live-copilot
// transcription, meeting transcription) is one explicit choice per
// capability, set from Settings and persisted in the database — never an
// automatic fallback chain, and never an environment variable. This mirrors
// the backend's providers.Registry/Selector/Transcriber on the Go side: the
// App methods here are just the Settings-facing surface for it.

// defaultedProviders fills every empty field with "openai", so a freshly
// opened Settings panel (nothing saved yet) shows a real selection instead
// of a blank dropdown.
func defaultedProviders(p CapabilityProviders) CapabilityProviders {
	if p.Text == "" {
		p.Text = "openai"
	}
	if p.Vision == "" {
		p.Vision = "openai"
	}
	if p.LiveTranscribe == "" {
		p.LiveTranscribe = "openai"
	}
	if p.MeetingProvider == "" {
		p.MeetingProvider = "openai"
	}
	if p.LiveMode == "" {
		p.LiveMode = liveModeBatch
	}
	return p
}

// GetCapabilityProviders returns the current choice for every capability,
// for Settings to show on open.
func (a *App) GetCapabilityProviders() (CapabilityProviders, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return defaultedProviders(CapabilityProviders{}), nil
	}
	stored, err := database.capabilityProviders()
	if err != nil {
		return CapabilityProviders{}, err
	}
	return defaultedProviders(stored), nil
}

// updateCapabilityProviders persists one field at a time without disturbing
// the other three choices: read the saved set, apply change, write it back.
func (a *App) updateCapabilityProviders(apply func(*CapabilityProviders)) error {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return nil
	}
	current, err := database.capabilityProviders()
	if err != nil {
		return err
	}
	apply(&current)
	return database.saveCapabilityProviders(current)
}

// SetTextProvider chooses who answers a plain text request. Takes effect
// immediately and only for text; see SetVisionProvider for screen captures.
func (a *App) SetTextProvider(name string) error {
	if a.backend == nil {
		return fmt.Errorf("backend no inicializado")
	}
	if err := a.backend.SetTextProvider(name); err != nil {
		return err
	}
	if err := a.updateCapabilityProviders(func(p *CapabilityProviders) { p.Text = name }); err != nil {
		logf("could not persist the text provider: %v", err)
		return fmt.Errorf("el proveedor quedó activo pero no se pudo guardar: %w", err)
	}
	return nil
}

// SetVisionProvider chooses who answers a request that carries a screen
// capture (DeepSeek can be selected but will error on every call: it only
// serves text).
func (a *App) SetVisionProvider(name string) error {
	if a.backend == nil {
		return fmt.Errorf("backend no inicializado")
	}
	if err := a.backend.SetVisionProvider(name); err != nil {
		return err
	}
	if err := a.updateCapabilityProviders(func(p *CapabilityProviders) { p.Vision = name }); err != nil {
		logf("could not persist the vision provider: %v", err)
		return fmt.Errorf("el proveedor quedó activo pero no se pudo guardar: %w", err)
	}
	return nil
}

// SetLiveTranscribeProvider chooses who transcribes the interview copilot's
// audio, independently of meeting transcription (SetMeetingProvider).
func (a *App) SetLiveTranscribeProvider(name string) error {
	if a.backend == nil {
		return fmt.Errorf("backend no inicializado")
	}
	if err := a.backend.SetLiveTranscribeProvider(name); err != nil {
		return err
	}
	if err := a.updateCapabilityProviders(func(p *CapabilityProviders) { p.LiveTranscribe = name }); err != nil {
		logf("could not persist the live transcription provider: %v", err)
		return fmt.Errorf("el proveedor quedó activo pero no se pudo guardar: %w", err)
	}
	return nil
}

// SetMeetingProvider chooses who transcribes meeting recordings.
func (a *App) SetMeetingProvider(name string) error {
	if a.backend == nil {
		return fmt.Errorf("backend no inicializado")
	}
	if err := a.backend.SetMeetingProvider(name); err != nil {
		return err
	}
	if err := a.updateCapabilityProviders(func(p *CapabilityProviders) { p.MeetingProvider = name }); err != nil {
		logf("could not persist the meeting provider: %v", err)
		return fmt.Errorf("el proveedor quedó activo pero no se pudo guardar: %w", err)
	}
	return nil
}

// AzureOpenAIForm is what Settings sends back and forth: no APIKey field in
// either direction once saved — same "never echoed back" policy as every
// other key in this app. HasKey tells the UI whether one is already stored,
// without revealing it.
type AzureOpenAIForm struct {
	HasKey               bool   `json:"hasKey"`
	Endpoint             string `json:"endpoint"`
	ChatAPIVersion       string `json:"chatApiVersion"`
	TranscribeAPIVersion string `json:"transcribeApiVersion"`
	ChatDeployment       string `json:"chatDeployment"`
	TranscribeDeployment string `json:"transcribeDeployment"`
}

// GetAzureOpenAISettings returns everything except the key itself, for
// Settings to pre-fill the endpoint/deployment fields on open.
func (a *App) GetAzureOpenAISettings() (AzureOpenAIForm, error) {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return AzureOpenAIForm{}, nil
	}
	stored, err := database.azureOpenAISettings()
	if err != nil {
		return AzureOpenAIForm{}, err
	}
	return AzureOpenAIForm{
		HasKey: stored.APIKey != "", Endpoint: stored.Endpoint,
		ChatAPIVersion: stored.ChatAPIVersion, TranscribeAPIVersion: stored.TranscribeAPIVersion,
		ChatDeployment: stored.ChatDeployment, TranscribeDeployment: stored.TranscribeDeployment,
	}, nil
}

// SetAzureOpenAIConfig saves the whole form as one unit. Chat and
// transcription each get their own API version (see
// azureopenai.Config.ChatAPIVersion): Azure can run them at different
// versions on the same resource. An empty apiKey keeps whatever key is
// already stored (so the rest can be edited without retyping it); clearing
// the key itself is a separate, explicit action — pass clearKey=true.
func (a *App) SetAzureOpenAIConfig(endpoint, chatAPIVersion, transcribeAPIVersion, chatDeployment, transcribeDeployment, apiKey string, clearKey bool) error {
	if a.backend == nil {
		return fmt.Errorf("backend no inicializado")
	}
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()

	settings := AzureOpenAISettings{
		Endpoint:             strings.TrimSpace(endpoint),
		ChatAPIVersion:       strings.TrimSpace(chatAPIVersion),
		TranscribeAPIVersion: strings.TrimSpace(transcribeAPIVersion),
		ChatDeployment:       strings.TrimSpace(chatDeployment),
		TranscribeDeployment: strings.TrimSpace(transcribeDeployment),
	}
	switch {
	case clearKey:
		settings.APIKey = ""
	case strings.TrimSpace(apiKey) != "":
		settings.APIKey = strings.TrimSpace(apiKey)
	case database != nil:
		if previous, err := database.azureOpenAISettings(); err == nil {
			settings.APIKey = previous.APIKey
		}
	}

	if err := a.backend.SetAzureOpenAIConfig(sonaraBackend.AzureConfig{
		APIKey: settings.APIKey, Endpoint: settings.Endpoint,
		ChatAPIVersion: settings.ChatAPIVersion, TranscribeAPIVersion: settings.TranscribeAPIVersion,
		ChatDeployment: settings.ChatDeployment, TranscribeDeployment: settings.TranscribeDeployment,
	}); err != nil {
		return err
	}
	if database == nil {
		return nil
	}
	if err := database.saveAzureOpenAISettings(settings); err != nil {
		logf("could not persist the Azure OpenAI settings: %v", err)
		return fmt.Errorf("la configuración quedó activa pero no se pudo guardar: %w", err)
	}
	return nil
}
