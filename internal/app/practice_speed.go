package app

import (
	"fmt"
	"strconv"
)

const practiceVoiceSpeedSetting = "practice_voice_speed"

func validPracticeVoiceSpeed(speed float64) bool {
	return speed >= 0.5 && speed <= 1.5
}

func (a *App) practiceVoiceSpeed(provider string) float64 {
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database != nil {
		if speed, err := strconv.ParseFloat(database.Setting(practiceVoiceSpeedSetting), 64); err == nil && validPracticeVoiceSpeed(speed) {
			return speed
		}
	}
	if provider == "local" {
		return 0.85
	}
	return 1
}

func (a *App) GetPracticeVoiceSpeed() float64 {
	if a.backend == nil {
		return 1
	}
	return a.practiceVoiceSpeed(a.backend.PracticeVoiceProvider())
}

func (a *App) SetPracticeVoiceSpeed(speed float64) error {
	if !validPracticeVoiceSpeed(speed) {
		return fmt.Errorf("la velocidad de la voz debe estar entre 0,5× y 1,5×")
	}
	a.mu.RLock()
	database := a.store
	a.mu.RUnlock()
	if database == nil {
		return fmt.Errorf("sin persistencia disponible")
	}
	return database.SetSetting(practiceVoiceSpeedSetting, strconv.FormatFloat(speed, 'f', -1, 64))
}
