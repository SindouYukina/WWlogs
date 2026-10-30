package main

// damagePushSchemaVersion identifies the layout of damagePushRecord.
const damagePushSchemaVersion = 1

// damagePushRecord is one DamageExecuteNotify pushed inside a combat notify. It is
// a separate damage stream from the damage RPC: the push carries its own attacker
// and target identifiers and is not paired with a request. Attacker identity is
// reported as observed; no character or summoner attribution is inferred.
type damagePushRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	FrameOrdinal  uint64 `json:"frameOrdinal"`
	SessionNumber int    `json:"sessionNumber"`
	Timestamp     string `json:"timestamp"`
	DamageID      int64  `json:"damageId"`
	AttackerID    int64  `json:"attackerEntityId"`
	TargetID      int64  `json:"targetEntityId"`
	Damage        int64  `json:"damage"`
	ChangeLife    int64  `json:"changeLife"`
	ElementType   int64  `json:"elementType"`
	PartID        int64  `json:"partId"`
	IsCrit        bool   `json:"isCrit"`
	KilledTarget  bool   `json:"killedTarget"`
	Stream        string `json:"stream"`
	Evidence      string `json:"evidence"`
	// ElementName names the protocol ElementType in the master locale; DamageElement and ClassCandidate come from the DamageId row.
	ElementName    string                   `json:"elementName,omitempty"`
	DamageElement  *combatEventElementJSONL `json:"damageElement,omitempty"`
	ClassCandidate string                   `json:"classCandidate,omitempty"`
	ClassStatus    resolutionStatus         `json:"classStatus,omitempty"`
	CalculateType  *int32                   `json:"calculateType,omitempty"`
}
