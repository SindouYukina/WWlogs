package capture

import "time"

// EffectEntityRelation classifies an entity with the same evidence rules as damage
// attribution. A role of the local team, whether reported as the current entity or
// listed in the local PlayerSceneGroup, is local_role; the local player's own
// Proto_PlayerEntity is local_player; other entities that reach the local team through
// a confirmed owner chain are local_owned.
func (p *Processor) EffectEntityRelation(entityID uint64) EffectEntityRelation {
	if p == nil || p.Entities == nil || entityID == 0 {
		return EffectRelationUnknown
	}
	identity := ResolveEntityIdentity(p.Entities, p.Masters, entityID)
	if identity.Runtime.EntityID == entityID && p.PlayerRelevance.isLocalPlayerEntity(identity.Runtime) {
		return EffectRelationLocalPlayer
	}
	switch p.playerRelationForEntity(entityID, identity) {
	case PlayerRelationLocal:
		if p.PlayerRelevance.IsRoleEntity(entityID) || p.PlayerRelevance.isLocalPlayerRoleEntity(identity.Runtime) {
			return EffectRelationLocalRole
		}
		return EffectRelationLocalOwned
	case PlayerRelationUnrelated:
		return EffectRelationNonPlayer
	default:
		return EffectRelationUnknown
	}
}

// refreshEffectRelations re-evaluates UNKNOWN relations of tracked effects after the
// entity or local-team evidence changed. It must run on the processing goroutine,
// the only owner of the entity resolver.
func (p *Processor) refreshEffectRelations() {
	if p == nil || p.Effects == nil {
		return
	}
	p.Effects.RefreshUnknownRelations(p.EffectEntityRelation)
}

func (p *Processor) effectHolderGeneration(entityID uint64) uint64 {
	if p == nil || p.Entities == nil {
		return 0
	}
	return p.Entities.lifecycleView(entityID).ReusedAfterRemove
}

// EffectStaticInfo returns the static definition parameters of an effect id, or nil.
func (p *Processor) EffectStaticInfo(effectID uint64) *EffectStaticInfo {
	if p == nil {
		return nil
	}
	return p.Masters.effectStaticInfo(effectID)
}

// EffectDefinition is the static Master evidence of one effect id for a view of stored effects: the same definition the
// effect tracker attaches to a live instance. Polarity is the game's HUD arrow (effectPolarityFor) and HUDIcon reports that a
// HUD buff bar draws the effect with an icon of its own; neither says what the effect does.
type EffectDefinition struct {
	Static   *EffectStaticInfo
	Polarity EffectPolarity
	HUDIcon  bool
}

// EffectDefinition looks up an effect id the way the effect tracker does. An id without a static row has a nil Static and
// stays unclassified.
func (m *MasterData) EffectDefinition(effectID uint64) EffectDefinition {
	var static *EffectStaticInfo
	if m != nil {
		static = m.masters.effectStaticInfo(effectID)
	}
	return EffectDefinition{Static: static, Polarity: effectPolarityFor(EffectInstanceSnapshot{Static: static}), HUDIcon: static != nil && len(static.HUDCueTypes) > 0}
}

