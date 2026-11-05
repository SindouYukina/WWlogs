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

	Health         ViewHealth           `json:"health"`
	Statistics     ViewStatistics       `json:"statistics"`
	Encounters     ViewEncounters       `json:"encounters"`
	Roles          []ViewRole           `json:"roles"`
	UnassignedRole UnassignedRoleTotals `json:"unassignedRole"`
	Effects        ViewEffects          `json:"effects"`
}

// ViewSnapshotSchemaVersion changes whenever a field changes meaning or is removed.
const ViewSnapshotSchemaVersion = 1

// View bounds keep a snapshot small regardless of how long a session runs.
const (
	maxViewRecentEncounters = 10
	maxViewEffects          = 128
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
	ElapsedSeconds  *float64             `json:"elapsedSeconds,omitempty"`
	EncounterID     string               `json:"encounterId"`
	Sequence        uint64               `json:"sequence"`
	ProtocolSession int                  `json:"protocolSession"`
	State           EncounterState       `json:"state"`
	Outcome         EncounterOutcome     `json:"outcome"`
	StartedAt       time.Time            `json:"startedAt"`
	StartBasis      string               `json:"startBasis"`
	EndedAt         time.Time            `json:"endedAt"`
	EndBasis        string               `json:"endBasis,omitempty"`
	Totals          EncounterTotals      `json:"totals"`
	Integrity       DataIntegrity        `json:"integrity"`
	DPS             ViewDPS              `json:"dps"`
	Roles           []ViewRole           `json:"roles"`
	UnassignedRole  UnassignedRoleTotals `json:"unassignedRole"`
}

// ViewRole is one local role's totals. Name comes from the Master database for the selected locale; it is empty with
// NameStatus UNKNOWN when the RoleId has no verified name.
type ViewRole struct {
	PlayerID       uint64   `json:"playerId"`
	RoleID         uint64   `json:"roleId"`
	Name           string   `json:"name"`
	NameStatus     string   `json:"nameStatus"`
	DamageDealt    int64    `json:"damageDealt"`
	DamageShare    *float64 `json:"damageShare,omitempty"`
	DirectDamage   int64    `json:"directDamage"`
	SummonDamage   int64    `json:"summonDamage"`
	OutgoingEvents uint64   `json:"outgoingEvents"`
	DamageTaken    int64    `json:"damageTaken"`
	IncomingEvents uint64   `json:"incomingEvents"`
}

// ViewEffects is the tracked effect state at the capture time.
type ViewEffects struct {
	Status      EffectSnapshotStatus `json:"status"`
	Reason      string               `json:"reason,omitempty"`
	Active      []ViewEffect         `json:"active"`
	RecentEnded []ViewEffect         `json:"recentEnded"`
	// Omitted counts tracked instances not listed because of maxViewEffects.
	Omitted int `json:"omitted"`
}

// ViewEffect is one tracked effect instance. Key is an opaque, stable identity for list rendering; it is a hash, not
// a protocol identifier. HolderRoleID is set when the holder is a local role by runtime evidence.
type ViewEffect struct {
	Key      string  `json:"key"`
	EffectID *uint64 `json:"effectId,omitempty"`
	TermName string  `json:"termName,omitempty"`
	TermID   *uint64 `json:"termId,omitempty"`
	// TermStatus is the evidence level of the TermName link; it is never stronger than correlated.
	TermStatus       string                `json:"termStatus,omitempty"`
	HolderRelation   EffectEntityRelation  `json:"holderRelation"`
	HolderRoleID     *uint64               `json:"holderRoleId,omitempty"`
	HolderName       string                `json:"holderName,omitempty"`
	HolderNameStatus string                `json:"holderNameStatus,omitempty"`
	SourceRelation   EffectEntityRelation  `json:"sourceRelation"`
	Polarity         EffectPolarity        `json:"polarity"`
	CombatClass      EffectCombatClass     `json:"combatClass"`
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
		},
		Encounters:     p.viewEncounters(statistics.Encounters),
		Roles:          p.viewRoles(statistics.Roles, statistics.DamageDealt),
		UnassignedRole: statistics.UnassignedRole,
		Effects:        p.viewEffects(),
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
	view := ViewEncounter{
		EncounterID: encounter.EncounterID, Sequence: encounter.Sequence, ProtocolSession: encounter.ProtocolSession,
		State: encounter.State, Outcome: encounter.Outcome, StartedAt: encounter.StartedAt, StartBasis: encounter.StartBasis,
		EndedAt: encounter.EndedAt, EndBasis: encounter.EndBasis, Totals: encounter.Totals,
		Integrity: cloneIntegrity(encounter.Integrity), DPS: viewDPS(encounter.EncounterDPS),
		Roles: p.viewRoles(encounter.Roles, encounter.Totals.DamageDealt), UnassignedRole: encounter.UnassignedRole,
	}
	end := encounter.EndedAt
	if end.IsZero() {
		end = p.currentFrameTime
	}
	if encounter.Integrity.Status != IntegrityUnreliable && !encounter.StartedAt.IsZero() && !end.IsZero() && !end.Before(encounter.StartedAt) {
		elapsed := end.Sub(encounter.StartedAt).Seconds()
		view.ElapsedSeconds = &elapsed
	}
	return view
}

