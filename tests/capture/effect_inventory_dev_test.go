package capture

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// This file is the development-profile, test-only PCAP replay that inventories gameplay-effect instances and checks the
// Effects view accounting. It replays archived captures through the same processor and datagram entry point as the
// executables and, after every packet, compares the tracker's live instances with the previous packet to record each
// instance's lifecycle per effect ID. Every effectInventoryPublishEvery packets it builds the view snapshot as the host
// does and checks that the effect counts partition the tracked set exactly.
//
// It is skipped unless WWLOGS_EFFECT_INVENTORY_OUT is set and reads only paths supplied through the environment. The
// output is research evidence for the private research tree; it is never a product input.
//
// Environment:
//
//	WWLOGS_EFFECT_INVENTORY_OUT        output directory (required to run)
//	WWLOGS_EFFECT_INVENTORY_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_EFFECT_INVENTORY_MASTER_DB  Master SQLite database

const effectInventoryPublishEvery = 37

// byteOrderMark may start a PCAP list written by Windows PowerShell.
const byteOrderMark = string(rune(0xFEFF))

type effectInventoryEntry struct {
	EffectID         uint64            `json:"effectId"`
	Instances        int               `json:"instances"`
	ByEvidence       map[string]int    `json:"byEvidence"`
	ByOrigin         map[string]int    `json:"byOrigin"`
	HolderRelations  map[string]int    `json:"holderRelations"`
	SourceRelations  map[string]int    `json:"sourceRelations"`
	HolderRoles      []uint64          `json:"holderRoles,omitempty"`
	HolderMonster    int               `json:"holderMonster"`
	CombatClass      map[string]int    `json:"combatClass"`
	CombatBasis      map[string]int    `json:"combatBasis"`
	Kind             map[string]int    `json:"kind"`
	DisplayCategory  map[string]int    `json:"displayCategory,omitempty"`
	Refreshes        int               `json:"refreshes"`
	StackChanges     int               `json:"stackChanges"`
	MaxStack         uint64            `json:"maxStack"`
	FiniteDurations  map[string]int    `json:"finiteDurations"`
	UnknownRemaining map[string]int    `json:"unknownRemaining"`
	Ended            map[string]int    `json:"ended"`
	ActiveAtEnd      int               `json:"activeAtEnd"`
	LifetimeSeconds  []float64         `json:"lifetimeSeconds,omitempty"`
	MaxConcurrent    int               `json:"maxConcurrent"`
	Definition       bool              `json:"definition"`
	DurationPolicy   *int64            `json:"durationPolicy,omitempty"`
	StaticDuration   *float64          `json:"staticDurationSeconds,omitempty"`
	StackLimit       *uint64           `json:"stackLimit,omitempty"`
	Attribute        string            `json:"attribute,omitempty"`
	Term             string            `json:"term,omitempty"`
	DeveloperTags    []string          `json:"developerTags,omitempty"`
	FirstSeen        string            `json:"firstSeen"`
	extra            map[uint64]bool   `json:"-"`
	concurrent       map[effectKey]int `json:"-"`
}

type effectInventorySample struct {
	At     string            `json:"at"`
	Counts ViewEffectCounts  `json:"counts"`
	Old    effectLegacyCount `json:"legacy"`
	// Listed counts the listed rows by kind, countdown, evidence, and target.
	Listed map[string]int `json:"listed,omitempty"`
}

// effectLegacyCount reproduces the count rules of the Effects Overlay before Phase 2B.3: the engine listed the first
// maxViewEffects tracked instances in priority order, the panel title counted those that passed the panel's relevance
// filter, and the hint added the engine's omitted count to the rows that did not fit.
type effectLegacyCount struct {
	Tracked       int `json:"tracked"`
	Listed        int `json:"listed"`
	EngineOmitted int `json:"engineOmitted"`
	Title         int `json:"title"`
}

