package history

import "wwlogs/internal/capture"

// Level is the history level every build records at.
//
// AGENTS.md section 5.4: Combat History is shared product data with the same semantics in both build profiles, and
// normalized combat results are product-domain records that a local Combat History may store. Both profiles therefore keep
// the same encounters and the same accepted combat results. Only the fields of the normalized record are written
// (insertEvent); Development-only diagnostics such as raw packets, payloads, resolver proofs and handles never enter the
// history.
func Level() capture.HistoryLevel {
	return capture.HistoryEvents
}

// levelName is the stored name of a level.
func levelName(level capture.HistoryLevel) string {
	if level >= capture.HistoryEvents {
		return "events"
	}
	return "encounters"
}
