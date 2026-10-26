//go:build wwdev

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

func (p *processor) observeEncounterSummary(event combatEvent) {
	if p.encounterSummary == nil {
		return
	}
	if p.encounterSummaries == nil {
		p.encounterSummaries = make(map[encounterSummaryKey]*encounterSummary)
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
	if _, exists := p.encounterSummaries[key]; !exists && len(p.encounterSummaries) >= maxEncounterSummaryGroups {
		key = encounterSummaryKey{overflow: true}
	}
	summary := p.encounterSummaries[key]
	if summary == nil {
		summary = &encounterSummary{
			key: key, characters: make(map[uint64]*characterSummary),
			ownerCandidates:        make(map[uint64]*ownerCandidateSummary),
			bossCandidates:         make(map[bossCandidateKey]bossCandidateSummary),
			buffObservations:       make(map[buffObservationKey]int),
			uncreditedEvents:       make(map[uncreditedEventKey]*uncreditedEventSummary),
			classificationEvidence: make(map[string]struct{}),
		}
		p.encounterSummaries[key] = summary
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
	case "HEALING_CANDIDATE":
		summary.healCandidates += event.Damage
	case "OTHER_COMBAT_RESULT_CANDIDATE":
		summary.otherCandidates += event.Damage
	default:
		summary.unclassified += event.Damage
	}

	if isCreditedCharacterStatus(event.CharacterStatus) && event.CharacterID != 0 {
		addCreditedCharacterEvent(summary, event)
	} else {
		summary.unknownCredit++
		summary.unknownCreditAbs += event.Damage
		addUncreditedEvent(summary, event)
		if event.HasSourceEntity && event.SourceEntityID != 0 {
			if p.pendingSummaryCreditCount < maxPendingSummaryCreditEvents {
				if p.pendingSummaryCredits == nil {
					p.pendingSummaryCredits = make(map[uint64][]pendingSummaryCredit)
				}
				p.pendingSummaryCredits[event.SourceEntityID] = append(p.pendingSummaryCredits[event.SourceEntityID], pendingSummaryCredit{key: key, event: event})
				p.pendingSummaryCreditCount++
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
	if p.encounterSummary == nil {
		return
	}
	if p.encounterSummaries == nil {
		p.encounterSummaries = make(map[encounterSummaryKey]*encounterSummary)
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
	if _, exists := p.encounterSummaries[key]; !exists && len(p.encounterSummaries) >= maxEncounterSummaryGroups {
		key = encounterSummaryKey{overflow: true}
	}
	summary := p.encounterSummaries[key]
	if summary == nil {
		summary = &encounterSummary{
			key: key, characters: make(map[uint64]*characterSummary),
			ownerCandidates:        make(map[uint64]*ownerCandidateSummary),
			bossCandidates:         make(map[bossCandidateKey]bossCandidateSummary),
			buffObservations:       make(map[buffObservationKey]int),
			classificationEvidence: make(map[string]struct{}),
		}
		p.encounterSummaries[key] = summary
	}
	if _, exists := summary.buffObservations[observation]; !exists && len(summary.buffObservations) >= maxSummaryBuffObservations {
		summary.buffObservationsOmitted++
		return
	}
	summary.buffObservations[observation]++
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
	pending := p.pendingSummaryCredits[sourceID]
	if len(pending) == 0 {
		return
	}
	delete(p.pendingSummaryCredits, sourceID)
	p.pendingSummaryCreditCount -= len(pending)
	for _, item := range pending {
		summary := p.encounterSummaries[item.key]
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
	if p.pendingSummaryCredits == nil {
		return
	}
	sourceIDs := make([]uint64, 0, len(p.pendingSummaryCredits))
	for sourceID := range p.pendingSummaryCredits {
		sourceIDs = append(sourceIDs, sourceID)
	}
	for _, sourceID := range sourceIDs {
		if roleID, correlated := p.sourceRoles.correlatedRole(sourceID); correlated {
			p.backfillEncounterSummary(sourceID, roleID)
		}
	}
}

func (p *processor) removePendingSummaryCredits(sourceID uint64) {
	if p.pendingSummaryCredits == nil {
		return
	}
	p.pendingSummaryCreditCount -= len(p.pendingSummaryCredits[sourceID])
	delete(p.pendingSummaryCredits, sourceID)
}

func (p *processor) resetPendingSummaryCredits() {
	p.pendingSummaryCredits = nil
	p.pendingSummaryCreditCount = 0
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
	if p.encounterSummary == nil {
		return nil
	}
	p.backfillAvailableSummaryCredits()
	if _, err := fmt.Fprintln(p.encounterSummary, "# Combat Attribution Summary"); err != nil {
		return err
	}
	if p.masters != nil {
		if _, err := fmt.Fprintf(p.encounterSummary, "\nMaster version: %s (Global, %s)\n", p.masters.manifest.GameVersion, p.masters.manifest.Language); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(p.encounterSummary, "\nAmounts are absolute CombatResult values. Damage and healing categories are master-based candidates. Healing is not verified as effective HP recovery, and overheal is unknown. Buff snapshot observations are listed separately from combat credit; no Echo or summon attribution is inferred here."); err != nil {
		return err
	}
	keys := make([]encounterSummaryKey, 0, len(p.encounterSummaries))
	for key := range p.encounterSummaries {
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
		_, err := fmt.Fprintln(p.encounterSummary, "\nNo matched combat events were observed.")
		return err
	}
	for _, key := range keys {
		if err := writeOneEncounterSummary(p.encounterSummary, p.encounterSummaries[key]); err != nil {
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
	if _, err := fmt.Fprintf(w, "\n- Time range: %s to %s\n- Matched result events: %d\n- CombatResult absolute total: %d\n- Damage candidates: %d\n- Healing candidate result magnitude: %d\n- Other combat result candidates: %d\n- Unclassified result magnitude: %d\n- Events without credited CharacterId: %d (result magnitude %d)\n- Earlier events backfilled from later same-lifetime Skill evidence: %d [CORRELATED]\n",
		formatSummaryTime(summary.startedAt), formatSummaryTime(summary.endedAt), summary.eventCount, summary.combatResultAbs,
		summary.damageCandidates, summary.healCandidates, summary.otherCandidates, summary.unclassified,
		summary.unknownCredit, summary.unknownCreditAbs, summary.identityBackfilledEvents); err != nil {
		return err
	}
	if summary.identityBackfillEventsOmitted > 0 {
		if _, err := fmt.Fprintf(w, "\nWarning: %d unresolved source events were not retained for identity backfill because the bounded pending-event limit was reached.\n", summary.identityBackfillEventsOmitted); err != nil {
			return err
		}
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
	if err := writeCharacterSummaries(w, summary); err != nil {
		return err
	}
	return writeOwnerCandidates(w, summary)
}

func writeUncreditedEvents(w io.Writer, summary *encounterSummary) error {
	if _, err := fmt.Fprintln(w, "\n### Uncredited result breakdown (raw IDs)"); err != nil {
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
		return keys[i].targetEntityID < keys[j].targetEntityID
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
