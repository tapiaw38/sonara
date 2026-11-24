package storage

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"sonara/backend/usage"
)

func TestUsageProjectionCalendarWindows(t *testing.T) {
	now := time.Date(2028, 2, 10, 12, 0, 0, 0, time.FixedZone("local", -3*3600))
	for _, tc := range []struct {
		name                string
		offsets             []int
		observed            int
		week, thirty, month float64
	}{
		{"empty", nil, 0, 0, 0, 0},
		{"one observed day with two calls", []int{0, 0}, 1, 14, 60, 5.8},
		{"three days with gaps", []int{0, 2, 6}, 3, 7, 30, 8.7},
		{"older calls only affect month", []int{0, 1, 2, 3, 4, 5, 6, 7}, 7, 7, 30, 23.2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := OpenAt(filepath.Join(t.TempDir(), "usage.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for i, offset := range tc.offsets {
				provider, model := "openai", "gpt-6-luna"
				if i%2 == 1 {
					provider, model = "azure", "chat"
				}
				err := db.RecordUsage(usage.Entry{At: now.AddDate(0, 0, -offset), Provider: provider, Model: model, Input: 1000000, TokensKnown: true, OK: true})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := db.SaveUsagePrices([]UsagePrice{{Provider: "openai", Model: "gpt-6-luna", Enabled: true, Input: 1}, {Provider: "azure", Model: "chat", Enabled: true, Input: 1}}); err != nil {
				t.Fatal(err)
			}
			report, err := db.APIUsage(now)
			if err != nil {
				t.Fatal(err)
			}
			got := report.Projection
			if got.ObservedDays != tc.observed || got.ElapsedDays != 10 || got.MonthDays != 29 || got.MonthHasData != (len(tc.offsets) > 0) {
				t.Fatalf("unexpected window: %+v", got)
			}
			var sum usage.Projection
			for _, row := range got.Providers {
				sum.WeekUSD += row.Cost.WeekUSD
				sum.ThirtyDayUSD += row.Cost.ThirtyDayUSD
				sum.MonthPaceUSD += row.Cost.MonthPaceUSD
			}
			for _, projection := range []usage.Projection{got.Total, sum} {
				if math.Abs(projection.WeekUSD-tc.week) > 1e-9 || math.Abs(projection.ThirtyDayUSD-tc.thirty) > 1e-9 || math.Abs(projection.MonthPaceUSD-tc.month) > 1e-9 {
					t.Fatalf("unexpected costs: %+v", projection)
				}
			}
		})
	}
}
