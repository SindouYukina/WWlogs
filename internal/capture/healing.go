package capture

import (
	"sort"
	"time"
)

// Healing statistics.
//
// A healing result is a DamageExecute result the Master classifies as healing (CalculateType 1). Its Amount is the
// magnitude the server reported; AppliedHealing is the HP the server applied, the response's ChangeLife negated, and
// zero when ChangeLife is absent (the target was already at full HP). In 39 archived captures these were checked
// against the server's own Life attribute reports: between two Life reports of one entity, Life changed by exactly the
// ChangeLife of the results observed in between in 137 of 139 intervals with healing (the other two lost HP to damage
// that was not observed), every zero-ChangeLife healing result with a known Life found its target at full HP, and no
// result other than healing ever raised HP. Healing that does not arrive as a DamageExecute result (revival, natural
// regeneration) is not a healing result and is not counted.
//
// The local team's healing is a healing result from a local source to a local target. Healing never takes part in
// encounter boundaries, encounter activity or the statistics clock: an encounter's healing is the healing observed in
// its own protocol session within its own span [start, end], associated when the encounter is rendered and frozen when
// it finishes.

// HealingTotals is the local team's healing. Effective is the HP applied; Reported is the magnitude the results
// carried; Overheal is Reported minus Effective. UnknownHealer is the part whose healer's role could not be resolved;
// it is still the local team's healing.
type HealingTotals struct {
	Effective              int64          `json:"effective"`
	Reported               int64          `json:"reported"`
	Overheal               int64          `json:"overheal"`
	Events                 uint64         `json:"events"`
	UnknownHealerEffective int64          `json:"unknownHealerEffective"`
	UnknownHealerEvents    uint64         `json:"unknownHealerEvents"`
	ByHealer               []HealerTotals `json:"byHealer"`
}

// HealerTotals is one healer role's share of HealingTotals.
type HealerTotals struct {
	PlayerID  uint64 `json:"playerId"`
	RoleID    uint64 `json:"roleId"`
	Effective int64  `json:"effective"`
	Reported  int64  `json:"reported"`
	Events    uint64 `json:"events"`
}

// HealingResult is an encounter's healing with how far it can be trusted. Status follows the encounter's data integrity
// as Encounter DPS does: available, lower_bound when the data is DEGRADED or some healing evidence was incomplete, and
// unreliable when the data is UNRELIABLE; the totals are then kept for diagnostics but must not be shown.
type HealingResult struct {
	Status DPSStatus `json:"status"`
	Reason string    `json:"reason,omitempty"`
	HealingTotals
}

// maxEncounterHealingEvents bounds the healing results kept for encounter association in one statistics interval.
const maxEncounterHealingEvents = 16384

type healingEvent struct {
	session      int
	at           time.Time
	applied      int64
	reported     int64
	healer       *CombatRole
	inconsistent bool
}

func healingEventOf(record CombatEventRecord) healingEvent {
	return healingEvent{
		session: record.Key.ProtocolSession, at: record.ObservedAt, applied: record.AppliedHealing, reported: record.Amount,
		healer: record.SourceRole, inconsistent: record.HealingInconsistent,
	}
}

// isLocalHealing reports whether a record is the local team's healing: a healing result from a local source to a local
// target that the attribution gate did not hold back.
func isLocalHealing(record CombatEventRecord) bool {
	return record.Kind == CombatEventHealing && record.Flow == CombatFlowLocalInternal && record.ContextDowngrade == ""
}

// healingAccumulator sums healing events into HealingTotals.
type healingAccumulator struct {
	totals       HealingTotals
	healers      map[[2]uint64]*HealerTotals
	inconsistent uint64
}

func (a *healingAccumulator) add(event healingEvent) {
	if event.inconsistent {
		a.inconsistent++
	}
	a.totals.Effective += event.applied
	a.totals.Reported += event.reported
	a.totals.Overheal += max(0, event.reported-event.applied)
	a.totals.Events++
	if event.healer == nil || event.healer.RoleID == 0 {
		a.totals.UnknownHealerEffective += event.applied
		a.totals.UnknownHealerEvents++
		return
	}
	if a.healers == nil {
		a.healers = make(map[[2]uint64]*HealerTotals)
	}
	key := [2]uint64{event.healer.PlayerID, event.healer.RoleID}
	healer := a.healers[key]
	if healer == nil {
		healer = &HealerTotals{PlayerID: key[0], RoleID: key[1]}
		a.healers[key] = healer
	}
	healer.Effective += event.applied
	healer.Reported += event.reported
	healer.Events++
}

