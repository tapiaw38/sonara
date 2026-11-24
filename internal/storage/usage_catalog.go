package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const usageCatalogSetting = "api_usage_price_catalog"

type usageCatalog struct {
	Updated time.Time    `json:"updated"`
	Prices  []UsagePrice `json:"prices"`
}

type catalogModel struct {
	Provider     string   `json:"litellm_provider"`
	Input        *float64 `json:"input_cost_per_token"`
	Output       *float64 `json:"output_cost_per_token"`
	AudioInput   *float64 `json:"input_cost_per_audio_token"`
	AudioOutput  *float64 `json:"output_cost_per_audio_token"`
	InputSecond  *float64 `json:"input_cost_per_second"`
	OutputSecond *float64 `json:"output_cost_per_second"`
	Character    *float64 `json:"input_cost_per_character"`
}

func (s *Store) usageCatalog() usageCatalog {
	var cached usageCatalog
	_ = json.Unmarshal([]byte(s.Setting(usageCatalogSetting)), &cached)
	return cached
}

// RefreshUsageCatalog updates the cache when due. It sends only a public GET
// and keeps the last complete cache when the source is unavailable.
func (s *Store) RefreshUsageCatalog(ctx context.Context, client *http.Client, endpoint string, now time.Time) error {
	if cached := s.usageCatalog(); !cached.Updated.IsZero() && now.Sub(cached.Updated) < 24*time.Hour {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catálogo de tarifas: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	var rows map[string]catalogModel
	if err := json.Unmarshal(body, &rows); err != nil {
		return err
	}
	cached := usageCatalog{Updated: now.UTC()}
	for id, row := range rows {
		switch row.Provider {
		case "openai", "deepseek", "gemini":
		default:
			continue
		}
		model := strings.TrimPrefix(id, row.Provider+"/")
		if model == "" || strings.Contains(model, "/") {
			continue
		}
		p := UsagePrice{Provider: row.Provider, Model: model, Enabled: true, Source: "catalog"}
		known := false
		for _, rate := range []struct {
			src   *float64
			dst   *float64
			scale float64
		}{
			{row.Input, &p.Input, 1e6}, {row.Output, &p.Output, 1e6},
			{row.AudioInput, &p.AudioInput, 1e6}, {row.AudioOutput, &p.AudioOutput, 1e6},
			{row.Character, &p.PerMillionChars, 1e6},
		} {
			if rate.src != nil && *rate.src >= 0 {
				*rate.dst = *rate.src * rate.scale
				known = true
			}
		}
		if row.InputSecond != nil || row.OutputSecond != nil {
			for _, rate := range []*float64{row.InputSecond, row.OutputSecond} {
				if rate != nil && *rate >= 0 {
					p.PerMinute += *rate * 60
					known = true
				}
			}
			p.DurationBilling = p.PerMinute > 0
		}
		if known {
			cached.Prices = append(cached.Prices, p)
		}
	}
	if len(cached.Prices) == 0 {
		return fmt.Errorf("el catálogo no contiene tarifas compatibles")
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	return s.SetSetting(usageCatalogSetting, string(data))
}
