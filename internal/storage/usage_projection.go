package storage

import (
	"sort"
	"time"

	"sonara/backend/usage"
)

type UsageProjection struct {
	ObservedDays int                  `json:"observedDays"`
	ElapsedDays  int                  `json:"elapsedDays"`
	MonthDays    int                  `json:"monthDays"`
	MonthHasData bool                 `json:"monthHasData"`
	Total        usage.Projection     `json:"total"`
	Providers    []ProviderProjection `json:"providers"`
}

type ProviderProjection struct {
	Name     string           `json:"name"`
	Cost     usage.Projection `json:"cost"`
	Unpriced bool             `json:"unpriced"`
}

func (s *Store) usageProjection(today, now time.Time, week, month UsagePeriod) (UsageProjection, error) {
	result := UsageProjection{
		ElapsedDays:  today.Day(),
		MonthDays:    today.AddDate(0, 1, -today.Day()).Day(),
		MonthHasData: month.Totals.Calls > 0,
		Providers:    []ProviderProjection{},
	}
	for day := today.AddDate(0, 0, -6); !day.After(today); day = day.AddDate(0, 0, 1) {
		var used int
		end := min(now.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()-1)
		err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM api_usage WHERE at_ms BETWEEN ? AND ?)`, day.UnixMilli(), end).Scan(&used)
		if err != nil {
			return UsageProjection{}, err
		}
		result.ObservedDays += used
	}
	project := func(weekUSD, monthUSD float64) usage.Projection {
		return usage.ProjectCost(weekUSD, monthUSD, result.ObservedDays, result.ElapsedDays, result.MonthDays)
	}
	result.Total = project(week.Totals.USD, month.Totals.USD)
	weekly, monthly := map[string]UsageTotals{}, map[string]UsageTotals{}
	for _, group := range week.Providers {
		weekly[group.Name] = group.Totals
	}
	for _, group := range month.Providers {
		monthly[group.Name] = group.Totals
		if _, exists := weekly[group.Name]; !exists {
			weekly[group.Name] = UsageTotals{}
		}
	}
	for name, w := range weekly {
		m := monthly[name]
		result.Providers = append(result.Providers, ProviderProjection{
			Name: name, Cost: project(w.USD, m.USD), Unpriced: w.Unpriced+m.Unpriced > 0,
		})
	}
	sort.Slice(result.Providers, func(i, j int) bool { return result.Providers[i].Name < result.Providers[j].Name })
	return result, nil
}
