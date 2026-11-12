package capture

import (
	"hash/fnv"
	"sort"
	"strconv"
	"time"
)

// ViewSnapshot is the product UI's view of one capture session at one moment. It is built on the capture goroutine
// from the statistics engine's own snapshots, holds no reference into processor state, and is never modified after it
// is built, so any number of readers may share it.
//
// It is a curated projection: aggregate statistics, encounter state and integrity, role-level totals, health, and the
// tracked effects. It carries no per-event records, RPC identifiers, or effect handles, so it is the same in both
// build profiles and stays within the Public Build boundary.
type ViewSnapshot struct {
	SchemaVersion        int    `json:"schemaVersion"`
	CaptureSessionID     uint64 `json:"captureSessionId"`
	StatisticsIntervalID uint64 `json:"statisticsIntervalId"`
	// ProtocolSession counts game handshakes in this capture session; 0 before the first one.
	ProtocolSession int `json:"protocolSession"`
	// CaptureObservedAt is the newest capture-observed frame time; zero before the first decoded frame.
	CaptureObservedAt time.Time `json:"captureObservedAt"`
	// Final is set on the last snapshot of a capture session, after the capture finished.
	Final bool `json:"final"`

	Health              ViewHealth           `json:"health"`
	Statistics          ViewStatistics       `json:"statistics"`
	Encounters          ViewEncounters       `json:"encounters"`
	Roles               []ViewRole           `json:"roles"`
	UnassignedRole      UnassignedRoleTotals `json:"unassignedRole"`
	UnassignedRoleShare *float64             `json:"unassignedRoleShare,omitempty"`
	Vehicle             *ViewVehicle         `json:"vehicle,omitempty"`
	// Completeness describes this capture interval's damage; each encounter carries its own.
	Completeness ViewDamageCompleteness `json:"completeness"`
	Effects      ViewEffects            `json:"effects"`
	// Timeline is the DPS over time of the encounter the UI shows (Encounters.Current, else the newest of
	// Encounters.Recent), at most maxViewTimelinePoints points; absent before the first encounter.
	Timeline *EncounterTimelineSnapshot `json:"timeline,omitempty"`
}

// ViewSnapshotSchemaVersion changes whenever a field changes meaning or is removed.
const ViewSnapshotSchemaVersion = 3

// View bounds keep a snapshot small regardless of how long a session runs.
const (
	maxViewRecentEncounters = 10
	maxViewEffects          = 128
	// maxViewTimelinePoints keeps the chart payload small; longer encounters use a wider display interval, and the
	// engine's 1-second intervals stay exact underneath.
	maxViewTimelinePoints = 120
)

// ViewHealth is the capture, transport, and context state behind the numbers.
type ViewHealth struct {
	Capture   ViewCaptureHealth       `json:"capture"`
	Transport TransportHealthSnapshot `json:"transport"`
	Context   ContextValiditySnapshot `json:"context"`
	// DataIntegrity is the integrity of the statistics interval: INTACT, DEGRADED, or UNRELIABLE.
	DataIntegrity IntegrityStatus `json:"dataIntegrity"`
	// RPCPairing reports damage request/response pairing; unpaired responses are never counted.
	RPCPairing ViewRPCPairing `json:"rpcPairing"`
	// Session is evidence about the game protocol session; it holds no key material.
	Session ViewSessionHealth `json:"session"`
}

// ViewCaptureHealth is the capture-layer subset the UI needs.
type ViewCaptureHealth struct {
	Source             string         `json:"source"`
	PacketsRead        uint64         `json:"packetsRead"`
	KernelReceived     CaptureCounter `json:"kernelReceived"`
	KernelDropped      CaptureCounter `json:"kernelDropped"`
	ApplicationDropped CaptureCounter `json:"applicationDropped"`
	DeliveryDelay      CaptureLatency `json:"deliveryDelay"`
	DiagnosticDropped  CaptureCounter `json:"diagnosticDropped"`
}

// ViewRPCPairing is the pairing counter subset the UI needs.
type ViewRPCPairing struct {
	Requests         int    `json:"requests"`
	Paired           uint64 `json:"paired"`
	PairedOutOfOrder uint64 `json:"pairedOutOfOrder"`
	Unpaired         uint64 `json:"unpaired"`
	HeldResponses    int    `json:"heldResponses"`
}

// ViewDPS is a DPS result with its duration in seconds. Value is meaningful only for status available and
// lower_bound; the UI must show the status, never a bare number, for anything else.
type ViewDPS struct {
	Status          DPSStatus `json:"status"`
	Reason          string    `json:"reason,omitempty"`
	Damage          int64     `json:"damage"`
	DurationSeconds float64   `json:"durationSeconds"`
	Value           float64   `json:"value"`
	Confidence      string    `json:"confidence,omitempty"`
}

