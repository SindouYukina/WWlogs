//go:build wwdev

package capture

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func derefUint64(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func addVehicleContributorEvent(summary *encounterSummary, event combatEvent) {
	candidate := event.DamageContributorCandidate
	if summary.vehicleContributors == nil {
		summary.vehicleContributors = make(map[uint64]*vehicleContributorSummary)
	}
	row := summary.vehicleContributors[candidate.FollowerConfigID]
	if row == nil {
		row = &vehicleContributorSummary{followerConfigID: candidate.FollowerConfigID}
		summary.vehicleContributors[candidate.FollowerConfigID] = row
	}
	row.status = strongestEvidence(row.status, candidate.Status)
	if candidate.Name != nil && candidate.Name.Text != "" {
		row.name, row.nameKind = candidate.Name.Text, candidate.Name.Kind
	}
	row.events++
	row.damage += event.Damage
}

func writeVehicleContributors(w io.Writer, summary *encounterSummary) error {
	if len(summary.vehicleContributors) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(summary.vehicleContributors))
	for id := range summary.vehicleContributors {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if _, err := fmt.Fprintln(w, "\n### Player-team vehicle contributors (independent damage sources)\n\nThese correlated vehicle contributors are included in the player-team damage and direction totals above when the runtime ownership link is established. They are listed separately from character rows so vehicle damage is not attributed to the active character or counted twice. The vehicle association is CORRELATED, not server-confirmed credit."); err != nil {
		return err
	}
	for _, id := range ids {
		row := summary.vehicleContributors[id]
		name := "Vehicle"
		if row.name != "" {
			name = fmt.Sprintf("[internal] %s", row.name)
		}
		if _, err := fmt.Fprintf(w, "\n- %s [FollowerSummonConfigId:%d; %s]: %d event(s), CombatResult magnitude %d", name, row.followerConfigID, row.status, row.events, row.damage); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func (p *processor) observeEncounterSummary(event combatEvent) {
	if p.dev.encounterSummary == nil {
		return
	}
	sourceRelation, targetRelation, _ := p.playerRelevantDamageEvent(event)
	if p.dev.encounterSummaries == nil {
		p.dev.encounterSummaries = make(map[encounterSummaryKey]*encounterSummary)
	}
	key := encounterSummaryKey{session: event.SessionNumber}
	sourceScene, sourceHasScene := event.Source.Runtime.sceneInstanceID, event.Source.Runtime.hasSceneInstanceID
	targetScene, targetHasScene := event.Target.Runtime.sceneInstanceID, event.Target.Runtime.hasSceneInstanceID
	switch {
	case sourceHasScene && targetHasScene && sourceScene != targetScene:
		key.scopeConflict = true
	case sourceHasScene:
		key.sceneInstance, key.hasScene = sourceScene, true
	case targetHasScene:
		key.sceneInstance, key.hasScene = targetScene, true
	}
	if _, exists := p.dev.encounterSummaries[key]; !exists && len(p.dev.encounterSummaries) >= maxEncounterSummaryGroups {
		key = encounterSummaryKey{overflow: true}
	}
	summary := p.dev.encounterSummaries[key]
	if summary == nil {
		summary = &encounterSummary{
			key: key, characters: make(map[uint64]*characterSummary),
			ownerCandidates:        make(map[uint64]*ownerCandidateSummary),
			healingAttributions:    make(map[healingAttributionKey]*healingAttributionSummary),
			bossCandidates:         make(map[bossCandidateKey]bossCandidateSummary),
			buffObservations:       make(map[buffObservationKey]int),
			uncreditedEvents:       make(map[uncreditedEventKey]*uncreditedEventSummary),
			classificationEvidence: make(map[string]struct{}),
		}
		p.dev.encounterSummaries[key] = summary
	}
	if summary.startedAt.IsZero() || (!event.Timestamp.IsZero() && event.Timestamp.Before(summary.startedAt)) {
		summary.startedAt = event.Timestamp
	}
	if event.Timestamp.After(summary.endedAt) {
		summary.endedAt = event.Timestamp
	}
	summary.eventCount++
	summary.combatResultAbs += event.Damage
	if event.EventTypeCandidate != "" && event.EventTypeProof != "" {
		proof := event.EventTypeCandidate + ": " + event.EventTypeProof
		if _, exists := summary.classificationEvidence[proof]; exists || len(summary.classificationEvidence) < maxSummaryClassificationEvidence {
			summary.classificationEvidence[proof] = struct{}{}
		} else {
			summary.classificationEvidenceOmitted++
		}
	}
	switch event.EventTypeCandidate {
	case "DAMAGE_CANDIDATE":
		summary.damageCandidates += event.Damage
		observeDamageDirection(summary, event, sourceRelation, targetRelation)
	case "HEALING_CANDIDATE":
		summary.healCandidates += event.Damage
	case "OTHER_COMBAT_RESULT_CANDIDATE":
		summary.otherCandidates += event.Damage
	default:
		summary.unclassified += event.Damage
	}
	if event.EventTypeCandidate == "HEALING_CANDIDATE" {
		addHealingAttribution(summary, event)
	}

	if sourceRelation == playerRelationLocal && event.EventTypeCandidate == "DAMAGE_CANDIDATE" &&
		event.DamageContributorCandidate != nil && event.DamageContributorCandidate.ActorType == "Vehicle" {
		addVehicleContributorEvent(summary, event)
	} else if isCreditedCharacterStatus(event.CharacterStatus) && event.CharacterID != 0 {
		addCreditedCharacterEvent(summary, event)
	} else {
		summary.unknownCredit++
		summary.unknownCreditAbs += event.Damage
		addUncreditedEvent(summary, event)
		if event.HasSourceEntity && event.SourceEntityID != 0 {
			if p.dev.pendingSummaryCreditCount < maxPendingSummaryCreditEvents {
				if p.dev.pendingSummaryCredits == nil {
					p.dev.pendingSummaryCredits = make(map[uint64][]pendingSummaryCredit)
				}
				p.dev.pendingSummaryCredits[event.SourceEntityID] = append(p.dev.pendingSummaryCredits[event.SourceEntityID], pendingSummaryCredit{key: key, event: event})
				p.dev.pendingSummaryCreditCount++
			} else {
				summary.identityBackfillEventsOmitted++
			}
		}
	}

	if event.SummonerCharacter.HasCharacterID && event.SummonerCharacter.CharacterID != 0 {
		candidate := summary.ownerCandidates[event.SummonerCharacter.CharacterID]
		if candidate == nil {
			if len(summary.ownerCandidates) >= maxSummaryOwnerCandidates {
				summary.ownerCandidatesOmitted++
			} else {
				candidate = &ownerCandidateSummary{id: event.SummonerCharacter.CharacterID}
				summary.ownerCandidates[candidate.id] = candidate
			}
		}
		if candidate != nil {
			candidate.status = strongestEvidence(candidate.status, event.SummonerCharacter.Status)
			if event.SummonerCharacter.Proof != "" {
				candidate.proof = event.SummonerCharacter.Proof
			}
			if event.SummonerCharacter.HasCharacter {
				candidate.name = event.SummonerCharacter.Character.Name
			}
			candidate.amount += event.Damage
			candidate.events++
		}
	}
	addBossCandidate(summary, event.Source)
	addBossCandidate(summary, event.Target)
}

func observeDamageDirection(summary *encounterSummary, event combatEvent, sourceRelation, targetRelation playerRelation) {
	if sourceRelation == playerRelationLocal && isEnemyEntityIdentity(event.Target) {
		summary.partyToEnemyCandidateAbs += event.Damage
		summary.partyToEnemyCandidateEvents++
		if len(event.Target.TemplateCandidates) > 0 {
			summary.partyToBossCandidateAbs += event.Damage
			summary.partyToBossCandidateEvents++
		}
		return
	}
	if targetRelation == playerRelationLocal && isEnemyEntityIdentity(event.Source) {
		summary.enemyToPartyCandidateAbs += event.Damage
		summary.enemyToPartyCandidateEvents++
		if len(event.Source.TemplateCandidates) > 0 {
			summary.bossToPartyCandidateAbs += event.Damage
			summary.bossToPartyCandidateEvents++
		}
		return
	}
	summary.unknownDirectionDamageAbs += event.Damage
	summary.unknownDirectionDamageEvents++
}

func isEnemyEntityIdentity(identity entityIdentity) bool {
	return identity.HasMonster || len(identity.TemplateCandidates) > 0
}

func addUncreditedEvent(summary *encounterSummary, event combatEvent) {
	if summary.uncreditedEvents == nil {
		summary.uncreditedEvents = make(map[uncreditedEventKey]*uncreditedEventSummary)
	}
	key := makeUncreditedEventKey(event)
	row := summary.uncreditedEvents[key]
	if row == nil {
		if len(summary.uncreditedEvents) >= maxSummaryUncreditedRows {
			summary.uncreditedEventsOmitted++
			return
		}
		row = &uncreditedEventSummary{key: key}
		summary.uncreditedEvents[key] = row
	}
	row.events++
	row.magnitude += event.Damage
}

func removeUncreditedEvent(summary *encounterSummary, event combatEvent) {
	key := makeUncreditedEventKey(event)
	row := summary.uncreditedEvents[key]
	if row == nil {
		if summary.uncreditedEventsOmitted > 0 {
			summary.uncreditedEventsOmitted--
		}
		return
	}
	row.events--
	row.magnitude -= event.Damage
	if row.events == 0 {
		delete(summary.uncreditedEvents, key)
	}
}

// uncreditedEventKeyTail orders the key fields that writeUncreditedEvents does not compare first.
func uncreditedEventKeyTail(key uncreditedEventKey) string {
	return fmt.Sprintf("%t/%020d/%t/%020d/%t/%t/%020d/%t/%t", key.hasSourceScene, key.sourceSceneID, key.hasTargetScene,
		key.targetSceneID, key.hasDamageID, key.hasBulletID, uint64(key.bulletID), key.hasSourceEntity, key.hasTargetEntity)
}

func makeUncreditedEventKey(event combatEvent) uncreditedEventKey {
	return uncreditedEventKey{
		sessionNumber: event.SessionNumber,
		sourceSceneID: event.Source.Runtime.sceneInstanceID, hasSourceScene: event.Source.Runtime.hasSceneInstanceID,
		targetSceneID: event.Target.Runtime.sceneInstanceID, hasTargetScene: event.Target.Runtime.hasSceneInstanceID,
		eventType: event.EventTypeCandidate, skillID: event.SkillID,
		damageID: event.DamageID, hasDamageID: event.HasDamageID,
		bulletID: event.BulletID, hasBulletID: event.HasBulletID,
		sourceEntityID: event.SourceEntityID, hasSourceEntity: event.HasSourceEntity,
		targetEntityID: event.TargetEntityID, hasTargetEntity: event.HasTargetEntity,
	}
}

func (p *processor) observeBuffSnapshot(row buffSnapshotAuditRow) {
	if p.dev.encounterSummary == nil {
		return
	}
	if p.dev.encounterSummaries == nil {
		p.dev.encounterSummaries = make(map[encounterSummaryKey]*encounterSummary)
	}
	key := encounterSummaryKey{session: row.SessionNumber}
	observation := buffObservationKey{hasBuffID: row.Buff.HasID, buffID: row.Buff.ID}
	if row.EntityID != nil && *row.EntityID != 0 {
		observation.targetID, observation.hasTargetID = *row.EntityID, true
		entity, _ := p.entities.resolve(*row.EntityID)
		if entity.hasSceneInstanceID {
			key.sceneInstance, key.hasScene = entity.sceneInstanceID, true
		}
	}
	if _, exists := p.dev.encounterSummaries[key]; !exists && len(p.dev.encounterSummaries) >= maxEncounterSummaryGroups {
		key = encounterSummaryKey{overflow: true}
	}
	summary := p.dev.encounterSummaries[key]
	if summary == nil {
		summary = &encounterSummary{
			key: key, characters: make(map[uint64]*characterSummary),
			ownerCandidates:        make(map[uint64]*ownerCandidateSummary),
			healingAttributions:    make(map[healingAttributionKey]*healingAttributionSummary),
			bossCandidates:         make(map[bossCandidateKey]bossCandidateSummary),
			buffObservations:       make(map[buffObservationKey]int),
			classificationEvidence: make(map[string]struct{}),
		}
		p.dev.encounterSummaries[key] = summary
	}
	if _, exists := summary.buffObservations[observation]; !exists && len(summary.buffObservations) >= maxSummaryBuffObservations {
		summary.buffObservationsOmitted++
		return
	}
	summary.buffObservations[observation]++
}

func addHealingAttribution(summary *encounterSummary, event combatEvent) {
	if !isCreditedCharacterStatus(event.CharacterStatus) || event.CharacterID == 0 ||
		!event.Target.HasCharacter || event.Target.Character.ID == 0 ||
		!isCreditedCharacterStatus(event.Target.CharacterStatus) {
		summary.healingAttributionUnresolved++
		summary.healingAttributionUnresolvedAbs += event.Damage
		return
	}

	hasSkillID := event.SkillID != 0 && event.SkillID != ^uint64(0)
	skillID, damageID := uint64(0), uint64(0)
	if hasSkillID {
		skillID = event.SkillID
	}
	if event.HasDamageID {
		damageID = event.DamageID
	}
	key := healingAttributionKey{
		healerID: event.CharacterID, targetID: event.Target.Character.ID,
		skillID: skillID, damageID: damageID,
		hasSkillID: hasSkillID, hasDamageID: event.HasDamageID,
	}
	row := summary.healingAttributions[key]
	if row == nil {
		if len(summary.healingAttributions) >= maxSummaryHealingAttributions {
			summary.healingAttributionsOmitted++
			return
		}
		row = &healingAttributionSummary{
			key:          key,
			healerProofs: make(map[string]struct{}), targetProofs: make(map[string]struct{}),
			skillProofs: make(map[string]struct{}), classificationProofs: make(map[string]struct{}),
		}
		summary.healingAttributions[key] = row
	}
	row.healerStatus = strongestEvidence(row.healerStatus, event.CharacterStatus)
	if event.CharacterProof != "" {
		row.healerProofs[event.CharacterProof] = struct{}{}
	}
	if event.CharacterName != "" && (event.CharacterNameStatus == resolutionVerified || event.CharacterNameStatus == resolutionCorrelated) {
		row.healerName, row.healerNameStatus = event.CharacterName, event.CharacterNameStatus
	}
	target := event.Target.Character
	row.targetStatus = strongestEvidence(row.targetStatus, event.Target.CharacterStatus)
	if event.Target.CharacterProof != "" {
		row.targetProofs[event.Target.CharacterProof] = struct{}{}
	}
	if target.Name != "" && (target.NameStatus == string(resolutionVerified) || target.NameStatus == string(resolutionCorrelated)) {
		row.targetName, row.targetNameStatus = target.Name, resolutionStatus(target.NameStatus)
	}
	row.skillStatus = strongestEvidence(row.skillStatus, event.SkillStatus)
	if event.SkillProof != "" {
		row.skillProofs[event.SkillProof] = struct{}{}
	}
	if event.SkillName != "" && (event.SkillNameStatus == resolutionVerified || event.SkillNameStatus == resolutionCorrelated) {
		row.skillName, row.skillNameStatus = event.SkillName, event.SkillNameStatus
	}
	row.classificationStatus = strongestEvidence(row.classificationStatus, event.EventTypeStatus)
	if event.EventTypeProof != "" {
		row.classificationProofs[event.EventTypeProof] = struct{}{}
	}
	row.reportedMagnitudeAbs += event.Damage
	row.events++
}

func addCreditedCharacterEvent(summary *encounterSummary, event combatEvent) {
	character := summary.characters[event.CharacterID]
	if character == nil {
		character = &characterSummary{id: event.CharacterID, skills: make(map[skillSummaryKey]*skillSummary), proofs: make(map[string]struct{})}
		summary.characters[event.CharacterID] = character
	}
	character.status = strongestEvidence(character.status, event.CharacterStatus)
	if event.CharacterProof != "" {
		character.proofs[event.CharacterProof] = struct{}{}
	}
	if event.CharacterName != "" && (event.CharacterNameStatus == resolutionVerified || event.CharacterNameStatus == resolutionCorrelated) {
		character.name, character.nameStatus = event.CharacterName, string(event.CharacterNameStatus)
	}
	character.eventCount++
	switch event.EventTypeCandidate {
	case "DAMAGE_CANDIDATE":
		character.damage += event.Damage
	case "HEALING_CANDIDATE":
		character.healingCandidate += event.Damage
	default:
		character.other += event.Damage
	}
	skillKey := skillSummaryKey{id: event.SkillID, eventType: event.EventTypeCandidate}
	skill := character.skills[skillKey]
	if skill == nil && len(character.skills) >= maxSummarySkillsPerCharacter {
		character.skillRowsOmitted++
	} else if skill == nil {
		skill = &skillSummary{id: event.SkillID, eventType: event.EventTypeCandidate, proofs: make(map[string]struct{})}
		character.skills[skillKey] = skill
	}
	if skill != nil && event.SkillProof != "" {
		skill.proofs[event.SkillProof] = struct{}{}
	}
	if skill != nil && event.SkillName != "" && (event.SkillNameStatus == resolutionVerified || event.SkillNameStatus == resolutionCorrelated) {
		skill.name, skill.nameState = event.SkillName, event.SkillNameStatus
	}
	if skill != nil {
		skill.amount += event.Damage
		skill.events++
	}
}

func (p *processor) backfillEncounterSummary(sourceID, roleID uint64) {
	if sourceID == 0 || roleID == 0 || p.masters == nil {
		return
	}
	character, ok := p.masters.characters[roleID]
	if !ok {
		return
	}
	pending := p.dev.pendingSummaryCredits[sourceID]
	if len(pending) == 0 {
		return
	}
	delete(p.dev.pendingSummaryCredits, sourceID)
	p.dev.pendingSummaryCreditCount -= len(pending)
	for _, item := range pending {
		summary := p.dev.encounterSummaries[item.key]
		if summary == nil || summary.unknownCredit <= 0 {
			continue
		}
		event := item.event
		event.CharacterID = character.ID
		event.CharacterStatus = resolutionCorrelated
		event.CharacterProof = "backfilled within observed session/scene/entity lifetime from 2+ distinct verified DamageContext.SkillId values mapped to RoleId through SkillDescription -> Skill"
		event.CharacterName = character.Name
		event.CharacterNameStatus = resolutionStatus(character.NameStatus)
		summary.unknownCredit--
		summary.unknownCreditAbs -= event.Damage
		removeUncreditedEvent(summary, event)
		summary.identityBackfilledEvents++
		addCreditedCharacterEvent(summary, event)
	}
}

func (p *processor) backfillAvailableSummaryCredits() {
	if p.dev.pendingSummaryCredits == nil {
		return
	}
	sourceIDs := make([]uint64, 0, len(p.dev.pendingSummaryCredits))
	for sourceID := range p.dev.pendingSummaryCredits {
		sourceIDs = append(sourceIDs, sourceID)
	}
	for _, sourceID := range sourceIDs {
		if roleID, correlated := p.sourceRoles.correlatedRole(sourceID); correlated {
			p.backfillEncounterSummary(sourceID, roleID)
		}
	}
}

func (p *processor) removePendingSummaryCredits(sourceID uint64) {
	if p.dev.pendingSummaryCredits == nil {
		return
	}
	p.dev.pendingSummaryCreditCount -= len(p.dev.pendingSummaryCredits[sourceID])
	delete(p.dev.pendingSummaryCredits, sourceID)
}

func (p *processor) resetPendingSummaryCredits() {
	p.dev.pendingSummaryCredits = nil
	p.dev.pendingSummaryCreditCount = 0
}

func isCreditedCharacterStatus(status resolutionStatus) bool {
	return status == resolutionVerified || status == resolutionCorrelated
}

func strongestEvidence(current, next resolutionStatus) resolutionStatus {
	if current == resolutionVerified || next == resolutionVerified {
		return resolutionVerified
	}
	if current == resolutionCorrelated || next == resolutionCorrelated {
		return resolutionCorrelated
	}
	return resolutionUnknown
}

func addBossCandidate(summary *encounterSummary, identity entityIdentity) {
	if !identity.HasMonster || identity.Monster.ID == 0 || !isCreditedCharacterStatus(identity.MonsterStatus) {
		return
	}
	key := bossCandidateKey{monsterID: identity.Monster.ID, entityID: identity.Runtime.entityID}
	if _, exists := summary.bossCandidates[key]; !exists && len(summary.bossCandidates) >= maxSummaryBossCandidates {
		summary.bossCandidatesOmitted++
		return
	}
	summary.bossCandidates[key] = bossCandidateSummary{
		monsterID: identity.Monster.ID, entityID: identity.Runtime.entityID,
		name: identity.Monster.Name, status: identity.MonsterStatus, proof: identity.MonsterProof,
	}
}

func (p *processor) writeEncounterSummary() error {
	if p.dev.encounterSummary == nil {
		return nil
	}
	p.backfillAvailableSummaryCredits()
	if _, err := fmt.Fprintln(p.dev.encounterSummary, "# Combat Attribution Summary"); err != nil {
		return err
	}
	if p.masters != nil {
		source := masterDatabaseSourceLabel(p.masters.databasePath)
		if p.masters.databasePath != "embedded" {
			source += ": " + filepath.Base(p.masters.databasePath)
		}
		if _, err := fmt.Fprintf(p.dev.encounterSummary, "\nCombat master: %s (Global, %s, SHA-256 %s)\n", source, p.masters.locale, p.masters.manifest.SourceHash); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(p.dev.encounterSummary, "\nAmounts are absolute CombatResult values. Damage and healing categories are master-based candidates. Healing is not verified as effective HP recovery, and overheal is unknown. Buff snapshot observations are listed separately from combat credit; no Echo or summon attribution is inferred here."); err != nil {
		return err
	}
	keys := make([]encounterSummaryKey, 0, len(p.dev.encounterSummaries))
	for key := range p.dev.encounterSummaries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].overflow != keys[j].overflow {
			return !keys[i].overflow
		}
		if keys[i].session != keys[j].session {
			return keys[i].session < keys[j].session
		}
		if keys[i].hasScene != keys[j].hasScene {
			return keys[i].hasScene
		}
		if keys[i].sceneInstance != keys[j].sceneInstance {
			return keys[i].sceneInstance < keys[j].sceneInstance
		}
		if keys[i].scopeConflict != keys[j].scopeConflict {
			return !keys[i].scopeConflict
		}
		return !keys[i].overflow && keys[j].overflow
	})
	if len(keys) == 0 {
		if _, err := fmt.Fprintln(p.dev.encounterSummary, "\nNo matched combat events were observed."); err != nil {
			return err
		}
	} else {
		for _, key := range keys {
			if err := writeOneEncounterSummary(p.dev.encounterSummary, p.dev.encounterSummaries[key]); err != nil {
				return err
			}
		}
	}
	if err := writeCombatActionRequests(p.dev.encounterSummary, p); err != nil {
		return err
	}
	return writeTimerObservations(p.dev.encounterSummary, p.dev.timerObservations)
}

func writeTimerObservations(w io.Writer, observations []timerObservation) error {
	if _, err := fmt.Fprintln(w, "\n## Challenge timers"); err != nil {
		return err
	}
	if len(observations) == 0 {
		_, err := fmt.Fprintln(w, "\n- No UpdateTimerInfoNotify timer was observed; remaining time UNKNOWN.")
		return err
	}
	for _, observation := range observations {
		if _, err := fmt.Fprintf(w, "\n- Session %d; %s; NodeId %s; EndTime %s; observed at frame %d (%s)",
			observation.sessionNumber, summaryTextLabel(observation.timerType), summaryInt64Label(observation.nodeID, observation.hasNodeID),
			summaryInt64Label(observation.endTime, observation.hasEndTime), observation.frameOrdinal, formatSummaryTime(observation.timestamp)); err != nil {
			return err
		}
		if observation.hasEndTime && observation.priorFlowTime > 0 && observation.nextFlowTime > 0 && observation.nextFlowTime >= observation.priorFlowTime {
			lowerMS := observation.endTime - observation.nextFlowTime
			upperMS := observation.endTime - observation.priorFlowTime
			if _, err := fmt.Fprintf(w, "; remaining at timer observation approximately %.3f-%.3f seconds [CORRELATED; bounded by adjacent FlowTime samples at frames %d and %d]\n",
				float64(lowerMS)/1000, float64(upperMS)/1000, observation.priorFlowFrame, observation.nextFlowFrame); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintln(w, "; remaining time UNKNOWN (no valid FlowTime bracket)."); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "\nFlowTime and EndTime share a candidate millisecond timebase in this capture. The range uses FlowTime samples immediately before and after each timer update; validate against the in-game display before treating it as exact UI time."); err != nil {
		return err
	}
	return nil
}

func summaryTextLabel(value string) string {
	if value == "" {
		return "UNKNOWN"
	}
	return value
}

func writeCombatActionRequests(w io.Writer, p *processor) error {
	if _, err := fmt.Fprintln(w, "\n## Outbound skill requests"); err != nil {
		return err
	}
	keys := make([]combatActionRequestKey, 0, len(p.dev.combatActionRequests))
	for key := range p.dev.combatActionRequests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].sessionNumber != keys[j].sessionNumber {
			return keys[i].sessionNumber < keys[j].sessionNumber
		}
		return keys[i].messageID < keys[j].messageID
	})
	linked, unlinked := 0, 0
	for _, key := range keys {
		if p.dev.combatActionRequests[key].LinkedDamageEvents > 0 {
			linked++
		} else {
			unlinked++
		}
	}
	if _, err := fmt.Fprintf(w, "\n- Captured skill requests: %d\n- Requests linked to one or more server damage results by exact MessageId: %d\n- Requests with no correlated damage result: %d (not sufficient to label a miss; may be non-damaging or unobserved)\n", len(keys), linked, unlinked); err != nil {
		return err
	}
	if len(keys) == 0 {
		_, err := fmt.Fprintln(w, "\n- No outbound skill requests were observed or decoded.")
		return err
	}
	for _, key := range keys {
		action := p.dev.combatActionRequests[key]
		status := "NO_CORRELATED_DAMAGE_RESULT"
		if action.LinkedDamageEvents > 0 {
			status = "LINKED_TO_SERVER_DAMAGE_RESULT"
		}
		if _, err := fmt.Fprintf(w, "\n- Session %d; %s; MessageId %d; EntityId %s; SkillId %d; TargetId %s; linked damage results %d\n",
			action.SessionNumber, status, action.MessageID, summaryIDLabel(action.EntityID, action.EntityID != 0), action.SkillID,
			summaryIDLabel(action.TargetID, action.TargetID != 0), action.LinkedDamageEvents); err != nil {
			return err
		}
	}
	return nil
}