// effectViewCost compares building and serializing the Effects view under the current rules with the pre-2B.3 rules
// (every tracked instance in priority order, at most maxViewEffects). Both include the same tracker snapshot, whose kind
// classification is also timed on its own.
type effectViewCost struct {
	Samples           int     `json:"samples"`
	CurrentMeanMicros float64 `json:"currentMeanMicros"`
	LegacyMeanMicros  float64 `json:"legacyMeanMicros"`
	KindMeanMicros    float64 `json:"kindClassificationMeanMicros"`
	CurrentMeanBytes  float64 `json:"currentMeanBytes"`
	LegacyMeanBytes   float64 `json:"legacyMeanBytes"`
	CurrentMaxBytes   int     `json:"currentMaxBytes"`
	LegacyMaxBytes    int     `json:"legacyMaxBytes"`
}

type effectInventoryResult struct {
	Name            string                 `json:"name"`
	UDPPackets      int                    `json:"udpPackets"`
	Counters        EffectCounters         `json:"counters"`
	DistinctEffects int                    `json:"distinctEffects"`
	Instances       int                    `json:"instances"`
	Samples         int                    `json:"samples"`
	MaxCounts       ViewEffectCounts       `json:"maxCounts"`
	MaxLegacy       effectLegacyCount      `json:"maxLegacy"`
	PeakSample      *effectInventorySample `json:"peakSample,omitempty"`
	// LegacyPeakSample is the sample with the most tracked instances, where the old title and hint diverged most.
	LegacyPeakSample   *effectInventorySample `json:"legacyPeakSample,omitempty"`
	FinalSample        *effectInventorySample `json:"finalSample,omitempty"`
	LegacyOverflowSeen bool                   `json:"legacyOverflowSeen"`
	ViewCost           effectViewCost         `json:"viewCost"`
	// ClearedWithoutRecord counts instances that a session boundary or reset removed without an ended record.
	ClearedWithoutRecord int                     `json:"clearedWithoutRecord"`
	Effects              []*effectInventoryEntry `json:"effects"`
}

func readReplayPCAPList(t *testing.T, listPath string) []equivalencePCAP {
	t.Helper()
	file, err := os.Open(listPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var pcaps []equivalencePCAP
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), byteOrderMark))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, found := strings.Cut(line, "|")
		if !found {
			t.Fatalf("malformed PCAP list line %q", line)
		}
		pcaps = append(pcaps, equivalencePCAP{name: name, path: path})
	}
	if len(pcaps) == 0 {
		t.Fatal("PCAP list is empty")
	}
	return pcaps
}

func TestEffectInventoryCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_EFFECT_INVENTORY_OUT")
	if out == "" {
		t.Skip("WWLOGS_EFFECT_INVENTORY_OUT is not set")
	}
	master := os.Getenv("WWLOGS_EFFECT_INVENTORY_MASTER_DB")
	listPath := os.Getenv("WWLOGS_EFFECT_INVENTORY_PCAPS")
	if master == "" || listPath == "" {
		t.Fatal("WWLOGS_EFFECT_INVENTORY_MASTER_DB and WWLOGS_EFFECT_INVENTORY_PCAPS are required")
	}
	pcaps := readReplayPCAPList(t, listPath)
	key, err := LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := LoadGameMasterDatabase(master, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	inputs := equivalenceInputs{localIP: "192.168.1.2"}
	for _, capture := range pcaps {
		t.Run(capture.name, func(t *testing.T) {
			result := effectInventoryReplay(t, inputs, masters, key, capture)
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, capture.name+".effects.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("effects: %d ids, %d instances, %d samples, max counts %+v, legacy max %+v", result.DistinctEffects, result.Instances, result.Samples, result.MaxCounts, result.MaxLegacy)
		})
	}
}