// ViewStatistics is the interval's confirmed and unattributed totals.
type ViewStatistics struct {
	DamageDealt     int64            `json:"damageDealt"`
	DamageTaken     int64            `json:"damageTaken"`
	OutgoingEvents  uint64           `json:"outgoingEvents"`
	IncomingEvents  uint64           `json:"incomingEvents"`
	Unattributed    ViewUnattributed `json:"unattributed"`
	DuplicateEvents uint64           `json:"duplicateEvents"`
	Integrity       DataIntegrity    `json:"integrity"`
	EncounterDPS    ViewDPS          `json:"encounterDps"`
	DamageSpanDPS   ViewDPS          `json:"damageSpanDps"`
	Rolling5sDPS    ViewDPS          `json:"rolling5sDps"`
	Rolling10sDPS   ViewDPS          `json:"rolling10sDps"`
	// Healing is the interval's local healing, with the interval's integrity applied.
	Healing ViewHealing `json:"healing"`
}

// ViewHealing is the local team's healing. Reported is the magnitude of the server-confirmed healing results, the summary
// card's main value; Effective is the HP actually applied, shown beneath it. Status follows data integrity as ViewDPS does,
// and the UI must show the status, never a bare number, for unreliable.
type ViewHealing struct {
	Status                 DPSStatus `json:"status"`
	Reason                 string    `json:"reason,omitempty"`
	Effective              int64     `json:"effective"`
	Reported               int64     `json:"reported"`
	Overheal               int64     `json:"overheal"`
	Events                 uint64    `json:"events"`
	UnknownHealerEffective int64     `json:"unknownHealerEffective"`
	UnknownHealerEvents    uint64    `json:"unknownHealerEvents"`
}

func viewHealing(result HealingResult) ViewHealing {
	return ViewHealing{
		Status: result.Status, Reason: result.Reason, Effective: result.Effective, Reported: result.Reported, Overheal: result.Overheal,
		Events: result.Events, UnknownHealerEffective: result.UnknownHealerEffective, UnknownHealerEvents: result.UnknownHealerEvents,
	}
}

// ViewUnattributed is damage that was observed but could not be confirmed as local; it is never part of the totals.
type ViewUnattributed struct {
	OutgoingDamage int64  `json:"outgoingDamage"`
	IncomingDamage int64  `json:"incomingDamage"`
	OutgoingEvents uint64 `json:"outgoingEvents"`
	IncomingEvents uint64 `json:"incomingEvents"`
}

// ViewEncounters is the encounter tracker state.
type ViewEncounters struct {
	State       EncounterState  `json:"state"`
	BattleState string          `json:"battleState"`
	Current     *ViewEncounter  `json:"current,omitempty"`
	Recent      []ViewEncounter `json:"recent"`
	// Older finished encounters are not listed; CompletedTotal counts every finished encounter of the interval.
	CompletedTotal uint64 `json:"completedTotal"`
}

// ViewEncounter is one encounter.
type ViewEncounter struct {
	ElapsedSeconds      *float64             `json:"elapsedSeconds,omitempty"`
	EncounterID         string               `json:"encounterId"`
	Sequence            uint64               `json:"sequence"`
	ProtocolSession     int                  `json:"protocolSession"`
	State               EncounterState       `json:"state"`
	Outcome             EncounterOutcome     `json:"outcome"`
	StartedAt           time.Time            `json:"startedAt"`
	StartBasis          string               `json:"startBasis"`
	EndedAt             time.Time            `json:"endedAt"`
	EndBasis            string               `json:"endBasis,omitempty"`
	Totals              EncounterTotals      `json:"totals"`
	Integrity           DataIntegrity        `json:"integrity"`
	DPS                 ViewDPS              `json:"dps"`
	PeakDPS             ViewDPS              `json:"peakDps"`
	Healing             ViewHealing          `json:"healing"`
	Roles               []ViewRole           `json:"roles"`
	UnassignedRole      UnassignedRoleTotals `json:"unassignedRole"`
	UnassignedRoleShare *float64             `json:"unassignedRoleShare,omitempty"`
	Vehicle             *ViewVehicle         `json:"vehicle,omitempty"`
	// Completeness describes this encounter's damage only, never another encounter of the interval.
	Completeness ViewDamageCompleteness `json:"completeness"`
}

// ViewRole is one local role's totals. Name comes from the Master database for the selected locale; it is empty with
// NameStatus UNKNOWN when the RoleId has no verified name.
type ViewRole struct {
	PlayerID   uint64 `json:"playerId"`
	RoleID     uint64 `json:"roleId"`
	Name       string `json:"name"`
	NameStatus string `json:"nameStatus"`
	// ElementID is a Master lookup filled by the display projection; zero means unknown. It never affects statistics.
	ElementID   uint64   `json:"elementId,omitempty"`
	DamageDealt int64    `json:"damageDealt"`
	DamageShare *float64 `json:"damageShare,omitempty"`
	// Rank and DPS are this row's place and rate within the scope that holds it (rankViewContributors).
	Rank           int      `json:"rank"`
	DPS            *float64 `json:"dps,omitempty"`
	DirectDamage   int64    `json:"directDamage"`
	SummonDamage   int64    `json:"summonDamage"`
	OutgoingEvents uint64   `json:"outgoingEvents"`
	DamageTaken    int64    `json:"damageTaken"`
	IncomingEvents uint64   `json:"incomingEvents"`
	// Skills and UnassignedSkill split DamageDealt by DamageContext.SkillId; shares use DamageDealt as denominator.
	Skills          []ViewSkill         `json:"skills"`
	UnassignedSkill ViewUnassignedSkill `json:"unassignedSkill"`
}