func writeOneEncounterSummary(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintf(w, "\n## Session %d — ", summary.key.session); err != nil {
		return err
	}
	switch {
	case summary.key.overflow:
		if _, err := fmt.Fprintln(w, "Additional session/scene groups — bounded overflow aggregate (individual scopes omitted)"); err != nil {
			return err
		}
	case summary.key.scopeConflict:
		if _, err := fmt.Fprintln(w, "Scene scope CONFLICT (capture-session aggregate)"); err != nil {
			return err
		}
	case summary.key.hasScene:
		if _, err := fmt.Fprintf(w, "Scene Instance %d (entity scope; encounter segmentation unverified)\n", summary.key.sceneInstance); err != nil {
			return err
		}
	default:
		if _, err := fmt.Fprintln(w, "Session summary — encounter boundary UNKNOWN (capture-session aggregate)"); err != nil {
			return err
		}
	}
	if summary.key.scopeConflict {
		if _, err := fmt.Fprintln(w, "\nWarning: source and target carried different scene instance IDs; these events are grouped only in a conflict bucket."); err != nil {
			return err
		}
	}
	if summary.key.overflow {
		if _, err := fmt.Fprintln(w, "\nWarning: the configured summary-group limit was reached; later scene/session groups are combined here to keep live memory bounded."); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\n- Time range: %s to %s\n- Matched result events: %d\n- CombatResult absolute total: %d\n- Damage candidates: %d\n- Healing candidate result magnitude: %d\n- Other combat result candidates: %d\n- Unclassified result magnitude: %d\n- Events without resolved source character identity: %d (result magnitude %d; this does not imply non-player damage; player-team relation is tracked separately)\n- Earlier events backfilled from later same-lifetime Skill evidence: %d [CORRELATED]\n",
		formatSummaryTime(summary.startedAt), formatSummaryTime(summary.endedAt), summary.eventCount, summary.combatResultAbs,
		summary.damageCandidates, summary.healCandidates, summary.otherCandidates, summary.unclassified,
		summary.unknownCredit, summary.unknownCreditAbs, summary.identityBackfilledEvents); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "\n### Damage direction candidates\n\n- Player team to identified enemy: %d event(s), reported magnitude %d\n- Player team to Boss template candidate: %d event(s), reported magnitude %d\n- Enemy to player team: %d event(s), reported magnitude %d\n- Boss template candidate to player team: %d event(s), reported magnitude %d\n- Player-team direction unresolved: %d event(s), reported magnitude %d\n",
		summary.partyToEnemyCandidateEvents, summary.partyToEnemyCandidateAbs,
		summary.partyToBossCandidateEvents, summary.partyToBossCandidateAbs,
		summary.enemyToPartyCandidateEvents, summary.enemyToPartyCandidateAbs,
		summary.bossToPartyCandidateEvents, summary.bossToPartyCandidateAbs,
		summary.unknownDirectionDamageEvents, summary.unknownDirectionDamageAbs); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "These counts use observed runtime player-team ownership and identified enemy entities. Missing CharacterId or character names do not break a confirmed player-team relation. A zero count means no qualifying event was observed; it does not establish that the game dealt zero damage."); err != nil {
		return err
	}
	if summary.identityBackfillEventsOmitted > 0 {
		if _, err := fmt.Fprintf(w, "\nWarning: %d unresolved source events were not retained for identity backfill because the bounded pending-event limit was reached.\n", summary.identityBackfillEventsOmitted); err != nil {
			return err
		}
	}
	if err := writeVehicleContributors(w, summary); err != nil {
		return err
	}
	if err := writeUncreditedEvents(w, summary); err != nil {
		return err
	}
	if err := writeClassificationEvidence(w, summary); err != nil {
		return err
	}
	if err := writeBuffSnapshotObservations(w, summary); err != nil {
		return err
	}
	if err := writeBossCandidates(w, summary); err != nil {
		return err
	}
	if err := writeHealingAttributions(w, summary); err != nil {
		return err
	}
	if err := writeCharacterSummaries(w, summary); err != nil {
		return err
	}
	return writeOwnerCandidates(w, summary)
}

