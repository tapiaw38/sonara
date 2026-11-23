package diarize

import (
	"fmt"
	"sort"
)

// OverlapMS is how much of [startMS, endMS) a span covers — zero when they
// don't overlap at all.
func OverlapMS(spanStart, spanEnd, startMS, endMS int64) int64 {
	from, to := max(spanStart, startMS), min(spanEnd, endMS)
	if to <= from {
		return 0
	}
	return to - from
}

// OverlapSpeaker returns the raw speaker label covering the most time inside
// [startMS, endMS) — a segment is attributed to whoever spoke the most during
// it, the same block-level granularity transcription already uses.
func OverlapSpeaker(spans []Span, startMS, endMS int64) string {
	totals := map[string]int64{}
	best, bestMS := "", int64(0)
	for _, span := range spans {
		totals[span.Speaker] += OverlapMS(span.StartMS, span.EndMS, startMS, endMS)
		if totals[span.Speaker] > bestMS {
			best, bestMS = span.Speaker, totals[span.Speaker]
		}
	}
	return best
}

// StableKeys renumbers one diarization call's raw labels ("Speaker 1",
// whatever the model used) into "spk_0", "spk_1"… ordered by first appearance,
// so storage and display never depend on the model's own naming.
func StableKeys(spans []Span) map[string]string {
	sorted := append([]Span(nil), spans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].StartMS < sorted[j].StartMS })
	keys := map[string]string{}
	for _, span := range sorted {
		if _, seen := keys[span.Speaker]; !seen {
			keys[span.Speaker] = fmt.Sprintf("spk_%d", len(keys))
		}
	}
	return keys
}

// Diarization over-splits: one voice at a new distance or pitch becomes a new
// "speaker". Like the minimum-speaker-duration step in Microsoft's VoxCeleb
// 2020 system, a label with too little total speech is not a person: its spans
// go to whichever real speaker talked nearest in time.
const (
	minSpeakerMS    = 3000
	minSpeakerShare = 0.05
)

func MergeMinorSpeakers(spans []Span) []Span {
	totals := map[string]int64{}
	var all int64
	for _, span := range spans {
		totals[span.Speaker] += span.EndMS - span.StartMS
		all += span.EndMS - span.StartMS
	}
	limit := max(int64(minSpeakerMS), int64(float64(all)*minSpeakerShare))
	major := func(speaker string) bool { return totals[speaker] >= limit }
	hasMajor := false
	for speaker := range totals {
		hasMajor = hasMajor || major(speaker)
	}
	if !hasMajor {
		return spans // nobody spoke long enough to judge: keep what was found
	}
	merged := make([]Span, 0, len(spans))
	for _, span := range spans {
		if !major(span.Speaker) {
			best, bestGap := "", int64(-1)
			for _, other := range spans {
				if !major(other.Speaker) {
					continue
				}
				gap := max(other.StartMS-span.EndMS, span.StartMS-other.EndMS, 0)
				if bestGap < 0 || gap < bestGap {
					best, bestGap = other.Speaker, gap
				}
			}
			span.Speaker = best
		}
		merged = append(merged, span)
	}
	return merged
}

const (
	// echoOverlapFraction: a segment counts as an echo once this much of its
	// own duration is covered by a detected echo span. Picked as a sane
	// starting point (more than half), not calibrated against real audio.
	// ponytail: raise this if real meetings show false positives (another
	// participant with a similar voice wrongly cleared); lower it if real
	// echo is only partially caught.
	echoOverlapFraction = 0.5
)

// IsEcho reports whether most of [startMS, endMS) is covered by echo spans.
func IsEcho(spans []EchoSpan, startMS, endMS int64) bool {
	duration := endMS - startMS
	if duration <= 0 || len(spans) == 0 {
		return false
	}
	var covered int64
	for _, span := range spans {
		covered += OverlapMS(span.StartMS, span.EndMS, startMS, endMS)
	}
	return float64(covered)/float64(duration) >= echoOverlapFraction
}