func effectInventoryReplay(t *testing.T, inputs equivalenceInputs, masters *GameMaster, key *StaticRSA, capture equivalencePCAP) effectInventoryResult {
	t.Helper()
	entries := map[uint64]*effectInventoryEntry{}
	type seenInstance struct {
		effectID    uint64
		hasID       bool
		lastUpdated time.Time
		stack       uint64
		hasStack    bool
		first       time.Time
	}
	live := map[effectKey]seenInstance{}
	var tracker *EffectTracker
	endedSeen := 0
	result := effectInventoryResult{Name: capture.name}
	var cost effectViewCost
	var costTotals [5]float64
	entryFor := func(id uint64, at time.Time) *effectInventoryEntry {
		entry := entries[id]
		if entry != nil {
			return entry
		}
		entry = &effectInventoryEntry{
			EffectID: id, ByEvidence: map[string]int{}, ByOrigin: map[string]int{}, HolderRelations: map[string]int{},
			SourceRelations: map[string]int{}, CombatClass: map[string]int{}, CombatBasis: map[string]int{}, Kind: map[string]int{}, DisplayCategory: map[string]int{},
			FiniteDurations: map[string]int{}, UnknownRemaining: map[string]int{}, Ended: map[string]int{},
			FirstSeen: at.UTC().Format(time.RFC3339Nano), extra: map[uint64]bool{}, concurrent: map[effectKey]int{},
		}
		if static, ok := masters.buffStatic(id); ok {
			entry.Definition = true
			if static.HasDurationPolicy {
				policy := static.DurationPolicy
				entry.DurationPolicy = &policy
			}
			if static.HasDuration {
				duration := static.DurationSeconds
				entry.StaticDuration = &duration
			}
			if static.HasStackLimit {
				limit := static.StackLimit
				entry.StackLimit = &limit
			}
			if static.HasAttribute && static.HasModifier {
				entry.Attribute = attributeTypeNames[static.Attribute]
				if entry.Attribute == "" {
					entry.Attribute = "attribute_" + formatUint(static.Attribute)
				}
			}
			entry.DeveloperTags = append([]string(nil), static.Tags...)
		}
		if term, ok := masters.buffTerm(id); ok {
			entry.Term = term.Name
		}
		entries[id] = entry
		return entry
	}
	// Relations, classes, kinds and roles are read once per instance, at the end of its life or of the capture, when the
	// tracker's relation evidence is most complete.
	classify := func(p *Processor, effect EffectInstanceSnapshot) {
		if effect.EffectID == nil {
			return
		}
		entry := entryFor(*effect.EffectID, effect.FirstObservedAt)
		entry.HolderRelations[string(effect.HolderRelation)]++
		entry.SourceRelations[string(effect.SourceRelation)]++
		entry.CombatClass[string(effect.CombatClass)]++
		entry.CombatBasis[effect.CombatBasis]++
		entry.Kind[string(effect.Kind)+"|"+effect.KindBasis]++
		// An ended record is classified as it was while active, as the view does for its recent-ended rows.
		whileActive := effect
		whileActive.Lifecycle = EffectLifecycleActive
		display := EffectDisplayFor(whileActive)
		entry.DisplayCategory[string(display.Eligibility)+":"+string(display.Target)]++
		if effect.HolderRelation == EffectRelationLocalRole || effect.HolderRelation == EffectRelationLocalOwned {
			if role := p.localRoleForEntity(effect.HolderEntityID, EntityIdentity{}); role != nil && !entry.extra[role.RoleID] {
				entry.extra[role.RoleID] = true
				entry.HolderRoles = append(entry.HolderRoles, role.RoleID)
			}
		}
	}
	recordEnded := func(p *Processor, record EffectEndedSnapshot) {
		if record.EffectID == nil {
			return
		}
		entry := entryFor(*record.EffectID, record.EndedAt)
		entry.Ended[string(record.Lifecycle)]++
		classify(p, record.EffectInstanceSnapshot)
		if !record.EndedAt.IsZero() && !record.FirstObservedAt.IsZero() {
			entry.LifetimeSeconds = append(entry.LifetimeSeconds, math.Round(record.EndedAt.Sub(record.FirstObservedAt).Seconds()*1000)/1000)
		}
	}
	sample := func(p *Processor) effectInventorySample {
		view := p.ViewSnapshot()
		counts := view.Effects.Counts
		if err := counts.AccountingError(len(view.Effects.Active)); err != nil {
			t.Fatalf("view effect counts at %s: %v (%+v)", p.CurrentFrameTime.UTC().Format(time.RFC3339Nano), err, counts)
		}
		for _, effect := range view.Effects.Active {
			if effect.Lifecycle != EffectLifecycleActive || effect.Display.Eligibility != EffectDisplayEligible {
				t.Fatalf("view lists an instance that is not display-eligible: %+v", effect)
			}
		}
		listed := map[string]int{}
		for _, effect := range view.Effects.Active {
			timed := "untimed"
			if effect.DurationSeconds != nil {
				timed = "timed"
			}
			listed[string(effect.Kind)+"|"+timed+"|"+string(effect.Evidence)+"|"+string(effect.Display.Target)]++
		}
		measureEffectViewCost(p, &cost, &costTotals)
		return effectInventorySample{At: p.CurrentFrameTime.UTC().Format(time.RFC3339Nano), Counts: counts, Old: legacyEffectCount(p), Listed: listed}
	}
	observe := func(p *Processor) {
		tracker = p.Effects
		tracker.mu.RLock()
		current := make(map[effectKey]seenInstance, len(tracker.instances))
		perEffect := map[uint64]int{}
		for key, instance := range tracker.instances {
			previous, existed := live[key]
			seen := seenInstance{effectID: instance.effectID, hasID: instance.hasEffectID, lastUpdated: instance.lastUpdatedAt, stack: instance.stack, hasStack: instance.hasStack, first: instance.firstObservedAt}
			if instance.hasEffectID {
				perEffect[instance.effectID]++
			}
			if !existed || previous.first != seen.first || previous.effectID != seen.effectID {
				if instance.hasEffectID {
					entry := entryFor(instance.effectID, instance.firstObservedAt)
					entry.Instances++
					entry.ByEvidence[string(instance.evidence)]++
					entry.ByOrigin[string(instance.origin)]++
					if instance.hasDuration {
						entry.FiniteDurations[formatFloat(math.Round(instance.duration*1000)/1000)]++
					} else {
						entry.UnknownRemaining[instance.unknownReason]++
					}
					if instance.holderMonster {
						entry.HolderMonster++
					}
					result.Instances++
				}
			} else if instance.hasEffectID {
				entry := entryFor(instance.effectID, instance.firstObservedAt)
				if seen.lastUpdated.After(previous.lastUpdated) {
					entry.Refreshes++
				}
				if seen.hasStack && (!previous.hasStack || previous.stack != seen.stack) {
					entry.StackChanges++
				}
			}
			if instance.hasEffectID && instance.hasStack {
				entry := entryFor(instance.effectID, instance.firstObservedAt)
				entry.MaxStack = max(entry.MaxStack, instance.stack)
			}
			current[key] = seen
		}
		ended := append([]EffectEndedSnapshot(nil), tracker.ended...)
		tracker.mu.RUnlock()
		for id, count := range perEffect {
			entry := entryFor(id, p.CurrentFrameTime)
			entry.MaxConcurrent = max(entry.MaxConcurrent, count)
		}
		// Every instance that left the live set since the previous packet has an ended record near the tail of the
		// bounded history, unless a session boundary cleared it (no record is written then).
		gone := map[effectKey]bool{}
		for key := range live {
			if _, still := current[key]; !still {
				gone[key] = true
			}
		}
		newEnded := 0
		for i := len(ended) - 1; i >= 0 && len(gone) > 0; i-- {
			record := ended[i]
			key := effectKey{record.SessionNumber, record.HolderEntityID, record.HolderGeneration, record.HandleID}
			if gone[key] {
				delete(gone, key)
				recordEnded(p, record)
				newEnded++
			}
		}
		result.ClearedWithoutRecord += len(gone)
		endedSeen += newEnded
		live = current
	}
	var peak *effectInventorySample
	p, stats := equivalenceReplay(t, inputs, masters, key, capture.path, nil, func(index int, p *Processor) {
		observe(p)
		if index%effectInventoryPublishEvery == 0 {
			s := sample(p)
			result.Samples++
			result.MaxCounts = maxViewEffectCounts(result.MaxCounts, s.Counts)
			result.MaxLegacy = effectLegacyCount{Tracked: max(result.MaxLegacy.Tracked, s.Old.Tracked), Listed: max(result.MaxLegacy.Listed, s.Old.Listed), EngineOmitted: max(result.MaxLegacy.EngineOmitted, s.Old.EngineOmitted), Title: max(result.MaxLegacy.Title, s.Old.Title)}
			if s.Old.EngineOmitted > 0 {
				result.LegacyOverflowSeen = true
			}
			if result.LegacyPeakSample == nil || s.Old.Tracked > result.LegacyPeakSample.Old.Tracked {
				copied := s
				result.LegacyPeakSample = &copied
			}
			if peak == nil || s.Counts.Eligible > peak.Counts.Eligible {
				copied := s
				peak = &copied
			}
		}
	})
	p.FinishCapture()
	observe(p)
	final := sample(p)
	result.FinalSample, result.PeakSample = &final, peak
	result.ViewCost = cost.finish(costTotals)
	result.UDPPackets = stats.UDPPackets
	snapshot := p.EffectSnapshotAtLastFrame()
	result.Counters = snapshot.Counters
	for _, effect := range snapshot.Effects {
		if effect.EffectID == nil {
			continue
		}
		entry := entryFor(*effect.EffectID, effect.FirstObservedAt)
		entry.ActiveAtEnd++
	}
	for _, effect := range snapshot.Effects {
		classify(p, effect)
	}
	_ = tracker
	for _, entry := range entries {
		sort.Float64s(entry.LifetimeSeconds)
		sort.Slice(entry.HolderRoles, func(i, j int) bool { return entry.HolderRoles[i] < entry.HolderRoles[j] })
		result.Effects = append(result.Effects, entry)
	}
	sort.Slice(result.Effects, func(i, j int) bool {
		if result.Effects[i].Instances != result.Effects[j].Instances {
			return result.Effects[i].Instances > result.Effects[j].Instances
		}
		return result.Effects[i].EffectID < result.Effects[j].EffectID
	})
	result.DistinctEffects = len(result.Effects)
	return result
}