func writeUncreditedEvents(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintln(w, "\n### Unresolved source character identity breakdown (raw IDs)"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nAmounts are absolute CombatResult values grouped by observed numeric IDs. These rows do not establish a Character, Owner, or Summoner."); err != nil {
		return err
	}
	keys := make([]uncreditedEventKey, 0, len(summary.uncreditedEvents))
	for key := range summary.uncreditedEvents {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := summary.uncreditedEvents[keys[i]], summary.uncreditedEvents[keys[j]]
		if left.magnitude != right.magnitude {
			return left.magnitude > right.magnitude
		}
		if keys[i].eventType != keys[j].eventType {
			return keys[i].eventType < keys[j].eventType
		}
		if keys[i].sessionNumber != keys[j].sessionNumber {
			return keys[i].sessionNumber < keys[j].sessionNumber
		}
		if keys[i].skillID != keys[j].skillID {
			return keys[i].skillID < keys[j].skillID
		}
		if keys[i].damageID != keys[j].damageID {
			return keys[i].damageID < keys[j].damageID
		}
		if keys[i].sourceEntityID != keys[j].sourceEntityID {
			return keys[i].sourceEntityID < keys[j].sourceEntityID
		}
		if keys[i].targetEntityID != keys[j].targetEntityID {
			return keys[i].targetEntityID < keys[j].targetEntityID
		}
		// The remaining key fields make the order total: rows that tie on the fields above (different BulletIds or
		// Scenes of one skill and entity pair) no longer follow map order.
		return uncreditedEventKeyTail(keys[i]) < uncreditedEventKeyTail(keys[j])
	})
	if len(keys) == 0 {
		if _, err := fmt.Fprintln(w, "\n- None."); err != nil {
			return err
		}
	}
	for _, key := range keys {
		row := summary.uncreditedEvents[key]
		eventType := key.eventType
		if eventType == "" {
			eventType = "UNCLASSIFIED_RESULT"
		}
		if _, err := fmt.Fprintf(w, "\n- Session %d; source Scene %s; target Scene %s; %s; SkillId %s; DamageId %s; BulletId %s; Source EntityId %s; Target EntityId %s: %d event(s), magnitude %d; CharacterId UNKNOWN\n",
			key.sessionNumber, summaryIDLabel(key.sourceSceneID, key.hasSourceScene), summaryIDLabel(key.targetSceneID, key.hasTargetScene),
			eventType,
			summaryIDLabel(key.skillID, key.skillID != 0), summaryIDLabel(key.damageID, key.hasDamageID),
			summaryInt64Label(key.bulletID, key.hasBulletID), summaryIDLabel(key.sourceEntityID, key.hasSourceEntity),
			summaryIDLabel(key.targetEntityID, key.hasTargetEntity), row.events, row.magnitude); err != nil {
			return err
		}
	}
	if summary.uncreditedEventsOmitted > 0 {
		if _, err := fmt.Fprintf(w, "\n- Additional uncredited events omitted by the safety limit: %d.\n", summary.uncreditedEventsOmitted); err != nil {
			return err
		}
	}
	return nil
}

