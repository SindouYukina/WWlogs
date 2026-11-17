package capture

import "time"

// CombatEventKind is the normalized category of one server-confirmed combat result.
type CombatEventKind string

const (
	CombatEventDamage  CombatEventKind = "damage"
	CombatEventHealing CombatEventKind = "healing"
	CombatEventOther   CombatEventKind = "other"
)

// CombatFlow classifies an event by the player relation of its two endpoints. It is the only input the
// statistics and encounter consumers use to decide where an event belongs; they never re-derive it.
type CombatFlow string

const (
	// CombatFlowLocalOutgoing is a local-team source hitting a confirmed non-player target.
	CombatFlowLocalOutgoing CombatFlow = "local_outgoing"
	// CombatFlowLocalIncoming is a confirmed non-player source hitting a local-team target.
	CombatFlowLocalIncoming CombatFlow = "local_incoming"
	// CombatFlowUnattributedOutgoing has an unresolved endpoint on a hit aimed away from the local team.
	CombatFlowUnattributedOutgoing CombatFlow = "unattributed_outgoing"
	// CombatFlowUnattributedIncoming has an unresolved endpoint on a hit aimed at the local team.
	CombatFlowUnattributedIncoming CombatFlow = "unattributed_incoming"
	// CombatFlowLocalInternal has local-team entities on both ends.
	CombatFlowLocalInternal CombatFlow = "local_internal"
	// CombatFlowExcluded is proven unrelated on both ends, such as monster versus monster.
	CombatFlowExcluded CombatFlow = "excluded"
	// CombatFlowUnresolved has no resolved relation on either end.
	CombatFlowUnresolved CombatFlow = "unresolved"
)

// AttributionConfidence states how strongly an event is tied to the local team.
type AttributionConfidence string

const (
	AttributionConfirmed    AttributionConfidence = "confirmed"
	AttributionUnattributed AttributionConfidence = "unattributed"
	AttributionNotLocal     AttributionConfidence = "not_local"
)

// combatEvidenceDamageRPCResponse marks a record built from a paired DamageExecute request and response.
const combatEvidenceDamageRPCResponse = "damage_rpc_response"

// CombatEventKey identifies one server response inside a statistics owner. The RPC layer already drops repeated
// responses; the key lets the statistics layer stay idempotent if the same response is delivered twice. RPCID is the
// request's RequestId continued past each wrap of the client's RequestId counter (extendedRequestID), so two requests that
// shared a RequestId in a long session keep two keys; until the first wrap it is the RequestId itself.
type CombatEventKey struct {
	ProtocolSession int    `json:"protocolSession"`
	RPCID           uint64 `json:"rpcId"`
	// Notify identifies a server-executed DamageExecuteNotify instead (notifyEventKey: the server frame's sequence and the
	// result's position in it); RPCID is then zero. The two key spaces never meet.
	Notify uint64 `json:"notify,omitempty"`
}

// hasIdentity reports whether the key names a real response or notification. Events without one are never deduplicated.
func (k CombatEventKey) hasIdentity() bool {
	return k.RPCID != 0 || k.Notify != 0
}

// CombatEndpoint is one side of an event with the relation established for it at event time.
type CombatEndpoint struct {
	EntityID uint64 `json:"entityId"`
	// EntityGeneration counts observed ID reuse after removal, so a reused EntityId is not merged with its predecessor.
	EntityGeneration uint64 `json:"entityGeneration"`
	Relation         string `json:"relation"`
}

// CombatEventRecord is the normalized form of one server-confirmed result. Raw wire values and identity
// evidence stay on the decoder's event; this record keeps only what downstream statistics need.
type CombatEventRecord struct {
	Kind CombatEventKind `json:"kind"`
	Key  CombatEventKey  `json:"key"`
	// ObservedAt is the capture time of the frame that carried the response, not a server event time.
	ObservedAt time.Time             `json:"observedAt"`
	Evidence   string                `json:"evidence"`
	Source     CombatEndpoint        `json:"source"`
	Target     CombatEndpoint        `json:"target"`
	Flow       CombatFlow            `json:"flow"`
	Confidence AttributionConfidence `json:"confidence"`
	Amount     int64                 `json:"amount"`
	DamageID   uint64                `json:"damageId,omitempty"`
	SkillID    uint64                `json:"skillId,omitempty"`
	// ContextDowngrade is set when a local flow was kept unattributed because local-player attribution was suspect
	// after a transport loss; Flow and Confidence then hold the unattributed classification.
	ContextDowngrade string `json:"contextDowngrade,omitempty"`
	// VehicleKind classifies a local vehicle contribution without changing source or role evidence.
	VehicleKind string `json:"vehicleKind,omitempty"`
	// EchoVisions are the SummonCfg ids of the ConcomitantVision entities the attacker held. They only help name a skill.
	EchoVisions []uint64 `json:"-"`
	// SourceRole and TargetRole preserve runtime role evidence independently of contributor classification.
	SourceRole *CombatRole `json:"sourceRole,omitempty"`
	TargetRole *CombatRole `json:"targetRole,omitempty"`
	// AppliedHealing is, on a healing record, the HP the server applied to the target: the response's ChangeLife
	// negated. An absent ChangeLife is the proto3 zero, so none was applied; whether the target was at full HP is known
	// only where Life was also reported (see healing.go). Amount stays the reported magnitude. HealingInconsistent marks
	// a healing result whose ChangeLife lowered HP; it applies nothing.
	AppliedHealing      int64 `json:"appliedHealing,omitempty"`
	HealingInconsistent bool  `json:"healingInconsistent,omitempty"`
	// SourceActor and TargetActor are the static identity evidence of each endpoint at event time (history_journal.go).
	// They only group enemy instances for Combat History and never change a statistic.
	SourceActor *CombatActorType `json:"-"`
	TargetActor *CombatActorType `json:"-"`
	// historySequence numbers the record in the Combat History journal once the statistics accepted it; zero otherwise.
	historySequence uint64
}

