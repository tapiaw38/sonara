package storage

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"sonara/backend/usage"
)

func TestUsageReportsCalendarPeriodsAndEditedPrices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	db, err := OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 18, 13, 0, 0, 0, time.FixedZone("local", -3*3600))
	entries := []usage.Entry{
		{At: now, Provider: "openai", Model: "gpt-6-luna", Function: usage.Copilot, Input: 1000, Output: 200, TokensKnown: true, OK: true},
		{At: now, Provider: "local", Model: "whisper-local", Function: usage.Meeting, AudioSeconds: 90, OK: true},
		{At: now, Provider: "azure", Model: "speech-deployment", Function: usage.TTS, Characters: 200, OK: true},
		{At: now.AddDate(0, 0, -3), Provider: "openai", Model: "gpt-transcribe", Function: usage.Meeting, AudioSeconds: 60, OK: true},
		{At: now.AddDate(0, 0, -10), Provider: "openai", Model: "gpt-6-luna", Function: usage.Summary},
		{At: now.AddDate(0, -1, 0), Provider: "local", Model: "whisper-local", Function: usage.Meeting, OK: true},
	}
	for _, entry := range entries {
		if err := db.RecordUsage(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SaveUsagePrices([]UsagePrice{{Provider: "openai", Model: "gpt-6-luna", Enabled: true, Input: 2, Output: 10}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenAt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	report, err := db.APIUsage(now)
	if err != nil {
		t.Fatal(err)
	}
	today, week, month := report.Periods[0], report.Periods[1], report.Periods[2]
	if today.Totals.Calls != 3 || today.Totals.Input != 1000 || today.Totals.Output != 200 ||
		today.Totals.AudioSeconds != 90 || today.Totals.Characters != 200 || today.Totals.Unpriced != 1 ||
		math.Abs(today.Totals.USD-.004) > 1e-9 || len(today.Providers) != 3 || len(today.Functions) != 3 || len(today.Models) != 3 {
		t.Fatalf("today: %+v", today)
	}
	if week.Totals.Calls != 4 || math.Abs(week.Totals.USD-.0085) > 1e-9 ||
		month.Totals.Calls != 5 || month.Totals.Errors != 1 {
		t.Fatalf("week / month: %+v / %+v", week.Totals, month.Totals)
	}
}

func TestUsageRealtimePricesByTokenModality(t *testing.T) {
	entry := usage.Entry{Provider: "openai", Input: 100, Output: 100, AudioInput: 80, AudioOutput: 90, TokensKnown: true, OK: true}
	price := UsagePrice{Enabled: true, Input: .6, Output: 2.4, AudioInput: 10, AudioOutput: 20}
	got, ok := estimateUsage(entry, price)
	if !ok || math.Abs(got-.002636) > 1e-9 {
		t.Fatalf("estimate = %f, %v", got, ok)
	}
}