func summaryIDLabel(value uint64, present bool) string {
	if !present {
		return "UNKNOWN"
	}
	return fmt.Sprintf("%d", value)
}

func summaryInt64Label(value int64, present bool) string {
	if !present {
		return "UNKNOWN"
	}
	return fmt.Sprintf("%d", value)
}

func writeBuffSnapshotObservations(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintln(w, "\n### Buff snapshot observations"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nThese rows report Buff IDs observed in EntityActiveResponse snapshots. They do not establish apply, refresh, removal, uptime, caster, or ownership semantics."); err != nil {
		return err
	}
	keys := make([]buffObservationKey, 0, len(summary.buffObservations))
	for key := range summary.buffObservations {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].hasBuffID != keys[j].hasBuffID {
			return keys[i].hasBuffID
		}
		if keys[i].buffID != keys[j].buffID {
			return keys[i].buffID < keys[j].buffID
		}
		if keys[i].hasTargetID != keys[j].hasTargetID {
			return keys[i].hasTargetID
		}
		return keys[i].targetID < keys[j].targetID
	})
	if len(keys) == 0 {
		if _, err := fmt.Fprintln(w, "\n- None observed."); err != nil {
			return err
		}
	}
	for _, key := range keys {
		buffID, targetID := "UNKNOWN", "UNKNOWN"
		if key.hasBuffID {
			buffID = fmt.Sprintf("%d", key.buffID)
		}
		if key.hasTargetID {
			targetID = fmt.Sprintf("%d", key.targetID)
		}
		if _, err := fmt.Fprintf(w, "\n- BuffId %s / EntityId %s: %d snapshot observation(s)\n", buffID, targetID, summary.buffObservations[key]); err != nil {
			return err
		}
	}
	if summary.buffObservationsOmitted > 0 {
		if _, err := fmt.Fprintf(w, "\n- Additional distinct Buff/Entity observation pairs omitted by the safety limit: %d.\n", summary.buffObservationsOmitted); err != nil {
			return err
		}
	}
	return nil
}

