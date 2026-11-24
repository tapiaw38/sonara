package usage

// Projection extrapolates estimated USD costs using the same time windows
// for the total and every provider.
type Projection struct {
	WeekUSD      float64 `json:"weekUSD"`
	ThirtyDayUSD float64 `json:"thirtyDayUSD"`
	MonthPaceUSD float64 `json:"monthPaceUSD"`
}

func ProjectCost(weekUSD, monthUSD float64, observedDays, elapsedDays, monthDays int) Projection {
	var result Projection
	if observedDays > 0 {
		daily := weekUSD / float64(observedDays)
		result.WeekUSD = daily * 7
		result.ThirtyDayUSD = daily * 30
	}
	if elapsedDays > 0 {
		result.MonthPaceUSD = monthUSD / float64(elapsedDays) * float64(monthDays)
	}
	return result
}
