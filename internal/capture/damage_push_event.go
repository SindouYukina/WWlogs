package capture

import (
	"regexp"
)

// abnormalColumn matches the AbnormalDamageConfig columns Abnormal<id> that name negative-status DamageIds.
var abnormalColumn = regexp.MustCompile(`^abnormal(\d+)$`)

// combatEventPushJSONL is what a server-executed DamageExecuteNotify adds to the common event fields of a development event
// line. The result itself went through the shared accounting (damage_notify.go); these fields only describe it.
type combatEventPushJSONL struct {
	Stream        string `json:"stream"`
	PairedWithRPC bool   `json:"pairedWithRpc"`
	// NotifyKey is the deduplication key of the result (notifyEventKey); zero when the frame had no sequence.
	NotifyKey      uint64 `json:"notifyKey"`
	NegativeStatus bool   `json:"negativeStatus"`
	// NegativeStatusLocal and NegativeStatusBasis are negativeStatusOwnership's decision for a negative-status result.
	NegativeStatusLocal bool   `json:"negativeStatusLocal,omitempty"`
	NegativeStatusBasis string `json:"negativeStatusBasis,omitempty"`
	DamageRaw           int32  `json:"damageRaw"`
	ChangeLifeRaw       int32  `json:"changeLifeRaw"`
	HasChangeLife       bool   `json:"hasChangeLife"`
}