func writeClassificationEvidence(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintln(w, "\nClassification evidence:"); err != nil {
		return err
	}
	evidence := make([]string, 0, len(summary.classificationEvidence))
	for item := range summary.classificationEvidence {
		evidence = append(evidence, item)
	}
	sort.Strings(evidence)
	if len(evidence) == 0 {
		_, err := fmt.Fprintln(w, "\n- UNKNOWN: no versioned Damage master classification was available.")
		return err
	}
	for _, item := range evidence {
		if _, err := fmt.Fprintf(w, "\n- %s\n", markdownText(item)); err != nil {
			return err
		}
	}
	if summary.classificationEvidenceOmitted > 0 {
		if _, err := fmt.Fprintf(w, "\n- Additional distinct classification evidence rows omitted by the safety limit: %d\n", summary.classificationEvidenceOmitted); err != nil {
			return err
		}
	}
	return nil
}

func writeBossCandidates(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintln(w, "\n### Monster candidates (numeric master mapping)"); err != nil {
		return err
	}
	keys := make([]bossCandidateKey, 0, len(summary.bossCandidates))
	for key := range summary.bossCandidates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].monsterID != keys[j].monsterID {
			return keys[i].monsterID < keys[j].monsterID
		}
		return keys[i].entityID < keys[j].entityID
	})
	if len(keys) == 0 {
		_, err := fmt.Fprintln(w, "\n- None resolved from observed entity/master IDs.")
		return err
	}
	for _, key := range keys {
		candidate := summary.bossCandidates[key]
		if _, err := fmt.Fprintf(w, "\n- MonsterId %d / EntityId %d — %s [%s]; evidence: %s\n", candidate.monsterID, candidate.entityID, markdownText(candidate.name), candidate.status, valueOrUnknown(candidate.proof)); err != nil {
			return err
		}
	}
	if summary.bossCandidatesOmitted > 0 {
		if _, err := fmt.Fprintf(w, "\n- Additional monster candidate rows omitted by the safety limit: %d.\n", summary.bossCandidatesOmitted); err != nil {
			return err
		}
	}
	return nil
}