// HistorySequence is the record's place in its capture session's Combat History journal; zero when it was not journaled.
func (r CombatEventRecord) HistorySequence() uint64 { return r.historySequence }

func (r PlayerRelation) String() string {
	switch r {
	case PlayerRelationLocal:
		return "LOCAL"
	case PlayerRelationUnrelated:
		return "UNRELATED"
	default:
		return "UNKNOWN"
	}
}

// classifyCombatFlow maps the endpoint relations to a flow. The mapping preserves the v0.3 statistics rules:
// only LOCAL->UNRELATED and UNRELATED->LOCAL are confirmed; any UNKNOWN endpoint stays isolated.
func classifyCombatFlow(source, target PlayerRelation) CombatFlow {
	switch {
	case source == PlayerRelationLocal && target == PlayerRelationUnrelated:
		return CombatFlowLocalOutgoing
	case source == PlayerRelationUnrelated && target == PlayerRelationLocal:
		return CombatFlowLocalIncoming
	case source == PlayerRelationLocal && target == PlayerRelationUnknown:
		return CombatFlowUnattributedOutgoing
	case source == PlayerRelationUnknown && target == PlayerRelationLocal:
		return CombatFlowUnattributedIncoming
	case source == PlayerRelationUnknown && target == PlayerRelationUnrelated:
		return CombatFlowUnattributedOutgoing
	case source == PlayerRelationUnrelated && target == PlayerRelationUnknown:
		return CombatFlowUnattributedIncoming
	case source == PlayerRelationLocal && target == PlayerRelationLocal:
		return CombatFlowLocalInternal
	case source == PlayerRelationUnrelated && target == PlayerRelationUnrelated:
		return CombatFlowExcluded
	default:
		return CombatFlowUnresolved
	}
}

func (f CombatFlow) confidence() AttributionConfidence {
	switch f {
	case CombatFlowLocalOutgoing, CombatFlowLocalIncoming:
		return AttributionConfirmed
	case CombatFlowUnattributedOutgoing, CombatFlowUnattributedIncoming, CombatFlowUnresolved:
		return AttributionUnattributed
	default:
		return AttributionNotLocal
	}
}

// normalizeCombatEvent builds the statistics record for a decoded damage-RPC result or a notified result
// (damage_notify.go). It returns false for results that cannot enter statistics: a non-positive amount or a missing
// endpoint, which keeps the v0.3 admission rule.
func normalizeCombatEvent(event CombatEvent, source, target PlayerRelation) (CombatEventRecord, bool) {
	if event.Damage <= 0 || !event.HasSourceEntity || event.SourceEntityID == 0 || !event.HasTargetEntity || event.TargetEntityID == 0 {
		return CombatEventRecord{}, false
	}
	key, evidence := CombatEventKey{ProtocolSession: event.SessionNumber, RPCID: extendedRequestID(event.RPCID, event.RequestEpoch)}, combatEvidenceDamageRPCResponse
	if event.Notify != nil {
		key, evidence = CombatEventKey{ProtocolSession: event.SessionNumber, Notify: event.Notify.Key}, combatEvidenceDamageExecuteNotify
		if event.Notify.NegativeStatusLocal {
			evidence = combatEvidenceNegativeStatusNotify
		}
	}
	kind := CombatEventOther
	switch event.EventTypeCandidate {
	case "DAMAGE_CANDIDATE":
		kind = CombatEventDamage
	case "HEALING_CANDIDATE":
		kind = CombatEventHealing
	}
	flow := classifyCombatFlow(source, target)
	record := CombatEventRecord{
		Kind:       kind,
		Key:        key,
		ObservedAt: event.Timestamp,
		Evidence:   evidence,
		Source: CombatEndpoint{
			EntityID: event.SourceEntityID, EntityGeneration: event.Source.Lifecycle.ReusedAfterRemove, Relation: source.String(),
		},
		Target: CombatEndpoint{
			EntityID: event.TargetEntityID, EntityGeneration: event.Target.Lifecycle.ReusedAfterRemove, Relation: target.String(),
		},
		Flow:       flow,
		Confidence: flow.confidence(),
		Amount:     event.Damage,
		DamageID:   event.DamageID,
		SkillID:    event.SkillID,

		EchoVisions: event.SourceVisionSummonCfgs,
	}
	if kind == CombatEventHealing && event.HasChangeLife {
		switch {
		case event.ChangeLifeRaw < 0:
			record.AppliedHealing = -int64(event.ChangeLifeRaw)
		case event.ChangeLifeRaw > 0:
			record.HealingInconsistent = true
		}
	}
	return record, true
}
