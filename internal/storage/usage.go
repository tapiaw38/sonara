package storage

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"sonara/backend/usage"
)

const usagePricesSetting = "api_usage_prices"

type UsagePrice struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Enabled         bool    `json:"enabled"`
	Input           float64 `json:"input"`
	Output          float64 `json:"output"`
	AudioInput      float64 `json:"audioInput"`
	AudioOutput     float64 `json:"audioOutput"`
	PerMinute       float64 `json:"perMinute"`
	PerMillionChars float64 `json:"perMillionChars"`
	Source          string  `json:"source"`
	DurationBilling bool    `json:"durationBilling"`
}

var defaultUsagePrices = []UsagePrice{
	{Provider: "openai", Model: "gpt-6-luna", Enabled: true, Input: .10, Output: .50},
	{Provider: "openai", Model: "gpt-6.1-sol", Enabled: true, Input: 2, Output: 10},
	{Provider: "openai", Model: "gpt-6-astra", Enabled: true, Input: 10, Output: 50},
	{Provider: "openai", Model: "gpt-transcribe", Enabled: true, PerMinute: .0045, DurationBilling: true},
	{Provider: "openai", Model: "gpt-live-transcribe", Enabled: true, PerMinute: .017, DurationBilling: true},
	{Provider: "openai", Model: "whisper-1", Enabled: true, PerMinute: .006, DurationBilling: true},
	{Provider: "openai", Model: "gpt-4o-mini-tts", Enabled: true, PerMinute: .015, DurationBilling: true},
	{Provider: "openai", Model: "tts-1", Enabled: true, PerMillionChars: 15},
	{Provider: "openai", Model: "tts-1-hd", Enabled: true, PerMillionChars: 30},
	{Provider: "openai", Model: "gpt-realtime-2.1-mini", Enabled: true, Input: .60, Output: 2.40, AudioInput: 10, AudioOutput: 20, PerMinute: .015},
	{Provider: "gemini", Model: "gemini-files", Enabled: true},
	{Provider: "gemini", Model: "gemini-4-hydrogen", Enabled: true, Input: 1.50, Output: 7.50, PerMinute: .003},
	{Provider: "deepseek", Model: "deepseek-asr", Enabled: true, PerMinute: .116 / 60, DurationBilling: true},
	{Provider: "deepseek", Model: "deepseek-flash", Enabled: true, Input: .12, Output: .24},
	{Provider: "deepseek", Model: "deepseek-v4-pro", Enabled: true, Input: 1.74, Output: 3.48},
}

type UsageTotals struct {
	Calls        int64   `json:"calls"`
	Errors       int64   `json:"errors"`
	Input        int64   `json:"input"`
	Output       int64   `json:"output"`
	AudioSeconds float64 `json:"audioSeconds"`
	Characters   int64   `json:"characters"`
	USD          float64 `json:"usd"`
	Unpriced     int64   `json:"unpriced"`
}
type UsageGroup struct {
	Name   string      `json:"name"`
	Totals UsageTotals `json:"totals"`
}
type UsagePeriod struct {
	ID        string       `json:"id"`
	Totals    UsageTotals  `json:"totals"`
	Providers []UsageGroup `json:"providers"`
	Functions []UsageGroup `json:"functions"`
	Models    []UsageGroup `json:"models"`
}
type UsageReport struct {
	Projection     UsageProjection `json:"projection"`
	Periods        []UsagePeriod   `json:"periods"`
	Prices         []UsagePrice    `json:"prices"`
	CatalogUpdated string          `json:"catalogUpdated"`
}

