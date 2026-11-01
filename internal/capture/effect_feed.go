package capture

// effectEntityRelation classifies an entity with the same evidence rules as damage
// attribution. A role entity of the local team is local_role; other entities that
// reach the local team through a confirmed owner chain are local_owned.
func (p *processor) effectEntityRelation(entityID uint64) EffectEntityRelation {
	if p == nil || p.entities == nil || entityID == 0 {
		return EffectRelationUnknown
	}
	identity := resolveEntityIdentity(p.entities, p.masters, entityID)
	switch p.playerRelationForEntity(entityID, identity) {
	case playerRelationLocal:
		if p.playerRelevance.isRoleEntity(entityID) {
			return EffectRelationLocalRole
		}
		return EffectRelationLocalOwned
	case playerRelationUnrelated:
		return EffectRelationNonPlayer
	default:
		return EffectRelationUnknown
	}
}

func (p *processor) effectHolderGeneration(entityID uint64) uint64 {
	if p == nil || p.entities == nil {
		return 0
	}
	return p.entities.lifecycleView(entityID).ReusedAfterRemove
}

func float64Pointer(value *float32) *float64 {
	if value == nil {
		return nil
	}
	converted := float64(*value)
	return &converted
}

// observeEffectLifecycle feeds one decoded lifecycle row to the effect tracker.
// Only server-side gameplay-effect messages carry a handle: apply and remove
// requests identify no instance and never create tracked state.
func (p *processor) observeEffectLifecycle(row buffLifecycleAuditRow) {
	if p == nil || p.effects == nil {
		return
	}
	switch row.EventKind {
	case "GAMEPLAY_EFFECT_APPLY":
		holder, ok := correlatedEffectHolder(row)
		if !ok || row.HandleID == nil {
			p.effects.NoteSkippedUncorrelatedHolder()
			return
		}
		event := EffectApplyEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: p.effectHolderGeneration(holder),
			HandleID: *row.HandleID, HolderRelation: p.effectEntityRelation(holder), SourceRelation: EffectRelationUnknown,
			DurationSeconds: float64Pointer(row.DurationSeconds), RemainingSeconds: float64Pointer(row.RemainingDuration),
			ObservedAt: row.Timestamp,
		}
		if row.ID != nil {
			event.EffectID, event.HasEffectID = *row.ID, true
			event.DefinitionLinked = p.masters.hasBuffDefinition(*row.ID)
		}
		if row.InstigatorID != nil && *row.InstigatorID != 0 {
			event.SourceEntityID, event.HasSource = *row.InstigatorID, true
			event.SourceRelation = p.effectEntityRelation(*row.InstigatorID)
		}
		if row.StackCount != nil {
			event.StackCount, event.HasStackCount = *row.StackCount, true
		}
		p.effects.ObserveApply(event)
		p.effects.RefreshRelations(row.SessionNumber, holder, p.effectEntityRelation)
	case "GAMEPLAY_EFFECT_REMOVE":
		holder, ok := correlatedEffectHolder(row)
		if !ok || row.HandleID == nil {
			p.effects.NoteSkippedUncorrelatedHolder()
			return
		}
		p.effects.ObserveRemove(EffectRemoveEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: p.effectHolderGeneration(holder),
			HandleID: *row.HandleID, ObservedAt: row.Timestamp,
		})
	case "BUFF_DURATION_CHANGE", "BUFF_STACK_COUNT_CHANGE":
		if row.CombatCommonEntityID == nil || *row.CombatCommonEntityID == 0 || row.HandleID == nil {
			p.effects.NoteSkippedUncorrelatedHolder()
			return
		}
		holder := *row.CombatCommonEntityID
		event := EffectUpdateEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: p.effectHolderGeneration(holder),
			HandleID:        *row.HandleID,
			DurationSeconds: float64Pointer(row.DurationSeconds), RemainingSeconds: float64Pointer(row.RemainingDuration),
			ObservedAt: row.Timestamp,
		}
		if row.EventKind == "BUFF_STACK_COUNT_CHANGE" && row.StackCount != nil {
			event.StackCount, event.HasStackCount = *row.StackCount, true
		}
		p.effects.ObserveUpdate(event)
		p.effects.RefreshRelations(row.SessionNumber, holder, p.effectEntityRelation)
	}
}

// observeEffectSnapshot feeds one FightBuffInformation row of an EntityActiveResponse.
// The holder is the entity of the matching EntityActiveRequest; without that match
// the row says nothing about who holds the effect.
func (p *processor) observeEffectSnapshot(frame gameFrame, record rawBuffSnapshot, entityID uint64, entityKnown bool) {
	if p == nil || p.effects == nil {
		return
	}
	if !entityKnown || entityID == 0 || !record.HasHandleID {
		p.effects.NoteSkippedSnapshot()
		return
	}
	event := EffectApplyEvent{
		SessionNumber: p.sessionNumber, HolderEntityID: entityID, HolderGeneration: p.effectHolderGeneration(entityID),
		HandleID: record.HandleID, HolderRelation: p.effectEntityRelation(entityID), SourceRelation: EffectRelationUnknown,
		ObservedAt: frame.timestamp,
	}
	if record.HasID {
		event.EffectID, event.HasEffectID = record.ID, true
		event.DefinitionLinked = p.masters.hasBuffDefinition(record.ID)
	}
	if record.HasInstigatorID && record.InstigatorID != 0 {
		event.SourceEntityID, event.HasSource = record.InstigatorID, true
		event.SourceRelation = p.effectEntityRelation(record.InstigatorID)
	}
	if record.HasStack {
		event.StackCount, event.HasStackCount = record.Stack, true
	}
	if record.HasDuration {
		duration := float64(record.Duration)
		event.DurationSeconds = &duration
	}
	if record.HasRemaining {
		remaining := float64(record.Remaining)
		event.RemainingSeconds = &remaining
	}
	p.effects.ObserveSnapshot(event)
	p.effects.RefreshRelations(p.sessionNumber, entityID, p.effectEntityRelation)
}

// correlatedEffectHolder returns the holder of a gameplay-effect row. The explicit
// TargetEntityId counts only when it matches CombatCommon.EntityId on the same
// event; a remove row that carries no explicit target falls back to CombatCommon.
func correlatedEffectHolder(row buffLifecycleAuditRow) (uint64, bool) {
	switch {
	case row.TargetEntityID != nil:
		if (row.TargetStatus != resolutionCorrelated && row.TargetStatus != resolutionVerified) || *row.TargetEntityID == 0 {
			return 0, false
		}
		return *row.TargetEntityID, true
	case row.EventKind == "GAMEPLAY_EFFECT_REMOVE" && row.CombatCommonEntityID != nil && *row.CombatCommonEntityID != 0:
		return *row.CombatCommonEntityID, true
	default:
		return 0, false
	}
}

// effectSnapshotAtLastFrame evaluates the tracker at the newest frame time, which
// is the capture clock the tracker's events use.
func (p *processor) effectSnapshotAtLastFrame() EffectSnapshot {
	if p == nil || p.effects == nil {
		return emptyEffectSnapshot()
	}
	return p.effects.SnapshotAt(CaptureQueryTime{CaptureSessionID: p.effects.captureSessionID, Timestamp: p.currentFrameTime})
}
