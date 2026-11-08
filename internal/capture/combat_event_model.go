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
// responses; the key lets the statistics layer stay idempotent if the same response is delivered twice.
type CombatEventKey struct {
	ProtocolSession int    `json:"protocolSession"`
	RPCID           uint64 `json:"rpcId"`
}

// hasIdentity reports whether the key names a real response. Events without an RPC id are never deduplicated.
func (k CombatEventKey) hasIdentity() bool {
	return k.RPCID != 0
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
	// SourceRole and TargetRole preserve runtime role evidence independently of contributor classification.
	SourceRole *CombatRole `json:"sourceRole,omitempty"`
	TargetRole *CombatRole `json:"targetRole,omitempty"`
	// AppliedHealing is, on a healing record, the HP the server applied to the target: the response's ChangeLife
	// negated. An absent ChangeLife is the proto3 zero: the target was already at full HP and the whole Amount was
	// overhealing. Amount stays the reported magnitude. HealingInconsistent marks a healing result whose ChangeLife
	// lowered HP; it applies nothing. Both were verified against the server's Life reports (healing-statistics research).
	AppliedHealing      int64 `json:"appliedHealing,omitempty"`
	HealingInconsistent bool  `json:"healingInconsistent,omitempty"`
}

func (r playerRelation) String() string {
	switch r {
	case playerRelationLocal:
		return "LOCAL"
	case playerRelationUnrelated:
		return "UNRELATED"
	default:
		return "UNKNOWN"
	}
}

// classifyCombatFlow maps the endpoint relations to a flow. The mapping preserves the v0.3 statistics rules:
// only LOCAL->UNRELATED and UNRELATED->LOCAL are confirmed; any UNKNOWN endpoint stays isolated.
func classifyCombatFlow(source, target playerRelation) CombatFlow {
	switch {
	case source == playerRelationLocal && target == playerRelationUnrelated:
		return CombatFlowLocalOutgoing
	case source == playerRelationUnrelated && target == playerRelationLocal:
		return CombatFlowLocalIncoming
	case source == playerRelationLocal && target == playerRelationUnknown:
		return CombatFlowUnattributedOutgoing
	case source == playerRelationUnknown && target == playerRelationLocal:
		return CombatFlowUnattributedIncoming
	case source == playerRelationUnknown && target == playerRelationUnrelated:
		return CombatFlowUnattributedOutgoing
	case source == playerRelationUnrelated && target == playerRelationUnknown:
		return CombatFlowUnattributedIncoming
	case source == playerRelationLocal && target == playerRelationLocal:
		return CombatFlowLocalInternal
	case source == playerRelationUnrelated && target == playerRelationUnrelated:
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

// normalizeCombatEvent builds the statistics record for a decoded damage-RPC result. It returns false for results
// that cannot enter statistics: a non-positive amount or a missing endpoint, which keeps the v0.3 admission rule.
func normalizeCombatEvent(event combatEvent, source, target playerRelation) (CombatEventRecord, bool) {
	if event.Damage <= 0 || !event.HasSourceEntity || event.SourceEntityID == 0 || !event.HasTargetEntity || event.TargetEntityID == 0 {
		return CombatEventRecord{}, false
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
		Key:        CombatEventKey{ProtocolSession: event.SessionNumber, RPCID: event.RPCID},
		ObservedAt: event.Timestamp,
		Evidence:   combatEvidenceDamageRPCResponse,
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
