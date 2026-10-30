package main

import (
	"regexp"
)

// pushCategoryAbnormal marks pushed damage whose DamageId is named by an AbnormalDamageConfig column.
const (
	pushCategoryAbnormal     = "ABNORMAL_STATE_DAMAGE"
	pushCategoryUnclassified = "UNCLASSIFIED_PUSH"
)

var abnormalColumn = regexp.MustCompile(`^abnormal(\d+)$`)

// combatEventPushJSONL is what a pushed DamageExecuteNotify adds to the common event fields.
type combatEventPushJSONL struct {
	Stream           string           `json:"stream"`
	PairedWithRPC    bool             `json:"pairedWithRpc"`
	Category         string           `json:"category"`
	CategoryStatus   resolutionStatus `json:"categoryStatus"`
	CategoryEvidence string           `json:"categoryEvidence"`
	ProtocolElement  int64            `json:"protocolElementType"`
	ElementName      string           `json:"elementName,omitempty"`
	PartID           int64            `json:"partId"`
	IsCrit           bool             `json:"isCrit"`
	KilledTarget     bool             `json:"killedTarget"`
	DamageRaw        int64            `json:"damageRaw"`
	ChangeLifeRaw    int64            `json:"changeLifeRaw"`
	Attribution      string           `json:"attribution"`
}