// effectStaticInfo builds the static definition parameters of an effect id, or nil without a static row or a term.
func (m *GameMaster) effectStaticInfo(effectID uint64) *EffectStaticInfo {
	static, hasStatic := m.buffStatic(effectID)
	term, hasTerm := m.buffTerm(effectID)
	if !hasStatic && !hasTerm {
		return nil
	}
	info := &EffectStaticInfo{DeveloperTags: append([]string(nil), static.Tags...), HUDCueTypes: append([]int32(nil), static.hudCueTypes...), HUDPolarity: static.hudPolarity, HUDHiddenOnMonsterHead: static.hudHiddenOnMonsterHead}
	info.ElementID, info.ElementBasis = effectElementFor(term.TermID, hasTerm, static)
	if hasTerm {
		info.Term = &EffectTermInfo{TermID: term.TermID, Name: term.Name, Description: term.Description, Status: string(term.Status)}
	}
	if static.HasDurationPolicy {
		value := static.DurationPolicy
		info.DurationPolicy = &value
	}
	if static.HasDuration {
		value := static.DurationSeconds
		info.StaticDurationSeconds = &value
	}
	if static.HasStackLimit {
		value := static.StackLimit
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
func (p *Processor) observeEffectLifecycle(row BuffLifecycleAuditRow) {
	if p == nil || p.Effects == nil {
		return
	}
	switch row.EventKind {
	case "GAMEPLAY_EFFECT_APPLY":
		holder, ok := correlatedEffectHolder(row)
		if !ok || row.HandleID == nil {
			p.Effects.NoteSkippedUncorrelatedHolder()
			return
		}
		event := EffectApplyEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: p.effectHolderGeneration(holder),
			HandleID: *row.HandleID, HolderRelation: p.EffectEntityRelation(holder), SourceRelation: EffectRelationUnknown,
			DurationSeconds: float64Pointer(row.DurationSeconds), RemainingSeconds: float64Pointer(row.RemainingDuration),
			ObservedAt: row.Timestamp,
		}
		if row.ID != nil {
			event.EffectID, event.HasEffectID = *row.ID, true
			event.DefinitionLinked = p.Masters.HasBuffDefinition(*row.ID)
			if event.DefinitionLinked {
				event.Static = p.EffectStaticInfo(*row.ID)
			}
			event.CombatClass, event.CombatBasis = p.effectDefinitionClass(*row.ID)
		}
		if row.InstigatorID != nil && *row.InstigatorID != 0 {
			event.SourceEntityID, event.HasSource = *row.InstigatorID, true
			event.SourceRelation = p.EffectEntityRelation(*row.InstigatorID)
		}
		if row.StackCount != nil {
			event.StackCount, event.HasStackCount = *row.StackCount, true
		}
		event.HolderMonster, event.SourceMonster = p.effectMonsterEntity(event.HolderEntityID), event.HasSource && p.effectMonsterEntity(event.SourceEntityID)
		p.withHistoryActors(&event)
		p.recordStatusTrigger(row, holder)
		p.observeStatusCause(row, holder, &event.HolderRelation, event.SourceRelation)
		p.Effects.ObserveApply(event)
		p.Effects.RefreshRelations(row.SessionNumber, holder, p.EffectEntityRelation)
	case "GAMEPLAY_EFFECT_REMOVE":
		holder, ok := correlatedEffectHolder(row)
		if !ok || row.HandleID == nil {
			p.Effects.NoteSkippedUncorrelatedHolder()
			return
		}
		p.observeStatusCause(row, holder, nil, EffectRelationUnknown)
		p.Effects.ObserveRemove(EffectRemoveEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: p.effectHolderGeneration(holder),
			HandleID: *row.HandleID, ObservedAt: row.Timestamp,
		})
	case "BUFF_DURATION_CHANGE", "BUFF_STACK_COUNT_CHANGE":
		if row.CombatCommonEntityID == nil || *row.CombatCommonEntityID == 0 || row.HandleID == nil {
			p.Effects.NoteSkippedUncorrelatedHolder()
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
			if row.InstigatorID != nil && *row.InstigatorID != 0 {
				event.StackInstigator, event.HasStackInstigator = *row.InstigatorID, true
			}
			// Before the tracker takes the new count: the cause compares it with the count before.
			p.observeStatusCause(row, holder, nil, EffectRelationUnknown)
		}
		p.Effects.ObserveUpdate(event)
		p.Effects.RefreshRelations(row.SessionNumber, holder, p.EffectEntityRelation)
	case "CLIENT_EFFECT_APPLY_REPORT", "CLIENT_EFFECT_STACK_REPORT", "CLIENT_EFFECT_REMOVE_REPORT":
		p.observeClientEffectReport(row)
	}
}

// observeClientEffectReport feeds an outbound effect report of the local client. The
// report is the client's own state change, not a server confirmation, and the tracker
// keeps it labelled client_report.
func (p *Processor) observeClientEffectReport(row BuffLifecycleAuditRow) {
	holder, ok := ClientEffectHolder(row)
	if !ok || row.HandleID == nil {
		p.Effects.NoteSkippedUncorrelatedHolder()
		return
	}
	generation := p.effectHolderGeneration(holder)
	switch row.EventKind {
	case "CLIENT_EFFECT_APPLY_REPORT":
		event := EffectApplyEvent{
			Evidence: EffectEvidenceClientReport, SessionNumber: row.SessionNumber,
			HolderEntityID: holder, HolderGeneration: generation, HandleID: *row.HandleID,
			HolderRelation: p.EffectEntityRelation(holder), SourceRelation: EffectRelationUnknown,
			DurationSeconds: float64Pointer(row.DurationSeconds), ObservedAt: row.Timestamp,
		}
		if row.ID != nil {
			event.EffectID, event.HasEffectID = *row.ID, true
			event.DefinitionLinked = p.Masters.HasBuffDefinition(*row.ID)
			if event.DefinitionLinked {
				event.Static = p.EffectStaticInfo(*row.ID)
			}
			event.CombatClass, event.CombatBasis = p.effectDefinitionClass(*row.ID)
		}
		if row.InstigatorID != nil && *row.InstigatorID != 0 {
			event.SourceEntityID, event.HasSource = *row.InstigatorID, true
			event.SourceRelation = p.EffectEntityRelation(*row.InstigatorID)
		}
		if row.StackCount != nil {
			event.StackCount, event.HasStackCount = *row.StackCount, true
		}
		event.HolderMonster, event.SourceMonster = p.effectMonsterEntity(event.HolderEntityID), event.HasSource && p.effectMonsterEntity(event.SourceEntityID)
		p.withHistoryActors(&event)
		p.Effects.ObserveApply(event)
	case "CLIENT_EFFECT_STACK_REPORT":
		event := EffectUpdateEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: generation,
			HandleID: *row.HandleID, ObservedAt: row.Timestamp, Evidence: EffectEvidenceClientReport,
		}
		// The push carries the remaining time when it was sent (BroadcastBuffStackChanged sends
		// GetRemainDuration()); natural expiries follow the last such push plus that time on the
		// effect clock. NotRefreshDuration keeps the running estimate.
		if row.NotRefreshDuration == nil || !*row.NotRefreshDuration {
			event.DurationSeconds = float64Pointer(row.DurationSeconds)
		}
		if row.StackCount != nil {
			event.StackCount, event.HasStackCount = *row.StackCount, true
		}
		p.Effects.ObserveUpdate(event)
	case "CLIENT_EFFECT_REMOVE_REPORT":
		p.Effects.ObserveRemove(EffectRemoveEvent{
			SessionNumber: row.SessionNumber, HolderEntityID: holder, HolderGeneration: generation,
			HandleID: *row.HandleID, ObservedAt: row.Timestamp,
			Evidence: EffectEvidenceClientReport, Premature: row.IsPrematureRemoval != nil && *row.IsPrematureRemoval,
		})
	}
	p.Effects.RefreshRelations(row.SessionNumber, holder, p.EffectEntityRelation)
}