// ViewSkill is one DamageContext.SkillId of a contributor. Name is a Master name for the selected locale, filled by the
// display projection. NameStatus VERIFIED is the SkillId's own official name; DERIVED is the official name of the
// related record NameSource and SourceID identify (skill_names.go), which the UI marks; UNKNOWN leaves Name empty and
// the UI shows the SkillId. Developer labels and unofficial translations are never used here.
type ViewSkill struct {
	SkillID    uint64 `json:"skillId"`
	Name       string `json:"name"`
	NameStatus string `json:"nameStatus"`
	// NameSource is "official", "damage_list", "skill_type", "tune_break" or "echo"; empty when Name is empty.
	NameSource string `json:"nameSource,omitempty"`
	// ShowSkillID says the skill table prints SkillID beside the row. A VERIFIED row is identified by its own official name; a
	// DERIVED row keeps the ID beside a name that belongs to a related record; an UNKNOWN row has only the ID to show.
	ShowSkillID bool `json:"showSkillId"`
	// SourceID is the record Name belongs to for a DERIVED name: Skill.Id (damage_list, skill_type, tune_break) or PhantomItem.Id (echo).
	SourceID uint64 `json:"sourceId,omitempty"`
	// IconKind and IconID address the row's image; the name and the image are resolved separately (skill_names.go).
	IconKind       string   `json:"iconKind,omitempty"`
	IconID         uint64   `json:"iconId,omitempty"`
	DamageDealt    int64    `json:"damageDealt"`
	DamageShare    *float64 `json:"damageShare,omitempty"`
	OutgoingEvents uint64   `json:"outgoingEvents"`
}

// ViewUnassignedSkill is a contributor's confirmed damage without a skill row. The hits have no SkillId, so a name here is
// only ever DERIVED, from the relations in skill_names.go; the row stays the unassigned row and a hit never gains a SkillId.
type ViewUnassignedSkill struct {
	UnassignedSkillTotals
	DamageShare *float64 `json:"damageShare,omitempty"`
	// Name is set when the engine has a reliable source for these hits; the UI then shows it with the derivation mark.
	Name       string `json:"name,omitempty"`
	NameStatus string `json:"nameStatus,omitempty"`
	// NameSource is "echo", "damage_type" (Outro Skill DMG) or "damage_list"; empty when Name is empty.
	NameSource string `json:"nameSource,omitempty"`
	SourceID   uint64 `json:"sourceId,omitempty"`
	IconKind   string `json:"iconKind,omitempty"`
	IconID     uint64 `json:"iconId,omitempty"`
	// UnnamedReason is a stable code saying why these hits have no name (skill_names.go); empty when named or when the row is empty.
	UnnamedReason string `json:"unnamedReason,omitempty"`
}

// ViewEffects is the display set of the tracked effect state at the capture time. Active lists display-eligible
// instances only (see effectDisplayFor); Counts accounts for every tracked instance, listed or not, by the reason it was
// left out, so a consumer can show what is hidden without seeing it.
type ViewEffects struct {
	Status EffectSnapshotStatus `json:"status"`
	Reason string               `json:"reason,omitempty"`
	// Buffs and Debuffs are the two rows of the Effects Overlay, classified, filtered and ordered here: soonest to end first,
	// cells without a reliable time last. The frontend only lays them out.
	Buffs   ViewEffectRow `json:"buffs"`
	Debuffs ViewEffectRow `json:"debuffs"`
	// Active is every listed cell in display order, the union of the two rows. It stays out of the snapshot to keep each
	// publish small; the rows carry the same cells.
	Active      []ViewEffect     `json:"-"`
	RecentEnded []ViewEffect     `json:"recentEnded"`
	Counts      ViewEffectCounts `json:"counts"`
	// ClockStalled reports that a live capture has seen no game frame for LiveFrameSilenceLimit: the countdowns stopped
	// advancing because the capture no longer knows what the game is doing. A replay never sets it.
	ClockStalled bool `json:"clockStalled,omitempty"`
}

// ViewEffectRow is one row of the Effects Overlay. Truncated counts the row's cells beyond the view's bound.
type ViewEffectRow struct {
	Effects   []ViewEffect `json:"effects"`
	Truncated int          `json:"truncated,omitempty"`
}

