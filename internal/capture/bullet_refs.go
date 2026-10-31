package capture

const bulletRefSchemaVersion = 1

// bulletRefRecord is one BulletId carried by an outbound combat message, with the
// static actor-chain translation of that BulletId for the entity that sent it.
type bulletRefRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	FrameOrdinal  uint64 `json:"frameOrdinal"`
	SessionNumber int    `json:"sessionNumber"`
	Timestamp     string `json:"timestamp"`
	Direction     string `json:"direction"`
	FieldPath     string `json:"fieldPath"`
	BulletID      int64  `json:"bulletId"`
	// HandleID and ResolvedBulletID are set when the BulletId field actually holds an ActiveBulletHandle.HandleId.
	HandleID         *int64           `json:"handleId,omitempty"`
	ResolvedBulletID *int64           `json:"resolvedBulletId,omitempty"`
	SourceEntityID   *int64           `json:"sourceEntityId,omitempty"`
	SourceBasis      string           `json:"sourceBasis,omitempty"`
	SkillID          *int64           `json:"skillId,omitempty"`
	DtType           *int64           `json:"dtType,omitempty"`
	Status           resolutionStatus `json:"status"`
	Namespace        string           `json:"namespace,omitempty"`
	ActorBlueprint   string           `json:"actorBlueprint,omitempty"`
	Via              string           `json:"via,omitempty"`
	DevLabelZhCn     string           `json:"devLabelZhCn,omitempty"`
	// DevLabel is the localized developer note of the bullet row; never an official name.
	DevLabel *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
	Evidence string                     `json:"evidence"`
}

const maxBulletHandles = 1 << 16

// bulletHandleKey scopes a bullet handle id to the network session, since handle ids restart.
type bulletHandleKey struct {
	session  int
	handleID int64
}

// bulletHandleEntry remembers the bullet a CreateBullet message created under a handle id.
type bulletHandleEntry struct {
	bulletID  int64
	skillID   int64
	hasSkill  bool
	status    resolutionStatus
	namespace string
	actor     string
	via       string
	label     string
}

// bulletSourceCandidate names where the firing entity id is read from.
type bulletSourceCandidate struct {
	basis string
	id    int64
}
