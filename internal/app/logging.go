package app

import "sonara/internal/logx"

// Short names for the app's log helpers. Function values, not wrappers, so the
// file:line in each entry still points at the caller.
var (
	logf     = logx.Logf
	logDedup = logx.Dedup
)