// observeEffectSnapshot feeds one FightBuffInformation row of an EntityActiveResponse.
// The holder is the entity of the matching EntityActiveRequest; without that match
// the row says nothing about who holds the effect.
func (p *Processor) observeEffectSnapshot(frame GameFrame, record RawBuffSnapshot, entityID uint64, entityKnown bool) {
	if p == nil || p.Effects == nil {
		return
	}
	if !entityKnown || entityID == 0 || !record.HasHandleID {
		p.Effects.NoteSkippedSnapshot()
		return
	}
	event := EffectApplyEvent{
		SessionNumber: p.SessionNumber, HolderEntityID: entityID, HolderGeneration: p.effectHolderGeneration(entityID),
		HandleID: record.HandleID, HolderRelation: p.EffectEntityRelation(entityID), SourceRelation: EffectRelationUnknown,
		ObservedAt: frame.Timestamp,
	}
	if record.HasID {
		event.EffectID, event.HasEffectID = record.ID, true
		event.DefinitionLinked = p.Masters.HasBuffDefinition(record.ID)
		if event.DefinitionLinked {
			event.Static = p.EffectStaticInfo(record.ID)
		}
		event.CombatClass, event.CombatBasis = p.effectDefinitionClass(record.ID)
	}
	if record.HasInstigatorID && record.InstigatorID != 0 {
		event.SourceEntityID, event.HasSource = record.InstigatorID, true
		event.SourceRelation = p.EffectEntityRelation(record.InstigatorID)
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
	p.withHistoryActors(&event)
	p.Effects.ObserveSnapshot(event)
	p.Effects.RefreshRelations(p.SessionNumber, entityID, p.EffectEntityRelation)
}

// correlatedEffectHolder returns the holder of a gameplay-effect row. The explicit
// TargetEntityId counts only when it matches CombatCommon.EntityId on the same
// event; a remove row that carries no explicit target falls back to CombatCommon.
func correlatedEffectHolder(row BuffLifecycleAuditRow) (uint64, bool) {
	switch {
	case row.TargetEntityID != nil:
		if (row.TargetStatus != ResolutionCorrelated && row.TargetStatus != ResolutionVerified) || *row.TargetEntityID == 0 {
			return 0, false
		}
		return *row.TargetEntityID, true
	case row.EventKind == "GAMEPLAY_EFFECT_REMOVE" && row.CombatCommonEntityID != nil && *row.CombatCommonEntityID != 0:
		return *row.CombatCommonEntityID, true
	default:
		return 0, false
	}
}

// EffectSnapshotAtLastFrame evaluates the tracker at the newest frame time, which
// is the capture clock the tracker's events use.
func (p *Processor) EffectSnapshotAtLastFrame() EffectSnapshot {
	return p.EffectSnapshotAfterLastFrame(0)
}

// EffectSnapshotAfterLastFrame evaluates the tracker lead past the newest frame. Querying never changes the tracker, so a
// later query only reads the same countdowns further along.
func (p *Processor) EffectSnapshotAfterLastFrame(lead time.Duration) EffectSnapshot {
	if p == nil || p.Effects == nil {
		return emptyEffectSnapshot()
	}
	p.refreshEffectRelations()
	query := p.CurrentFrameTime
	if !query.IsZero() {
		query = query.Add(lead)
	}
	return p.Effects.SnapshotAt(CaptureQueryTime{CaptureSessionID: p.Effects.CaptureSessionID, Timestamp: query})
}