// result returns the totals with healers by PlayerId then RoleId.
func (a *healingAccumulator) result() HealingTotals {
	totals := a.totals
	totals.ByHealer = make([]HealerTotals, 0, len(a.healers))
	for _, healer := range a.healers {
		totals.ByHealer = append(totals.ByHealer, *healer)
	}
	sort.Slice(totals.ByHealer, func(i, j int) bool {
		if totals.ByHealer[i].PlayerID != totals.ByHealer[j].PlayerID {
			return totals.ByHealer[i].PlayerID < totals.ByHealer[j].PlayerID
		}
		return totals.ByHealer[i].RoleID < totals.ByHealer[j].RoleID
	})
	return totals
}

// observeHealing keeps one local healing result for encounter association. It does not move the tracker's clock and
// cannot open, extend, or end an encounter.
func (t *encounterTracker) observeHealing(record CombatEventRecord) {
	if record.ObservedAt.IsZero() {
		// Without a time it belongs to no encounter; the interval totals still count it.
		return
	}
	if len(t.healing) >= maxEncounterHealingEvents {
		t.healing = t.healing[1:]
		t.healingDropped++
	}
	t.healing = append(t.healing, healingEventOf(record))
}

// encounterHealing is the healing observed in the encounter's protocol session within [start, end], before data
// integrity is applied.
func (t *encounterTracker) encounterHealing(record *encounterRecord, end time.Time) HealingResult {
	var sum healingAccumulator
	for _, event := range t.healing {
		if event.session != record.protocolSession || event.at.Before(record.startAt) || event.at.After(end) {
			continue
		}
		sum.add(event)
	}
	result := HealingResult{Status: DPSStatusAvailable, HealingTotals: sum.result()}
	switch {
	case sum.inconsistent > 0:
		result.Status, result.Reason = DPSStatusLowerBound, "healing_result_lowered_hp"
	case t.healingDropped > 0:
		result.Status, result.Reason = DPSStatusLowerBound, "healing_capacity_exceeded"
	}
	return result
}

// pruneHealing forgets healing results no later encounter can include: those of older protocol sessions, and those of
// the finished encounter's session at or before its end, which is the new boundary floor.
func (t *encounterTracker) pruneHealing(record *encounterRecord) {
	kept := t.healing[:0]
	for _, event := range t.healing {
		if event.session < record.protocolSession || (event.session == record.protocolSession && !event.at.After(record.endAt)) {
			continue
		}
		kept = append(kept, event)
	}
	t.healing = kept
}

// applyIntegrityToHealing applies the encounter's data integrity the way applyIntegrityToDPS does.
func applyIntegrityToHealing(result HealingResult, integrity DataIntegrity) HealingResult {
	if result.Status == DPSStatusUnreliable {
		return result
	}
	switch integrity.Status {
	case IntegrityUnreliable:
		result.Status, result.Reason = DPSStatusUnreliable, "data_integrity_unreliable"
	case IntegrityDegraded:
		if result.Status == DPSStatusAvailable {
			result.Status, result.Reason = DPSStatusLowerBound, "data_integrity_degraded"
		}
	}
	return result
}

// intervalHealing is the interval's local healing with the interval's integrity applied.
func intervalHealing(statistics DamageStatisticsSnapshot) HealingResult {
	result := HealingResult{Status: DPSStatusAvailable, HealingTotals: statistics.Healing}
	if statistics.HealingInconsistentEvents > 0 {
		result.Status, result.Reason = DPSStatusLowerBound, "healing_result_lowered_hp"
	}
	return applyIntegrityToHealing(result, statistics.Integrity)
}

// gateHealing holds the local team's healing back while local-player attribution is suspect after a transport loss,
// as gateAttribution does for damage, unless the client has referenced both entities since. It changes none of the
// damage gate's counters.
func (p *processor) gateHealing(record CombatEventRecord) CombatEventRecord {
	if !p.context.attribution.suspect || (p.context.revalidatesEntity(record.Source.EntityID) && p.context.revalidatesEntity(record.Target.EntityID)) {
		return record
	}
	record.ContextDowngrade = contextDowngradeAttribution
	return record
}
