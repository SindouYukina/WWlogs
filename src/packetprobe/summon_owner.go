package main

// summonOwnerSchemaVersion identifies the layout of summonOwnerRecord.
const summonOwnerSchemaVersion = 1

// summonOwnerRecord is one ownership or dependence fact from an entity component.
// Kind is one of "summoner" (SummonerComponentPb on the entity), "concomitant"
// (ConcomitantsComponentPb.VisionEntityIds and CustomEntityIds on the entity), or
// "dependence" (DependenceComponentPb.DependenceEntityIds on the entity). The
// facts are copied as observed. Role and summon identity are not inferred here.
type summonOwnerRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	FrameOrdinal  uint64 `json:"frameOrdinal"`
	SessionNumber int    `json:"sessionNumber"`
	Timestamp     string `json:"timestamp"`
	Command       string `json:"command"`
	Kind          string `json:"kind"`
	EntityID      uint64 `json:"entityId"`
	RelatedID     *int64 `json:"relatedEntityId,omitempty"`
	SummonerID    *int64 `json:"summonerId,omitempty"`
	SummonCfgID   *int64 `json:"summonCfgId,omitempty"`
	SummonSkillID *int64 `json:"summonSkillId,omitempty"`
	PlayerID      *int64 `json:"playerId,omitempty"`
	SummonType    string `json:"summonType,omitempty"`
	SummonTypeRaw *int64 `json:"summonTypeRaw,omitempty"`
	Evidence      string `json:"evidence"`
}
