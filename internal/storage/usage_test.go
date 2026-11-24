package storage

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
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

func TestUsageCatalogRefreshesDailyAndPreservesManualOverride(t *testing.T) {
	db, err := OpenAt(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" {
			t.Error("expected public GET")
		}
		if calls > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"gpt-6-luna":{"litellm_provider":"openai","input_cost_per_token":0.000001,"output_cost_per_token":0.000002},"azure/unknown":{"litellm_provider":"azure","input_cost_per_token":0.000001}}`)
	}))
	defer server.Close()
	refresh := func(at time.Time) error {
		return db.RefreshUsageCatalog(context.Background(), server.Client(), server.URL, at)
	}
	if err := refresh(now); err != nil {
		t.Fatal(err)
	}
	if err := refresh(now.Add(time.Hour)); err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	k := priceKey("openai", "gpt-6-luna")
	if err := db.SaveUsagePrices([]UsagePrice{{Provider: k[0], Model: k[1], Enabled: true, Input: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := refresh(now.Add(25 * time.Hour)); err == nil {
		t.Fatal("expected failed refresh")
	}
	prices, err := db.usagePrices(db.usageCatalog())
	if err != nil || prices[k].Input != 9 || prices[k].Source != "manual" {
		t.Fatalf("manual: %+v %v", prices[k], err)
	}
	if err := db.SaveUsagePrices([]UsagePrice{{Provider: k[0], Model: k[1], Source: "reset"}}); err != nil {
		t.Fatal(err)
	}
	prices, err = db.usagePrices(db.usageCatalog())
	if err != nil || prices[k].Input != 1 || prices[k].Output != 2 || prices[k].Source != "catalog" {
		t.Fatalf("catalog: %+v %v", prices[k], err)
	}
}
