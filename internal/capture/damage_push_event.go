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
	// Credit* describe the role credit of a local negative-status result (negative_status_credit.go): the settlement
	// (forced, own, applied or undetermined), how many entities the evidence leaves as its cause, the basis
	// (negative_status_trigger or negative_status_owner), the credited role, the executing status Buff, the InvokePeriod
	// that forced it, the instance owner, and why there is no credit.
	Settlement          string `json:"settlement,omitempty"`
	CreditCandidates    int    `json:"creditCandidates,omitempty"`
	CreditBasis         string `json:"creditBasis,omitempty"`
	CreditRoleID        uint64 `json:"creditRoleId,omitempty"`
	CreditEntityID      uint64 `json:"creditEntityId,omitempty"`
	StatusEffectID      uint64 `json:"statusEffectId,omitempty"`
	TriggerEffectID     uint64 `json:"triggerEffectId,omitempty"`
	StatusOwnerEntityID uint64 `json:"statusOwnerEntityId,omitempty"`
	CreditReason        string `json:"creditReason,omitempty"`
	DamageRaw           int32  `json:"damageRaw"`
	ChangeLifeRaw       int32  `json:"changeLifeRaw"`
	HasChangeLife       bool   `json:"hasChangeLife"`
}