func (s *Store) RecordUsage(e usage.Entry) error {
	_, err := s.db.Exec(`INSERT INTO api_usage
		(at_ms, provider, function, model, input_tokens, output_tokens, audio_input_tokens,
		audio_output_tokens, audio_seconds, characters, tokens_known, ok)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.UnixMilli(), e.Provider, e.Function, e.Model, e.Input, e.Output, e.AudioInput,
		e.AudioOutput, max(0, e.AudioSeconds), e.Characters, e.TokensKnown, e.OK)
	return err
}

func priceKey(provider, model string) [2]string { return [2]string{provider, model} }

func (s *Store) SaveUsagePrices(prices []UsagePrice) error {
	keys := make(map[[2]string]bool)
	for _, p := range prices {
		switch p.Provider {
		case "openai", "azure", "deepseek", "gemini":
		default:
			return fmt.Errorf("proveedor de precio inválido")
		}
		k := priceKey(p.Provider, p.Model)
		if strings.TrimSpace(p.Model) == "" || len(p.Model) > 256 || keys[k] {
			return fmt.Errorf("modelo de precio inválido o duplicado")
		}
		keys[k] = true
		for _, value := range []float64{p.Input, p.Output, p.AudioInput, p.AudioOutput, p.PerMinute, p.PerMillionChars} {
			if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("los precios deben ser números no negativos")
			}
		}
	}
	manual, err := s.manualUsagePrices()
	if err != nil {
		return err
	}
	for _, p := range prices {
		k := priceKey(p.Provider, p.Model)
		if p.Source == "reset" {
			delete(manual, k)
		} else {
			p.Source = "manual"
			manual[k] = p
		}
	}
	prices = make([]UsagePrice, 0, len(manual))
	for _, price := range manual {
		prices = append(prices, price)
	}
	data, err := json.Marshal(prices)
	if err != nil {
		return err
	}
	return s.SetSetting(usagePricesSetting, string(data))
}

func (s *Store) manualUsagePrices() (map[[2]string]UsagePrice, error) {
	manual := make(map[[2]string]UsagePrice)
	if raw := s.Setting(usagePricesSetting); raw != "" {
		var overrides []UsagePrice
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			return nil, err
		}
		for _, p := range overrides {
			p.Source = "manual"
			manual[priceKey(p.Provider, p.Model)] = p
		}
	}
	return manual, nil
}

func (s *Store) usagePrices(cached usageCatalog) (map[[2]string]UsagePrice, error) {
	prices := make(map[[2]string]UsagePrice)
	for _, p := range defaultUsagePrices {
		p.Source = "default"
		prices[priceKey(p.Provider, p.Model)] = p
	}
	rows, err := s.db.Query("SELECT DISTINCT provider, model FROM api_usage WHERE provider != 'local' AND model != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p UsagePrice
		if err := rows.Scan(&p.Provider, &p.Model); err != nil {
			return nil, err
		}
		k := priceKey(p.Provider, p.Model)
		if _, ok := prices[k]; !ok {
			prices[k] = p
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range cached.Prices {
		key := priceKey(p.Provider, p.Model)
		if base, found := prices[key]; found {
			if p.PerMinute == 0 && base.PerMinute > 0 {
				p.PerMinute, p.DurationBilling = base.PerMinute, base.DurationBilling
			}
			prices[key] = p
		}
	}
	manual, err := s.manualUsagePrices()
	if err != nil {
		return nil, err
	}
	for key, p := range manual {
		prices[key] = p
	}
	return prices, nil
}

func (s *Store) APIUsage(now time.Time) (UsageReport, error) {
	cached := s.usageCatalog()
	prices, err := s.usagePrices(cached)
	if err != nil {
		return UsageReport{}, err
	}
	report := UsageReport{Prices: make([]UsagePrice, 0, len(prices))}
	if !cached.Updated.IsZero() {
		report.CatalogUpdated = cached.Updated.Format(time.RFC3339)
	}
	for _, p := range prices {
		report.Prices = append(report.Prices, p)
	}
	sort.Slice(report.Prices, func(i, j int) bool {
		a, b := report.Prices[i], report.Prices[j]
		if a.Provider == b.Provider {
			return a.Model < b.Model
		}
		return a.Provider < b.Provider
	})
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	for _, period := range []struct {
		id    string
		start time.Time
	}{
		{"today", today}, {"week", today.AddDate(0, 0, -6)}, {"month", today.AddDate(0, 0, 1-d)},
	} {
		result, err := s.usagePeriod(period.id, period.start, now, prices)
		if err != nil {
			return UsageReport{}, err
		}
		report.Periods = append(report.Periods, result)
	}
	report.Projection, err = s.usageProjection(today, now, report.Periods[1], report.Periods[2])
	if err != nil {
		return UsageReport{}, err
	}
	return report, nil
}

func (s *Store) usagePeriod(id string, start, end time.Time, prices map[[2]string]UsagePrice) (UsagePeriod, error) {
	rows, err := s.db.Query(`SELECT provider,function,model,tokens_known,ok,COUNT(*),
		SUM(input_tokens),SUM(output_tokens),SUM(audio_input_tokens),SUM(audio_output_tokens),
		SUM(audio_seconds),SUM(characters)
		FROM api_usage WHERE at_ms BETWEEN ? AND ? GROUP BY provider,function,model,tokens_known,ok`, start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return UsagePeriod{}, err
	}
	defer rows.Close()
	period := UsagePeriod{ID: id}
	providers, functions, models := map[string]*UsageTotals{}, map[string]*UsageTotals{}, map[string]*UsageTotals{}
	for rows.Next() {
		var e usage.Entry
		var calls int64
		if err := rows.Scan(&e.Provider, &e.Function, &e.Model, &e.TokensKnown, &e.OK, &calls, &e.Input, &e.Output, &e.AudioInput, &e.AudioOutput, &e.AudioSeconds, &e.Characters); err != nil {
			return UsagePeriod{}, err
		}
		cost, known := estimateUsage(e, prices[priceKey(e.Provider, e.Model)])
		for _, total := range []*UsageTotals{&period.Totals, usageGroup(providers, e.Provider), usageGroup(functions, e.Function), usageGroup(models, e.Provider+" · "+e.Model)} {
			total.Calls += calls
			if !e.OK {
				total.Errors += calls
			}
			total.Input += e.Input
			total.Output += e.Output
			total.AudioSeconds += e.AudioSeconds
			total.Characters += e.Characters
			total.USD += cost
			if !known {
				total.Unpriced += calls
			}
		}
	}
	period.Providers = sortUsageGroups(providers)
	period.Functions = sortUsageGroups(functions)
	period.Models = sortUsageGroups(models)
	return period, rows.Err()
}

func usageGroup(groups map[string]*UsageTotals, key string) *UsageTotals {
	if groups[key] == nil {
		groups[key] = &UsageTotals{}
	}
	return groups[key]
}

func sortUsageGroups(groups map[string]*UsageTotals) []UsageGroup {
	out := make([]UsageGroup, 0, len(groups))
	for name, total := range groups {
		out = append(out, UsageGroup{name, *total})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func estimateUsage(e usage.Entry, p UsagePrice) (float64, bool) {
	switch {
	case e.Provider == "local":
		return 0, true
	case !p.Enabled:
		return 0, false
	case e.Model == "gemini-files":
		return 0, true
	case e.OK && p.PerMillionChars > 0 && e.Characters > 0:
		return float64(e.Characters) * p.PerMillionChars / 1e6, true
	case e.AudioSeconds > 0 && (p.DurationBilling || !e.TokensKnown) && p.PerMinute > 0:
		if !e.OK && !e.TokensKnown {
			return 0, false
		}
		return e.AudioSeconds * p.PerMinute / 60, true
	case e.TokensKnown:
		inputAudio, outputAudio := min(e.Input, e.AudioInput), min(e.Output, e.AudioOutput)
		return (float64(e.Input-inputAudio)*p.Input + float64(e.Output-outputAudio)*p.Output +
			float64(inputAudio)*p.AudioInput + float64(outputAudio)*p.AudioOutput) / 1e6, true
	default:
		return 0, false
	}
}
