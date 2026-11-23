package app

import (
	"testing"

	sonaraBackend "sonara/backend/client"
	"sonara/internal/storage"
)

func TestDefaultedProvidersFillsOnlyEmptyFields(t *testing.T) {
	got := defaultedProviders(storage.CapabilityProviders{Text: "deepseek"})
	if got.Text != "deepseek" || got.Vision != "openai" || got.LiveTranscribe != "openai" || got.MeetingProvider != "openai" {
		t.Errorf("got %+v, want the explicit choice kept and the rest defaulted to openai", got)
	}
}

func TestCapabilityProvidersRoundTripAndPersistIndependently(t *testing.T) {
	a, database := testMeetingApp(t)
	a.backend = sonaraBackend.New()
	if err := a.SetTextProvider("deepseek"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetVisionProvider("azure"); err != nil {
		t.Fatal(err)
	}
	got, err := a.GetCapabilityProviders()
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "deepseek" || got.Vision != "azure" || got.LiveTranscribe != "openai" || got.MeetingProvider != "openai" {
		t.Fatalf("got %+v", got)
	}
	// Changing one capability must never disturb an already-saved one.
	stored, err := database.CapabilityProviders()
	if err != nil || stored.Text != "deepseek" || stored.Vision != "azure" {
		t.Fatalf("stored = %+v, err %v", stored, err)
	}
	if a.backend.TextProvider() != "deepseek" || a.backend.VisionProvider() != "azure" {
		t.Errorf("backend selection = %q/%q, want deepseek/azure", a.backend.TextProvider(), a.backend.VisionProvider())
	}
}

func TestSetProviderRejectsUnknownName(t *testing.T) {
	a, _ := testMeetingApp(t)
	if err := a.SetTextProvider("bogus"); err == nil {
		t.Fatal("an unknown provider name must be rejected, not silently stored")
	}
}

func TestAzureConfigPreservesStoredKeyWhenNotRetyped(t *testing.T) {
	a, database := testMeetingApp(t)
	a.backend = sonaraBackend.New()
	if err := a.SetAzureOpenAIConfig("https://x.openai.azure.com", "2024-06-01", "2024-08-01-preview", "chat", "transcribe", "live", "secret-key", false); err != nil {
		t.Fatal(err)
	}
	// Edit only the endpoint, leaving apiKey blank: the stored key must survive.
	if err := a.SetAzureOpenAIConfig("https://y.openai.azure.com", "2024-06-01", "2024-08-01-preview", "chat", "transcribe", "live", "", false); err != nil {
		t.Fatal(err)
	}
	stored, err := database.AzureOpenAISettings()
	if err != nil || stored.APIKey != "secret-key" || stored.Endpoint != "https://y.openai.azure.com" {
		t.Fatalf("stored = %+v, err %v", stored, err)
	}
	form, err := a.GetAzureOpenAISettings()
	if err != nil || !form.HasKey || form.Endpoint != "https://y.openai.azure.com" || form.LiveDeployment != "live" {
		t.Fatalf("form = %+v, err %v (the key itself must never be echoed back)", form, err)
	}

	// clearKey explicitly removes it even with endpoint/deployments present.
	if err := a.SetAzureOpenAIConfig("https://y.openai.azure.com", "2024-06-01", "2024-08-01-preview", "chat", "transcribe", "live", "", true); err != nil {
		t.Fatal(err)
	}
	form, _ = a.GetAzureOpenAISettings()
	if form.HasKey {
		t.Error("clearKey must remove the stored key")
	}
}
