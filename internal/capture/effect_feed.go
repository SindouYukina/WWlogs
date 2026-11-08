package capture

import "time"

// effectEntityRelation classifies an entity with the same evidence rules as damage
// attribution. A role of the local team, whether reported as the current entity or
// listed in the local PlayerSceneGroup, is local_role; other entities that reach
// the local team through a confirmed owner chain are local_owned.
func (p *processor) effectEntityRelation(entityID uint64) EffectEntityRelation {
	if p == nil || p.entities == nil || entityID == 0 {
		return EffectRelationUnknown
	}
	identity := resolveEntityIdentity(p.entities, p.masters, entityID)
	switch p.playerRelationForEntity(entityID, identity) {
	case playerRelationLocal:
		if p.playerRelevance.isRoleEntity(entityID) || p.playerRelevance.isLocalPlayerRoleEntity(identity.Runtime) {
			return EffectRelationLocalRole
		}
		return EffectRelationLocalOwned
	case playerRelationUnrelated:
		return EffectRelationNonPlayer
	default:
		return EffectRelationUnknown
	}
}

// refreshEffectRelations re-evaluates UNKNOWN relations of tracked effects after the
// entity or local-team evidence changed. It must run on the processing goroutine,
// the only owner of the entity resolver.
func (p *processor) refreshEffectRelations() {
	if p == nil || p.effects == nil {
		return
	}
	p.effects.RefreshUnknownRelations(p.effectEntityRelation)
}

func (p *processor) effectHolderGeneration(entityID uint64) uint64 {
	if p == nil || p.entities == nil {
		return 0
	}
	return p.entities.lifecycleView(entityID).ReusedAfterRemove
}

// effectStaticInfo returns the static definition parameters of an effect id, or nil.
func (p *processor) effectStaticInfo(effectID uint64) *EffectStaticInfo {
	if p == nil {
		return nil
	}
	static, hasStatic := p.masters.buffStatic(effectID)
	term, hasTerm := p.masters.buffTerm(effectID)
	if !hasStatic && !hasTerm {
		return nil
	}
	info := &EffectStaticInfo{DeveloperTags: append([]string(nil), static.tags...), HUDCueTypes: append([]int32(nil), static.hudCueTypes...), HUDIconGeneric: static.hudIconGeneric}
	info.ElementID, info.ElementBasis = effectElementFor(term.termID, hasTerm, static)
	if info.ElementBasis == effectElementBasisAttribute {
		info.ElementFamily = elementFamilyOfAttribute(static.attribute)
	}
	if hasTerm {
		info.Term = &EffectTermInfo{TermID: term.termID, Name: term.name, Description: term.description, Status: string(term.status)}
	}
	if static.hasDurationPolicy {
		value := static.durationPolicy
		info.DurationPolicy = &value
	}
	if static.hasDuration {
		value := static.durationSeconds
		info.StaticDurationSeconds = &value
	}
	if static.hasStackLimit {
		value := static.stackLimit
		info.StackLimit = &value
	}
	return info
}

func float64Pointer(value *float32) *float64 {
	if value == nil {
		return nil
	}
	converted := float64(*value)
	return &converted
}

// observeEffectLifecycle feeds one decoded lifecycle row to the effect tracker.
// Gameplay-effect notifies and the local client's effect reports carry a handle;
// server apply and remove requests and the client's answers to them identify no
// instance by themselves and never create tracked state.
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
			if event.DefinitionLinked {
				event.Static = p.effectStaticInfo(*row.ID)
			}
			event.CombatClass, event.CombatBasis = p.effectDefinitionClass(*row.ID)
		}
		if row.InstigatorID != nil && *row.InstigatorID != 0 {
			event.SourceEntityID, event.HasSource = *row.InstigatorID, true
			event.SourceRelation = p.effectEntityRelation(*row.InstigatorID)
		}
		if row.StackCount != nil {
			event.StackCount, event.HasStackCount = *row.StackCount, true
		}
		event.HolderMonster, event.SourceMonster = p.effectMonsterEntity(event.HolderEntityID), event.HasSource && p.effectMonsterEntity(event.SourceEntityID)
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
	case "CLIENT_EFFECT_APPLY_REPORT", "CLIENT_EFFECT_STACK_REPORT", "CLIENT_EFFECT_REMOVE_REPORT":
		p.observeClientEffectReport(row)
	}
}