// ViewEffect is one tracked effect instance. Key is an opaque, stable identity for list rendering; it is a hash, not
// a protocol identifier. HolderRoleID is set when the holder is a local role by runtime evidence.
type ViewEffect struct {
	Key      string  `json:"key"`
	EffectID *uint64 `json:"effectId,omitempty"`
	TermName string  `json:"termName,omitempty"`
	TermID   *uint64 `json:"termId,omitempty"`
	// TermStatus is the evidence level of the TermName link; it is never stronger than correlated.
	TermStatus       string               `json:"termStatus,omitempty"`
	HolderRelation   EffectEntityRelation `json:"holderRelation"`
	HolderRoleID     *uint64              `json:"holderRoleId,omitempty"`
	HolderName       string               `json:"holderName,omitempty"`
	HolderNameStatus string               `json:"holderNameStatus,omitempty"`
	SourceRelation   EffectEntityRelation `json:"sourceRelation"`
	Polarity         EffectPolarity       `json:"polarity"`
	CombatClass      EffectCombatClass    `json:"combatClass"`
	Kind             EffectKind           `json:"kind"`
	// HUDIcon reports that the game draws this effect in a HUD buff bar with an icon of its own (effect_hud_cues.go);
	// only such an effect has an icon worth requesting. It says nothing about the effect's meaning.
	HUDIcon bool `json:"hudIcon,omitempty"`
	// ElementID is the element the effect's own definition states (effect_element.go); absent means none is established.
	ElementID        *uint64               `json:"elementId,omitempty"`
	Display          EffectDisplay         `json:"display"`
	StackCount       *uint64               `json:"stackCount,omitempty"`
	Lifecycle        EffectLifecycle       `json:"lifecycle"`
	FirstObservedAt  time.Time             `json:"firstObservedAt"`
	LastUpdatedAt    time.Time             `json:"lastUpdatedAt"`
	DurationSeconds  *float64              `json:"durationSeconds,omitempty"`
	RemainingStatus  EffectRemainingStatus `json:"remainingStatus"`
	RemainingSeconds *float64              `json:"remainingSeconds,omitempty"`
	ExpectedExpiry   *time.Time            `json:"expectedExpiry,omitempty"`
	Stale            bool                  `json:"stale,omitempty"`
	Evidence         EffectEvidence        `json:"evidence"`
	EndedAt          *time.Time            `json:"endedAt,omitempty"`
	// Instances is how many tracker instances this overlay cell stands for (effectDisplayCells).
	Instances int `json:"instances,omitempty"`
	// ShowCountdown says the remaining time may be shown: the engine expects it to be right. A frontend applies only its own
	// connection and freshness state on top of it.
	ShowCountdown bool `json:"showCountdown,omitempty"`
}

// viewSnapshot builds the UI view at the newest capture time. It runs on the capture goroutine.
func (p *processor) viewSnapshot() ViewSnapshot {
	statistics := p.damageStatisticsSnapshot()
	observedAt := p.currentFrameTime
	if !observedAt.IsZero() {
		statistics = p.damageStatisticsSnapshotAt(CaptureQueryTime{CaptureSessionID: statistics.CaptureSessionID, Timestamp: observedAt})
	}
	capture := p.captureLayerHealth()
	pairing := p.rpcPairingSnapshot()
	view := ViewSnapshot{
		SchemaVersion:        ViewSnapshotSchemaVersion,
		CaptureSessionID:     statistics.CaptureSessionID,
		StatisticsIntervalID: statistics.StatisticsIntervalID,
		ProtocolSession:      p.sessionNumber,
		CaptureObservedAt:    observedAt,
		Health: ViewHealth{
			Capture: ViewCaptureHealth{
				Source: capture.Source, PacketsRead: capture.PacketsRead, KernelReceived: capture.KernelReceived,
				KernelDropped: capture.KernelDropped, ApplicationDropped: capture.ApplicationDropped, DeliveryDelay: capture.DeliveryDelay, DiagnosticDropped: capture.DiagnosticDropped,
			},
			Transport:     cloneTransportHealth(p.transportHealthSnapshot()),
			Context:       cloneContextValidity(p.context.snapshot()),
			Session:       p.sessionHealthSnapshot(),
			DataIntegrity: statistics.Integrity.Status,
			RPCPairing: ViewRPCPairing{
				Requests: pairing.Requests, Paired: pairing.Paired, PairedOutOfOrder: pairing.PairedOutOfOrder,
				Unpaired:      pairing.UnpairedExpired + pairing.UnpairedEvicted + pairing.UnpairedSessionChange + pairing.UnpairedCaptureEnd,
				HeldResponses: pairing.HeldResponses,
			},
		},
		Statistics: ViewStatistics{
			DamageDealt: statistics.DamageDealt, DamageTaken: statistics.DamageTaken,
			OutgoingEvents: statistics.OutgoingEvents, IncomingEvents: statistics.IncomingEvents,
			Unattributed: ViewUnattributed{
				OutgoingDamage: statistics.UnattributedOutgoingDamage, IncomingDamage: statistics.UnattributedIncomingDamage,
				OutgoingEvents: statistics.UnattributedOutgoingEvents, IncomingEvents: statistics.UnattributedIncomingEvents,
			},
			DuplicateEvents: statistics.DuplicateEvents,
			Integrity:       cloneIntegrity(statistics.Integrity),
			EncounterDPS:    viewDPS(statistics.EncounterDPS),
			DamageSpanDPS:   viewDPS(statistics.DamageSpanDPS),
			Rolling5sDPS:    viewDPS(statistics.Rolling5sDPS),
			Rolling10sDPS:   viewDPS(statistics.Rolling10sDPS),
			Healing:         viewHealing(intervalHealing(statistics)),
		},
		Encounters:          p.viewEncounters(statistics.Encounters),
		Roles:               p.viewRoles(statistics.Roles, statistics.DamageDealt),
		UnassignedRole:      statistics.UnassignedRole,
		UnassignedRoleShare: unassignedRoleShare(statistics.UnassignedRole, statistics.DamageDealt),
		Vehicle:             p.viewVehicle(statistics.Vehicle, statistics.DamageDealt),
		Completeness:        intervalDamageCompleteness(statistics),
		Effects:             p.viewEffects(),
	}
	rankViewContributors(view.Roles, view.Vehicle, nil)
	if p.damageStatistics != nil {
		view.Timeline = p.damageStatistics.TimelineAt(CaptureQueryTime{CaptureSessionID: statistics.CaptureSessionID, Timestamp: observedAt}, maxViewTimelinePoints)
	}
	return view
}