func writeHealingAttributions(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintln(w, "\n### Healing source-to-target attribution candidates"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\nAmounts are reported absolute CombatResult magnitudes for HEALING_CANDIDATE events; they are not effective HP changes. This table does not compute Effective Healing or Overheal; the encounter statistics take the applied HP change from ChangeLife."); err != nil {
		return err
	}
	keys := make([]healingAttributionKey, 0, len(summary.healingAttributions))
	for key := range summary.healingAttributions {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := keys[i], keys[j]
		if left.healerID != right.healerID {
			return left.healerID < right.healerID
		}
		if left.targetID != right.targetID {
			return left.targetID < right.targetID
		}
		if left.hasSkillID != right.hasSkillID {
			return left.hasSkillID
		}
		if left.skillID != right.skillID {
			return left.skillID < right.skillID
		}
		if left.hasDamageID != right.hasDamageID {
			return left.hasDamageID
		}
		return left.damageID < right.damageID
	})
	if len(keys) == 0 {
		if _, err := fmt.Fprintln(w, "\n- No healing events had both a credited source CharacterId and a resolved target CharacterId."); err != nil {
			return err
		}
	}
	for _, key := range keys {
		row := summary.healingAttributions[key]
		skillID := summaryIDLabel(key.skillID, key.hasSkillID)
		if key.hasSkillID {
			skillID = fmt.Sprintf("%d [%s]", key.skillID, row.skillStatus)
			if row.skillName != "" {
				skillID += fmt.Sprintf(" — %s [name %s]", markdownText(row.skillName), valueOrUnknown(string(row.skillNameStatus)))
			}
		}
		damageID := summaryIDLabel(key.damageID, key.hasDamageID)
		if _, err := fmt.Fprintf(w, "\n- Source CharacterId %d — %s [%s; name %s] → Target CharacterId %d — %s [%s; name %s]; SkillId %s; DamageId %s: %d event(s), reported magnitude %d; classification %s; source evidence: %s; target evidence: %s; skill evidence: %s\n",
			key.healerID, markdownText(valueOrUnknown(row.healerName)), row.healerStatus, valueOrUnknown(string(row.healerNameStatus)),
			key.targetID, markdownText(valueOrUnknown(row.targetName)), row.targetStatus, valueOrUnknown(string(row.targetNameStatus)),
			skillID, damageID, row.events, row.reportedMagnitudeAbs, row.classificationStatus,
			markdownText(valueOrUnknown(joinEvidence(row.healerProofs))), markdownText(valueOrUnknown(joinEvidence(row.targetProofs))),
			markdownText(valueOrUnknown(joinEvidence(row.skillProofs)))); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  Classification evidence: %s\n", markdownText(valueOrUnknown(joinEvidence(row.classificationProofs)))); err != nil {
			return err
		}
	}
	if summary.healingAttributionUnresolved > 0 {
		if _, err := fmt.Fprintf(w, "\n- Healing events without both source and target Character identities: %d (reported magnitude %d)\n",
			summary.healingAttributionUnresolved, summary.healingAttributionUnresolvedAbs); err != nil {
			return err
		}
	}
	if summary.healingAttributionsOmitted > 0 {
		if _, err := fmt.Fprintf(w, "\n- Additional healing attribution rows omitted by the safety limit: %d; candidate totals above still include these events.\n", summary.healingAttributionsOmitted); err != nil {
			return err
		}
	}
	return nil
}