// legacyEffectCount recomputes the pre-2B.3 overlay title and engine bound from the tracker snapshot.
func legacyEffectCount(p *Processor) effectLegacyCount {
	snapshot := p.EffectSnapshotAtLastFrame()
	priority := func(effect EffectInstanceSnapshot) int {
		local := localEffectRelation(effect.HolderRelation) || localEffectRelation(effect.SourceRelation)
		switch {
		case !local:
			return 5
		case effect.CombatClass == EffectCombatClassNonCombat:
			return 4
		case effect.Lifecycle != EffectLifecycleActive:
			return 3
		case effect.CombatClass == EffectCombatClassCombat:
			return 0
		}
		return 1
	}
	sort.SliceStable(snapshot.Effects, func(i, j int) bool { return priority(snapshot.Effects[i]) < priority(snapshot.Effects[j]) })
	count := effectLegacyCount{Tracked: len(snapshot.Effects)}
	for i, effect := range snapshot.Effects {
		if i >= maxViewEffects {
			count.EngineOmitted++
			continue
		}
		count.Listed++
		if effect.CombatClass != EffectCombatClassNonCombat && (localEffectRelation(effect.HolderRelation) || localEffectRelation(effect.SourceRelation)) {
			count.Title++
		}
	}
	return count
}

// measureEffectViewCost times one current and one legacy Effects view build, each with its own tracker snapshot.
func measureEffectViewCost(p *Processor, cost *effectViewCost, totals *[5]float64) {
	start := time.Now()
	current := p.viewEffects()
	currentData, _ := json.Marshal(current)
	currentElapsed := time.Since(start)

	start = time.Now()
	snapshot := p.EffectSnapshotAtLastFrame()
	priority := func(effect EffectInstanceSnapshot) int {
		local := localEffectRelation(effect.HolderRelation) || localEffectRelation(effect.SourceRelation)
		switch {
		case !local:
			return 5
		case effect.CombatClass == EffectCombatClassNonCombat:
			return 4
		case effect.Lifecycle != EffectLifecycleActive:
			return 3
		case effect.CombatClass == EffectCombatClassCombat:
			return 0
		}
		return 1
	}
	sort.SliceStable(snapshot.Effects, func(i, j int) bool { return priority(snapshot.Effects[i]) < priority(snapshot.Effects[j]) })
	legacy := struct {
		Active  []ViewEffect `json:"active"`
		Omitted int          `json:"omitted"`
	}{Active: []ViewEffect{}}
	for _, effect := range snapshot.Effects {
		if len(legacy.Active) == maxViewEffects {
			legacy.Omitted++
			continue
		}
		legacy.Active = append(legacy.Active, p.viewEffect(effect))
	}
	legacyData, _ := json.Marshal(legacy)
	legacyElapsed := time.Since(start)

	start = time.Now()
	for _, effect := range snapshot.Effects {
		_, _ = classifyEffectKind(effect.CombatClass, effect.CombatBasis, effect.Static)
	}
	kindElapsed := time.Since(start)

	cost.Samples++
	totals[0] += float64(currentElapsed.Microseconds())
	totals[1] += float64(legacyElapsed.Microseconds())
	totals[2] += float64(kindElapsed.Nanoseconds()) / 1000
	totals[3] += float64(len(currentData))
	totals[4] += float64(len(legacyData))
	cost.CurrentMaxBytes = max(cost.CurrentMaxBytes, len(currentData))
	cost.LegacyMaxBytes = max(cost.LegacyMaxBytes, len(legacyData))
}

