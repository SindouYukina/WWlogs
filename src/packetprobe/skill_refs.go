package main

const skillRefSchemaVersion = 1

// skillRefRecord is one SkillId carried by a combat message with its namespace check: whether the
// SkillId is a skill the firing entity's actor lists in EntitySkillPreload.
type skillRefRecord struct {
	SchemaVersion  int              `json:"schemaVersion"`
	FrameOrdinal   uint64           `json:"frameOrdinal"`
	SessionNumber  int              `json:"sessionNumber"`
	Timestamp      string           `json:"timestamp"`
	Direction      string           `json:"direction"`
	FieldPath      string           `json:"fieldPath"`
	Holder         string           `json:"holder"`
	SkillID        int64            `json:"skillId"`
	SourceEntityID *int64           `json:"sourceEntityId,omitempty"`
	SourceBasis    string           `json:"sourceBasis,omitempty"`
	Status         resolutionStatus `json:"status"`
	Namespace      string           `json:"namespace,omitempty"`
	ActorBlueprint string           `json:"actorBlueprint,omitempty"`
	DevLabelZhCn   string           `json:"devLabelZhCn,omitempty"`
	Evidence       string           `json:"evidence"`
}