func writeCharacterSummaries(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintln(w, "\n### Character credit summaries"); err != nil {
		return err
	}
	ids := make([]uint64, 0, len(summary.characters))
	for id := range summary.characters {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		_, err := fmt.Fprintln(w, "\n- No events had a VERIFIED or CORRELATED CharacterId credit.")
		return err
	}
	for _, id := range ids {
		character := summary.characters[id]
		if _, err := fmt.Fprintf(w, "\n#### CharacterId %d — %s [%s; name %s]\n\n- Damage candidate total: %d\n- Healing candidate result magnitude: %d\n- Other credited combat result magnitude: %d\n- Credited events: %d\n- Credit evidence: %s\n\nSkill breakdown (numeric SkillId):\n",
			id, markdownText(character.name), character.status, valueOrUnknown(character.nameStatus), character.damage,
			character.healingCandidate, character.other, character.eventCount, markdownText(valueOrUnknown(joinEvidence(character.proofs)))); err != nil {
			return err
		}
		keys := make([]skillSummaryKey, 0, len(character.skills))
		for key := range character.skills {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].id != keys[j].id {
				return keys[i].id < keys[j].id
			}
			return keys[i].eventType < keys[j].eventType
		})
		for _, key := range keys {
			skill := character.skills[key]
			idLabel := "UNKNOWN"
			if skill.id != 0 {
				idLabel = fmt.Sprintf("%d", skill.id)
			}
			if _, err := fmt.Fprintf(w, "\n- SkillId %s — %s [%s; name %s]: %s amount %d across %d events; master evidence: %s\n", idLabel, markdownText(skill.name), skill.eventType, valueOrUnknown(string(skill.nameState)), skill.eventType, skill.amount, skill.events, markdownText(valueOrUnknown(joinEvidence(skill.proofs)))); err != nil {
				return err
			}
		}
		if character.skillRowsOmitted > 0 {
			if _, err := fmt.Fprintf(w, "\n- Skill breakdown rows omitted by the safety limit: %d; character totals above still include these events.\n", character.skillRowsOmitted); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeOwnerCandidates(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintln(w, "\n### Separate owner/summoner candidates (not credited)"); err != nil {
		return err
	}
	ids := make([]uint64, 0, len(summary.ownerCandidates))
	for id := range summary.ownerCandidates {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		_, err := fmt.Fprintln(w, "\n- None observed.")
		return err
	}
	for _, id := range ids {
		candidate := summary.ownerCandidates[id]
		if _, err := fmt.Fprintf(w, "\n- CharacterId %d — %s [%s]: associated with %d events (result magnitude %d); retained separately from Damage Credit. Evidence: %s\n", id, markdownText(candidate.name), candidate.status, candidate.events, candidate.amount, valueOrUnknown(candidate.proof)); err != nil {
			return err
		}
	}
	if summary.ownerCandidatesOmitted > 0 {
		if _, err := fmt.Fprintf(w, "\n- Additional owner/summoner candidate events omitted by the safety limit: %d.\n", summary.ownerCandidatesOmitted); err != nil {
			return err
		}
	}
	return nil
}

func formatSummaryTime(value time.Time) string {
	if value.IsZero() {
		return "UNKNOWN"
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func valueOrUnknown(value string) string {
	if value == "" {
		return string(resolutionUnknown)
	}
	return value
}

func markdownText(value string) string {
	if value == "" {
		return "(name unavailable)"
	}
	return strings.NewReplacer("|", "\\|", "\r", " ", "\n", " ").Replace(value)
}

func joinEvidence(values map[string]struct{}) string {
	if len(values) == 0 {
		return string(resolutionUnknown)
	}
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	return strings.Join(items, "; ")
}