func viewDPS(result DPSResult) ViewDPS {
	return ViewDPS{
		Status: result.Status, Reason: result.Reason, Damage: result.Damage, DurationSeconds: result.Duration.Seconds(),
		Value: result.Value, Confidence: result.Confidence,
	}
}

func cloneIntegrity(integrity DataIntegrity) DataIntegrity {
	integrity.Risks = append([]string{}, integrity.Risks...)
	return integrity
}

func cloneTransportHealth(health TransportHealthSnapshot) TransportHealthSnapshot {
	health.Directions = append([]TransportDirectionHealth{}, health.Directions...)
	return health
}

func cloneContextValidity(context ContextValiditySnapshot) ContextValiditySnapshot {
	context.Components = append([]ContextComponentSnapshot{}, context.Components...)
	return context
}

func (p *processor) viewEncounters(encounters EncounterTrackerSnapshot) ViewEncounters {
	view := ViewEncounters{
		State: encounters.State, BattleState: encounters.BattleState, Recent: []ViewEncounter{},
		CompletedTotal: encounters.CompletedDropped + uint64(len(encounters.Completed)),
	}
	if encounters.Current != nil {
		current := p.viewEncounter(*encounters.Current)
		view.Current = &current
	}
	completed := encounters.Completed
	if len(completed) > maxViewRecentEncounters {
		completed = completed[len(completed)-maxViewRecentEncounters:]
	}
	for _, encounter := range completed {
		view.Recent = append(view.Recent, p.viewEncounter(encounter))
	}
	return view
}

func (p *processor) viewEncounter(encounter EncounterSnapshot) ViewEncounter {
	return projectEncounter(p.masters, encounter, p.currentFrameTime)
}

// ProjectEncounter builds the view of a finished encounter with the same projection the live view uses: Master names, the
// skill split, contributor ranks and per-row DPS. Combat History uses it to show a stored encounter exactly as it was shown.
func (m *MasterData) ProjectEncounter(encounter EncounterSnapshot) ViewEncounter {
	var masters *gameMaster
	if m != nil {
		masters = m.masters
	}
	return projectEncounter(masters, encounter, time.Time{})
}

// projectEncounter projects one encounter. now is the capture time an open encounter's elapsed time is measured to.
func projectEncounter(masters *gameMaster, encounter EncounterSnapshot, now time.Time) ViewEncounter {
	view := ViewEncounter{
		EncounterID: encounter.EncounterID, Sequence: encounter.Sequence, ProtocolSession: encounter.ProtocolSession,
		State: encounter.State, Outcome: encounter.Outcome, StartedAt: encounter.StartedAt, StartBasis: encounter.StartBasis,
		EndedAt: encounter.EndedAt, EndBasis: encounter.EndBasis, Totals: encounter.Totals,
		Integrity: cloneIntegrity(encounter.Integrity), DPS: viewDPS(encounter.EncounterDPS),
		PeakDPS: viewDPS(encounter.PeakDPS), Healing: viewHealing(encounter.Healing),
		Roles: projectRoles(masters, encounter.Roles, encounter.Totals.DamageDealt), UnassignedRole: encounter.UnassignedRole,
		UnassignedRoleShare: unassignedRoleShare(encounter.UnassignedRole, encounter.Totals.DamageDealt),
		Vehicle:             projectVehicle(masters, encounter.Vehicle, encounter.Totals.DamageDealt),
		Completeness:        encounterDamageCompleteness(encounter),
	}
	rankViewContributors(view.Roles, view.Vehicle, &view.DPS)
	end := encounter.EndedAt
	if end.IsZero() {
		end = now
	}
	if encounter.Integrity.Status != IntegrityUnreliable && !encounter.StartedAt.IsZero() && !end.IsZero() && !end.Before(encounter.StartedAt) {
		elapsed := end.Sub(encounter.StartedAt).Seconds()
		view.ElapsedSeconds = &elapsed
	}
	return view
}