// observeClientEffectReport feeds an outbound effect report of the local client. The
// report is the client's own state change, not a server confirmation, and the tracker
// keeps it labelled client_report.
func (p *processor) observeClientEffectReport(row buffLifecycleAuditRow) {
	holder, ok := clientEffectHolder(row)
	if !ok || row.HandleID == nil {
		p.effects.NoteSkippedUncorrelatedHolder()
		return
	}
	generation := p.effectHolderGeneration(holder)
	switch row.EventKind {
	case "CLIENT_EFFECT_APPLY_REPORT":
		event := EffectApplyEvent{
			Evidence: EffectEvidenceClientReport, SessionNumber: row.SessionNumber,
			HolderEntityID: holder, HolderGeneration: generation, HandleID: *row.HandleID,
			HolderRelation: p.effectEntityRelation(holder), SourceRelation: EffectRelationUnknown,
			DurationSeconds: float64Pointer(row.DurationSeconds), ObservedAt: row.Timestamp,
		}
		if row.ID != nil {
			event.EffectID, event.HasEffectID = *row.ID, true
			event.DefinitionLinked = p.masters.hasBuffDefinition(*row.ID)
			if event.DefinitionLinked {
				event.Static = p.effectStaticInfo(*row.ID)
			}
			event.CombatClass, event.CombatBasis = p.effectDefinitionClass(*row.ID)
		}
		if row.InstigatorID != nil && *row.InstigatorID != 0 {
			event.SourceEntityID, event.HasSource = *row.InstigatorID, true
			event.SourceRelation = p.effectEntityRelation(*row.InstigatorID)
		}
		if row.StackCount != nil {
			event.StackCount, event.HasStackCount = *row.StackCount, true
		}
		event.HolderMonster, event.SourceMonster = p.effectMonsterEntity(event.HolderEntityID), event.HasSource && p.effectMonsterEntity(event.SourceEntityID)
		p.effects.ObserveApply(event)
	case "CLIENT_EFFECT_STACK_REPORT":
		event := EffectUpdateEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: generation,
			HandleID: *row.HandleID, ObservedAt: row.Timestamp,
		}
		// The push carries the full duration it restarts; observed natural expiries follow
		// the last such push plus its duration on the client clock. NotRefreshDuration
		// keeps the running estimate.
		if row.NotRefreshDuration == nil || !*row.NotRefreshDuration {
			event.DurationSeconds = float64Pointer(row.DurationSeconds)
		}
		if row.StackCount != nil {
			event.StackCount, event.HasStackCount = *row.StackCount, true
		}
		p.effects.ObserveUpdate(event)
	case "CLIENT_EFFECT_REMOVE_REPORT":
		p.effects.ObserveRemove(EffectRemoveEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: generation,
			HandleID: *row.HandleID, ObservedAt: row.Timestamp,
			Evidence: EffectEvidenceClientReport, Premature: row.IsPrematureRemoval != nil && *row.IsPrematureRemoval,
		})
	}
	p.effects.RefreshRelations(row.SessionNumber, holder, p.effectEntityRelation)
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
		if event.DefinitionLinked {
			event.Static = p.effectStaticInfo(record.ID)
		}
		event.CombatClass, event.CombatBasis = p.effectDefinitionClass(record.ID)
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
	event.HolderMonster, event.SourceMonster = p.effectMonsterEntity(event.HolderEntityID), event.HasSource && p.effectMonsterEntity(event.SourceEntityID)
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
	return p.effectSnapshotAfterLastFrame(0)
}

// effectSnapshotAfterLastFrame evaluates the tracker lead past the newest frame. Querying never changes the tracker, so a
// later query only reads the same countdowns further along.
func (p *processor) effectSnapshotAfterLastFrame(lead time.Duration) EffectSnapshot {
	if p == nil || p.effects == nil {
		return emptyEffectSnapshot()
	}
	p.refreshEffectRelations()
	query := p.currentFrameTime
	if !query.IsZero() {
		query = query.Add(lead)
	}
	return p.effects.SnapshotAt(CaptureQueryTime{CaptureSessionID: p.effects.captureSessionID, Timestamp: query})
}