func (c effectViewCost) finish(totals [5]float64) effectViewCost {
	if c.Samples == 0 {
		return c
	}
	n := float64(c.Samples)
	c.CurrentMeanMicros, c.LegacyMeanMicros, c.KindMeanMicros = totals[0]/n, totals[1]/n, totals[2]/n
	c.CurrentMeanBytes, c.LegacyMeanBytes = totals[3]/n, totals[4]/n
	return c
}

func maxViewEffectCounts(a, b ViewEffectCounts) ViewEffectCounts {
	return ViewEffectCounts{
		Tracked: max(a.Tracked, b.Tracked), EstimatedExpired: max(a.EstimatedExpired, b.EstimatedExpired), Active: max(a.Active, b.Active),
		NotLocal: max(a.NotLocal, b.NotLocal), Local: max(a.Local, b.Local), NonHUD: max(a.NonHUD, b.NonHUD), HUDUnresolved: max(a.HUDUnresolved, b.HUDUnresolved),
		PredictedExpired: max(a.PredictedExpired, b.PredictedExpired),
		Eligible:         max(a.Eligible, b.Eligible), Listed: max(a.Listed, b.Listed), Truncated: max(a.Truncated, b.Truncated),
		Cells: max(a.Cells, b.Cells), ListedCells: max(a.ListedCells, b.ListedCells), TruncatedCells: max(a.TruncatedCells, b.TruncatedCells),
	}
}

func formatUint(value uint64) string { return json.Number(jsonUint(value)).String() }

func jsonUint(value uint64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func formatFloat(value float64) string {
	data, _ := json.Marshal(value)
	return string(data)
}