// rankViewContributors fills the Rank and DPS of one scope's contributor rows. It runs while the scope is projected, from the
// rows already built for that scope, so a row's damage, share, DPS and rank always describe the same snapshot and encounter
// and no later reader has to scan events or ask the engine again.
//
// A role and a vehicle are ranked together, each as itself: a vehicle is never credited to a role, and unassigned damage has
// no row, so it is never ranked. Rank 0 is the most damage. Equal damage falls back to the row's identity, which does not
// change during a fight, so a tie never swaps two rows.
//
// scopeDPS is the scope's own DPS result; the rows use its duration, the confirmed elapsed time of the encounter including any
// time stop, so they add up to it. A DPS the UI may not show (or no DPS result, as for a capture interval) leaves DPS nil.
func rankViewContributors(roles []ViewRole, vehicle *ViewVehicle, scopeDPS *ViewDPS) {
	type contributor struct {
		key    string
		damage int64
		rank   *int
		dps    **float64
	}
	contributors := make([]contributor, 0, len(roles)+1)
	for i := range roles {
		role := &roles[i]
		key := "role-" + strconv.FormatUint(role.PlayerID, 10) + "-" + strconv.FormatUint(role.RoleID, 10)
		contributors = append(contributors, contributor{key: key, damage: role.DamageDealt, rank: &role.Rank, dps: &role.DPS})
	}
	if vehicle != nil {
		contributors = append(contributors, contributor{key: "vehicle-" + vehicle.Kind, damage: vehicle.DamageDealt, rank: &vehicle.Rank, dps: &vehicle.DPS})
	}
	sort.Slice(contributors, func(i, j int) bool {
		if contributors[i].damage != contributors[j].damage {
			return contributors[i].damage > contributors[j].damage
		}
		return contributors[i].key < contributors[j].key
	})
	var seconds float64
	if scopeDPS != nil && (scopeDPS.Status == DPSStatusAvailable || scopeDPS.Status == DPSStatusLowerBound) {
		seconds = scopeDPS.DurationSeconds
	}
	for i := range contributors {
		*contributors[i].rank = i
		if seconds > 0 {
			value := float64(contributors[i].damage) / seconds
			*contributors[i].dps = &value
		}
	}
}

// unassignedRoleShare uses the same confirmed-local denominator as the role rows.
func unassignedRoleShare(unassigned UnassignedRoleTotals, total int64) *float64 {
	if total <= 0 {
		return nil
	}
	share := float64(unassigned.DamageDealt) / float64(total)
	return &share
}

func (p *processor) viewRoles(roles []RoleDamageSnapshot, total int64) []ViewRole {
	return projectRoles(p.masters, roles, total)
}

func projectRoles(masters *gameMaster, roles []RoleDamageSnapshot, total int64) []ViewRole {
	view := make([]ViewRole, 0, len(roles))
	for _, role := range roles {
		name, status := roleDisplayName(masters, role.RoleID)
		view = append(view, ViewRole{
			PlayerID: role.PlayerID, RoleID: role.RoleID, Name: name, NameStatus: status,
			DamageDealt: role.DamageDealt, DirectDamage: role.DirectDamage, SummonDamage: role.SummonDamage,
			OutgoingEvents: role.OutgoingEvents, DamageTaken: role.DamageTaken, IncomingEvents: role.IncomingEvents,
		})
		view[len(view)-1].Skills, view[len(view)-1].UnassignedSkill = projectSkills(masters, role.RoleID, role.Skills, role.UnassignedSkill, role.DamageDealt)
		if total > 0 {
			share := float64(role.DamageDealt) / float64(total)
			view[len(view)-1].DamageShare = &share
		}
	}
	return view
}

// viewSkills projects a contributor's skill split. Shares use the contributor's own confirmed damage, never the team's.
// roleID is zero for the vehicle, which has no skill group.
func (p *processor) viewSkills(roleID uint64, skills []SkillDamageSnapshot, unassigned UnassignedSkillTotals, total int64) ([]ViewSkill, ViewUnassignedSkill) {
	return projectSkills(p.masters, roleID, skills, unassigned, total)
}