func (p *processor) viewRoles(roles []RoleDamageSnapshot, total int64) []ViewRole {
	view := make([]ViewRole, 0, len(roles))
	for _, role := range roles {
		name, status := p.roleDisplayName(role.RoleID)
		view = append(view, ViewRole{
			PlayerID: role.PlayerID, RoleID: role.RoleID, Name: name, NameStatus: status,
			DamageDealt: role.DamageDealt, DirectDamage: role.DirectDamage, SummonDamage: role.SummonDamage,
			OutgoingEvents: role.OutgoingEvents, DamageTaken: role.DamageTaken, IncomingEvents: role.IncomingEvents,
		})
		if total > 0 {
			share := float64(role.DamageDealt) / float64(total)
			view[len(view)-1].DamageShare = &share
		}
	}
	return view
}

// roleDisplayName returns the Master name of a RoleId. A name is shown only when Master marks it verified; anything
// else stays UNKNOWN rather than a plausible guess.
func (p *processor) roleDisplayName(roleID uint64) (string, string) {
	if p.masters == nil {
		return "", string(resolutionUnknown)
	}
	character, ok := p.masters.characters[roleID]
	if !ok || character.Name == "" || character.NameStatus != string(resolutionVerified) {
		return "", string(resolutionUnknown)
	}
	return character.Name, character.NameStatus
}

func (p *processor) viewEffects() ViewEffects {
	if p.effects == nil {
		return ViewEffects{Status: EffectSnapshotComplete, Active: []ViewEffect{}}
	}
	snapshot := p.effectSnapshotAtLastFrame()
	// Keep local combat effects ahead of unrelated instances when the curated view reaches its bound.
	priority := func(effect EffectInstanceSnapshot) int {
		local := effect.HolderRelation == EffectRelationLocalRole || effect.HolderRelation == EffectRelationLocalOwned || effect.SourceRelation == EffectRelationLocalRole || effect.SourceRelation == EffectRelationLocalOwned
		if !local {
			return 5
		}
		if effect.CombatClass == EffectCombatClassNonCombat {
			return 4
		}
		if effect.Lifecycle != EffectLifecycleActive {
			return 3
		}
		if effect.CombatClass == EffectCombatClassCombat {
			return 0
		}
		return 1
	}
	sort.SliceStable(snapshot.Effects, func(i, j int) bool { return priority(snapshot.Effects[i]) < priority(snapshot.Effects[j]) })
	view := ViewEffects{Status: snapshot.Status, Reason: snapshot.Reason, Active: []ViewEffect{}}
	for _, effect := range snapshot.Effects {
		if len(view.Active) == maxViewEffects {
			view.Omitted++
			continue
		}
		view.Active = append(view.Active, p.viewEffect(effect))
	}
	endedEffects := snapshot.RecentEnded
	if len(endedEffects) > 12 {
		endedEffects = endedEffects[len(endedEffects)-12:]
	}
	for _, ended := range endedEffects {
		effect := p.viewEffect(ended.EffectInstanceSnapshot)
		// An ended holder's ID may already have been reused. Do not resolve its past role from current entities.
		effect.HolderRoleID = nil
		at := ended.EndedAt
		effect.EndedAt = &at
		view.RecentEnded = append(view.RecentEnded, effect)
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
