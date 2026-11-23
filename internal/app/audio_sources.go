package app

import (
	"context"
	"fmt"

	"sonara/internal/pipewire"
	"sonara/internal/storage"
)

func (a *App) Microphones() ([]pipewire.Source, error) {
	return a.microphones(a.requestContext())
}

func (a *App) microphones(ctx context.Context) ([]pipewire.Source, error) {
	a.mu.RLock()
	hook := a.microphonesHook
	a.mu.RUnlock()
	if hook != nil {
		return hook(ctx)
	}
	return pipewire.ListMicrophones(ctx)
}

func (a *App) AudioSources() ([]pipewire.Source, error) {
	return pipewire.OutputStreams(a.requestContext())
}

func (a *App) SetAudioSource(target string) (string, error) {
	if target != "" {
		sources, err := a.AudioSources()
		if err != nil {
			return "", err
		}
		app := ""
		for _, source := range sources {
			if source.Target == target {
				app = source.App
				break
			}
		}
		if app == "" {
			return "", fmt.Errorf("fuente de audio ya no disponible")
		}
		a.mu.Lock()
		a.selectedAudioSource, a.preferredAudioApp = target, app
		a.audioGuessing = false
		database := a.store
		a.mu.Unlock()
		if database != nil {
			if err := database.SetSetting(storage.SettingAudioApp, app); err != nil {
				logf("could not persist the preferred audio application: %v", err)
			}
		}
		return target, nil
	}
	a.mu.Lock()
	a.selectedAudioSource, a.preferredAudioApp = "", ""
	database := a.store
	a.mu.Unlock()
	if database != nil {
		if err := database.SetSetting(storage.SettingAudioApp, ""); err != nil {
			logf("could not clear the preferred audio application: %v", err)
		}
	}
	return "", nil
}

type audioSourceState struct {
	target     string
	guessed    bool
	candidates int
}

func (a *App) audioTarget(ctx context.Context) (string, error) {
	state, err := a.resolveAudioTarget(ctx)
	a.mu.Lock()
	a.audioGuessing, a.audioCandidates = state.guessed, state.candidates
	a.mu.Unlock()
	return state.target, err
}

func (a *App) resolveAudioTarget(ctx context.Context) (audioSourceState, error) {
	sources, err := pipewire.OutputStreams(ctx)
	if err != nil {
		return audioSourceState{}, err
	}
	candidates := len(sources)
	a.mu.RLock()
	selected, preferred := a.selectedAudioSource, a.preferredAudioApp
	a.mu.RUnlock()
	if selected != "" {
		for _, source := range sources {
			if source.Target == selected {
				return audioSourceState{target: selected, candidates: candidates}, nil
			}
		}
		if adopted, err := pipewire.AdoptPreferredApp(sources, preferred); err == nil {
			a.mu.Lock()
			a.selectedAudioSource = adopted
			a.mu.Unlock()
			logDedup("audio-latch", "audio source %s vanished; re-latched onto %q as target %s", selected, preferred, adopted)
			return audioSourceState{target: adopted, candidates: candidates}, nil
		}

		if guess, guessed, err := pipewire.ChooseStream(sources); err == nil {
			a.mu.Lock()
			a.selectedAudioSource = guess
			a.mu.Unlock()
			logDedup("audio-latch", "audio source %s vanished; %q is ambiguous, guessing %s", selected, preferred, guess)
			return audioSourceState{target: guess, guessed: guessed, candidates: candidates}, nil
		}
		logDedup("audio-latch", "audio source %s vanished and %q is ambiguous or absent; waiting for the user", selected, preferred)
		return audioSourceState{candidates: candidates}, fmt.Errorf("fuente elegida desapareció; actualizá la lista")
	}
	if adopted, err := pipewire.AdoptPreferredApp(sources, preferred); err == nil {
		a.mu.Lock()
		a.selectedAudioSource = adopted
		a.mu.Unlock()
		logDedup("audio-latch", "using remembered application %q as target %s", preferred, adopted)
		return audioSourceState{target: adopted, candidates: candidates}, nil
	}
	target, guessed, err := pipewire.ChooseStream(sources)
	if err != nil {
		return audioSourceState{candidates: candidates}, err
	}
	return audioSourceState{target: target, guessed: guessed, candidates: candidates}, nil
}