func projectSkills(masters *gameMaster, roleID uint64, skills []SkillDamageSnapshot, unassigned UnassignedSkillTotals, total int64) ([]ViewSkill, ViewUnassignedSkill) {
	share := func(amount int64) *float64 {
		if total <= 0 {
			return nil
		}
		value := float64(amount) / float64(total)
		return &value
	}
	view := make([]ViewSkill, 0, len(skills))
	// A trial role names its rows through its parent role's skills (staticRoleID).
	staticRole := masters.staticRoleID(roleID)
	for _, skill := range skills {
		name, status, source, sourceID := masters.skillRowName(staticRole, skill)
		iconKind, iconID := masters.skillRowIcon(skill, source, sourceID)
		view = append(view, ViewSkill{
			SkillID: skill.SkillID, Name: name, NameStatus: status, NameSource: source, SourceID: sourceID, IconKind: iconKind, IconID: iconID,
			ShowSkillID: status != string(resolutionVerified),
			DamageDealt: skill.DamageDealt, DamageShare: share(skill.DamageDealt), OutgoingEvents: skill.OutgoingEvents,
		})
	}
	row := ViewUnassignedSkill{UnassignedSkillTotals: unassigned, DamageShare: share(unassigned.DamageDealt)}
	source, reason := masters.unassignedSkillName(staticRole, unassigned)
	if source.name != "" {
		row.Name, row.NameStatus, row.NameSource, row.SourceID, row.IconKind, row.IconID = source.name, skillNameStatusDerived, source.source, source.sourceID, source.iconKind, source.iconID
	}
	row.UnnamedReason = reason
	return view, row
}

// roleDisplayName returns the Master name of a RoleId. A name is shown only when Master marks it verified; anything
// else stays UNKNOWN rather than a plausible guess.
func (p *processor) roleDisplayName(roleID uint64) (string, string) {
	return roleDisplayName(p.masters, roleID)
}

func roleDisplayName(masters *gameMaster, roleID uint64) (string, string) {
	if masters == nil {
		return "", string(resolutionUnknown)
	}
	character, ok := masters.characters[masters.staticRoleID(roleID)]
	if !ok || character.Name == "" || character.NameStatus != string(resolutionVerified) {
		return "", string(resolutionUnknown)
	}
	return character.Name, character.NameStatus
}

func (p *processor) viewEffects() ViewEffects {
	if p.effects == nil {
		return ViewEffects{Status: EffectSnapshotComplete, Active: []ViewEffect{}}
	}
	snapshot := p.effectSnapshotAfterLastFrame(p.viewClockLead)
	// Only display-eligible instances are listed; the bound applies to the display set alone, so an eligible instance is
	// never pushed out by one that would not have been shown. Instances the strip cannot tell apart share one cell
	// (effectDisplayCells). Cells are ordered by when they end, so the bound keeps the soonest. The order only decides which
	// cells fit; it never removes one from the display set.
	view := ViewEffects{Status: snapshot.Status, Reason: snapshot.Reason, Active: []ViewEffect{}, Buffs: ViewEffectRow{Effects: []ViewEffect{}}, Debuffs: ViewEffectRow{Effects: []ViewEffect{}}, ClockStalled: p.viewClockStalled}
	eligible := make([]EffectInstanceSnapshot, 0, len(snapshot.Effects))
	for _, effect := range snapshot.Effects {
		display := effectDisplayFor(effect)
		view.Counts.add(display.Eligibility)
		if display.Eligibility == EffectDisplayEligible {
			eligible = append(eligible, effect)
		}
	}
	cells := effectDisplayCells(eligible)
	sortEffectCells(cells)
	view.Counts.Cells = len(cells)
	countdowns := snapshot.Status == EffectSnapshotComplete && !p.viewClockStalled && p.context.snapshot().State == ContextValid
	for _, cell := range cells {
		row := &view.Buffs
		if effectPolarityFor(cell.representative) == EffectPolarityDebuff {
			row = &view.Debuffs
		}
		if len(view.Active) == maxViewEffects {
			view.Counts.Truncated += cell.instances()
			view.Counts.TruncatedCells++
			row.Truncated++
			continue
		}
		view.Counts.Listed += cell.instances()
		view.Counts.ListedCells++
		projected := p.viewEffectCell(cell)
		projected.ShowCountdown = countdowns && effectCellCountsDown(cell.earliest)
		view.Active = append(view.Active, projected)
		row.Effects = append(row.Effects, projected)
	}
	endedEffects := snapshot.RecentEnded
	if len(endedEffects) > 12 {
		endedEffects = endedEffects[len(endedEffects)-12:]
	}
	for _, ended := range endedEffects {
		effect := p.viewEffect(ended.EffectInstanceSnapshot)
		// An ended instance carries the decision it had while active, so a consumer can tell which ended rows it showed.
		whileActive := ended.EffectInstanceSnapshot
		whileActive.Lifecycle = EffectLifecycleActive
		effect.Display = effectDisplayFor(whileActive)
		// An ended holder's ID may already have been reused. Do not resolve its past role from current entities.
		effect.HolderRoleID = nil
		at := ended.EndedAt
		effect.EndedAt = &at
		view.RecentEnded = append(view.RecentEnded, effect)
	}
	return view
}

// viewEffectCell projects one Effects Overlay cell. Every member agrees on its stack count (effectCellIdentity), so the
// count is never a sum across holders. The countdown is the earliest member's, so a cell never reads later than its first
// end and it sorts where its first end belongs.
func (p *processor) viewEffectCell(cell effectDisplayCell) ViewEffect {
	view := p.viewEffect(cell.representative)
	view.Display = effectDisplayFor(cell.representative)
	view.Instances = cell.instances()
	view.RemainingStatus, view.RemainingSeconds = cell.earliest.RemainingStatus, cloneFloat64(cell.earliest.RemainingSeconds)
	view.ExpectedExpiry = nil
	if cell.earliest.ExpectedExpiry != nil {
		expiry := *cell.earliest.ExpectedExpiry
		view.ExpectedExpiry = &expiry
	}
	if cell.instances() > 1 {
		view.HolderRoleID = nil
	}
	return view
}

func (p *processor) viewEffect(effect EffectInstanceSnapshot) ViewEffect {
	view := ViewEffect{
		Key:              effectViewKey(effect),
		EffectID:         cloneUint64(effect.EffectID),
		HolderRelation:   effect.HolderRelation,
		SourceRelation:   effect.SourceRelation,
		Polarity:         effect.Polarity,
		CombatClass:      effect.CombatClass,
		Kind:             effect.Kind,
		StackCount:       cloneUint64(effect.StackCount),
		Lifecycle:        effect.Lifecycle,
		FirstObservedAt:  effect.FirstObservedAt,
		LastUpdatedAt:    effect.LastUpdatedAt,
		DurationSeconds:  cloneFloat64(effect.ServerDurationSeconds),
		RemainingStatus:  effect.RemainingStatus,
		RemainingSeconds: cloneFloat64(effect.RemainingSeconds),
		Stale:            effect.StaleSinceTransportLoss,
		Evidence:         effect.Evidence,
	}
	if effect.ExpectedExpiry != nil {
		expiry := *effect.ExpectedExpiry
		view.ExpectedExpiry = &expiry
	}
	if effect.Static != nil && len(effect.Static.HUDCueTypes) > 0 {
		view.HUDIcon = true
	}
	if effect.Static != nil && effect.Static.ElementID != 0 {
		element := effect.Static.ElementID
		view.ElementID = &element
	}
	if effect.Static != nil && effect.Static.Term != nil {
		view.TermName = effect.Static.Term.Name
		view.TermStatus = effect.Static.Term.Status
		id := effect.Static.Term.TermID
		view.TermID = &id
	}
	if effect.HolderRelation == EffectRelationLocalRole || effect.HolderRelation == EffectRelationLocalOwned {
		if role := p.localRoleForEntity(effect.HolderEntityID, entityIdentity{}); role != nil {
			roleID := role.RoleID
			view.HolderRoleID = &roleID
		}
	}
	return view
}

// effectViewKey hashes the tracker's instance identity so the UI can key list rows without seeing it.
func effectViewKey(effect EffectInstanceSnapshot) string {
	hash := fnv.New64a()
	for _, value := range []uint64{uint64(effect.SessionNumber), effect.HolderEntityID, effect.HolderGeneration, effect.HandleID} {
		var buffer [8]byte
		for i := range buffer {
			buffer[i] = byte(value >> (8 * i))
		}
		_, _ = hash.Write(buffer[:])
	}
	return strconv.FormatUint(hash.Sum64(), 36)
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// ViewVehicle projects one contributor with the unchanged confirmed-local share denominator.
type ViewVehicle struct {
	VehicleDamageSnapshot
	DamageShare *float64 `json:"damageShare,omitempty"`
	// Rank and DPS are this row's place and rate within the scope that holds it (rankViewContributors).
	Rank int      `json:"rank"`
	DPS  *float64 `json:"dps,omitempty"`
	// Skills and UnassignedSkill shadow the embedded engine rows with their display projection.
	Skills          []ViewSkill         `json:"skills"`
	UnassignedSkill ViewUnassignedSkill `json:"unassignedSkill"`
}

func (p *processor) viewVehicle(vehicle *VehicleDamageSnapshot, total int64) *ViewVehicle {
	return projectVehicle(p.masters, vehicle, total)
}

func projectVehicle(masters *gameMaster, vehicle *VehicleDamageSnapshot, total int64) *ViewVehicle {
	if vehicle == nil {
		return nil
	}
	view := &ViewVehicle{VehicleDamageSnapshot: *vehicle}
	view.VehicleDamageSnapshot.Skills = nil
	view.Skills, view.UnassignedSkill = projectSkills(masters, 0, vehicle.Skills, vehicle.UnassignedSkill, vehicle.DamageDealt)
	if total > 0 {
		share := float64(vehicle.DamageDealt) / float64(total)
		view.DamageShare = &share
	}
	return view
}
