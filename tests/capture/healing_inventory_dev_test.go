//go:build wwdev

package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// This development-profile, test-only collector inventories healing evidence in archived captures for the v0.6.0
// healing statistics research. For every DamageExecute result the Master classifies as healing (CalculateType 1) it
// records the reported magnitude, the response's ChangeLife, the source and target relations and role, the encounter
// assignment and repeats. For damage results it records how ChangeLife compares with the amount. With a protocol schema
// it also reads the Life and LifeMax attributes the server reports for entities and checks, between two Life reports of
// one entity, whether the Life change equals the healing and damage ChangeLife observed in between. The captures are
// only read.
//
// Environment:
//
//	WWLOGS_HEALING_OUT        output directory (required to run)
//	WWLOGS_HEALING_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_HEALING_MASTER_DB  Master SQLite database
//	WWLOGS_HEALING_SCHEMA     optional protocol schema JSON, for the Life attribute cross-check

type healingInventoryEvent struct {
	Key          string `json:"key"`
	At           string `json:"at"`
	Kind         string `json:"kind"`
	Flow         string `json:"flow"`
	Assignment   string `json:"assignment"`
	Source       uint64 `json:"source"`
	SourceRel    string `json:"sourceRelation"`
	SourceRoleID uint64 `json:"sourceRoleId,omitempty"`
	Target       uint64 `json:"target"`
	TargetRel    string `json:"targetRelation"`
	TargetRoleID uint64 `json:"targetRoleId,omitempty"`
	Amount       int64  `json:"amount"`
	ChangeLife   *int32 `json:"changeLife"`
	DamageID     uint64 `json:"damageId,omitempty"`
	SkillID      uint64 `json:"skillId,omitempty"`
}

type healingLifeCheck struct {
	Intervals       int `json:"intervals"`
	WithHealing     int `json:"intervalsWithHealing"`
	HealingMatched  int `json:"healingIntervalsMatched"`
	HealingMismatch int `json:"healingIntervalsMismatched"`
	DamageOnlyMatch int `json:"damageOnlyIntervalsMatched"`
	DamageOnlyMiss  int `json:"damageOnlyIntervalsMismatched"`
	// UnexplainedGain counts intervals where Life rose with no healing result at all (regeneration or another source).
	UnexplainedGain       int      `json:"unexplainedGainIntervals"`
	UnexplainedGainAmount int64    `json:"unexplainedGainAmount"`
	GainExamples          []string `json:"unexplainedGainExamples,omitempty"`
	// Zero-ChangeLife healing results whose target was known to be at full Life just before.
	ZeroHealAtFullLife    int      `json:"zeroHealingAtFullLife"`
	ZeroHealBelowFullLife int      `json:"zeroHealingBelowFullLife"`
	ZeroHealLifeUnknown   int      `json:"zeroHealingLifeUnknown"`
	Examples              []string `json:"examples,omitempty"`
}

type healingInventory struct {
	Capture         string `json:"capture"`
	DamageResults   int    `json:"damageResults"`
	DamageWithLife  int    `json:"damageWithChangeLife"`
	DamageLifeEqual int    `json:"damageChangeLifeEqualsAmount"`
	DamageLifeLess  int    `json:"damageChangeLifeBelowAmount"`
	DamageLifeMore  int    `json:"damageChangeLifeAboveAmount"`
	DamageLifeGain  int    `json:"damageChangeLifeNegative"`
	// GainWithoutHeal counts decoded results of any kind whose ChangeLife raised HP but that are not healing records.
	GainWithoutHeal  int               `json:"lifeGainResultsNotHealing"`
	SelfHealing      int               `json:"selfHealing"`
	HealerUnknown    int               `json:"healingWithoutSourceRole"`
	TargetUnknown    int               `json:"healingTargetNotLocal"`
	Healing          int               `json:"healingResults"`
	HealingDuplicate int               `json:"healingRepeatedKeys"`
	HealingFlows     map[string]int    `json:"healingFlows"`
	HealingAssigned  map[string]int    `json:"healingAssignments"`
	Categories       map[string]int    `json:"healingCategories"`
	Reported         int64             `json:"reportedTotal"`
	Effective        int64             `json:"effectiveTotal"`
	Overheal         int64             `json:"overhealTotal"`
	LocalTeam        int64             `json:"effectiveToLocalTeam"`
	LocalTeamKnown   int64             `json:"effectiveToLocalTeamWithSourceRole"`
	OtherCalculate   map[string]int    `json:"otherCalculateTypes"`
	Life             *healingLifeCheck `json:"lifeCheck,omitempty"`
	// Engine is what the statistics engine reports for the same replay: the interval's local healing and each
	// encounter's healing, so the collector's own sum can be compared with it.
	Engine *healingEngineReport    `json:"engine,omitempty"`
	Events []healingInventoryEvent `json:"healingEvents"`
}

type healingEngineReport struct {
	Interval        HealingTotals   `json:"interval"`
	NotLocal        uint64          `json:"notLocalEvents"`
	Duplicates      uint64          `json:"duplicateEvents"`
	Inconsistent    uint64          `json:"inconsistentEvents"`
	Encounters      []HealingResult `json:"encounters"`
	EncounterSum    int64           `json:"encounterEffectiveSum"`
	MatchesRecords  bool            `json:"intervalMatchesRecords"`
	PeakDPS         []float64       `json:"encounterPeakDps"`
	PeakMatchesBest bool            `json:"peakMatchesTimeline"`
}

type lifeReport struct {
	at      time.Time
	ordinal uint64
	order   int
	life    int64
	max     int64
}

func TestHealingInventoryCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_HEALING_OUT")
	if out == "" {
		t.Skip("WWLOGS_HEALING_OUT is not set")
	}
	master, listPath := os.Getenv("WWLOGS_HEALING_MASTER_DB"), os.Getenv("WWLOGS_HEALING_PCAPS")
	if master == "" || listPath == "" {
		t.Fatal("WWLOGS_HEALING_MASTER_DB and WWLOGS_HEALING_PCAPS are required")
	}
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := loadGameMasterDatabase(master, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	var schema *protocolSchema
	if path := os.Getenv("WWLOGS_HEALING_SCHEMA"); path != "" {
		if schema, err = loadProtocolSchema(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, capture := range readReplayPCAPList(t, listPath) {
		t.Run(capture.name, func(t *testing.T) {
			inventory := inventoryHealing(t, masters, key, schema, capture.name, readPerturbationPackets(t, capture.path))
			data, err := json.MarshalIndent(inventory, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, capture.name+".healing.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %d healing, reported %d, effective %d, overheal %d, local team %d (role known %d), flows %v, categories %v",
				capture.name, inventory.Healing, inventory.Reported, inventory.Effective, inventory.Overheal, inventory.LocalTeam, inventory.LocalTeamKnown, inventory.HealingFlows, inventory.Categories)
		})
	}
}

func inventoryHealing(t *testing.T, masters *gameMaster, key *staticRSA, schema *protocolSchema, name string, packets []perturbationPacket) healingInventory {
	t.Helper()
	var records, events, hp bytes.Buffer
	p := newProcessor(key, io.Discard, io.Discard, nil)
	p.masters = masters
	p.dev.encounterOut = &records
	p.dev.combatEventsJSONL = &events
	if schema != nil {
		p.dev.protocolSchema = schema
		p.dev.hpStateOut = &hp
	}
	streams := make(map[string]*stream)
	stats := captureStats{}
	for _, packet := range packets {
		stats.udpPackets++
		consumeDatagram(packet.payload, packet.direction, packet.at, streams, p, &stats)
	}
	finishTransport(streams, p, &stats)
	p.finishCapture()

	type jsonlEvent struct {
		SessionNumber int    `json:"sessionNumber"`
		RPCID         uint64 `json:"rpcId"`
		CombatResult  struct {
			ChangeLife    *int32 `json:"changeLifeRaw"`
			CalculateType *int32 `json:"calculateType"`
			Magnitude     int64  `json:"combatResultAbs"`
		} `json:"combatResult"`
	}
	changeLife := map[string]*int32{}
	inventory := healingInventory{Capture: name, HealingFlows: map[string]int{}, HealingAssigned: map[string]int{}, Categories: map[string]int{}, OtherCalculate: map[string]int{}}
	scan := func(buffer *bytes.Buffer, each func([]byte)) {
		scanner := bufio.NewScanner(bytes.NewReader(buffer.Bytes()))
		scanner.Buffer(make([]byte, 1<<20), 1<<26)
		for scanner.Scan() {
			each(scanner.Bytes())
		}
	}
	scan(&events, func(line []byte) {
		var event jsonlEvent
		if json.Unmarshal(line, &event) != nil || event.RPCID == 0 {
			return
		}
		key := keyString(event.SessionNumber, event.RPCID)
		changeLife[key] = event.CombatResult.ChangeLife
		if life := event.CombatResult.ChangeLife; life != nil && *life < 0 && (event.CombatResult.CalculateType == nil || *event.CombatResult.CalculateType != 1) {
			inventory.GainWithoutHeal++
		}
		if calc := event.CombatResult.CalculateType; calc != nil && *calc != 0 && *calc != 1 {
			inventory.OtherCalculate[jsonNumber(int64(*calc))]++
		}
	})
	seen := map[string]bool{}
	scan(&records, func(line []byte) {
		var record struct {
			Type string `json:"type"`
			CombatEventRecord
			Assignment string `json:"assignment"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type != "COMBAT_EVENT_RECORD" {
			return
		}
		key := keyString(record.Key.ProtocolSession, record.Key.RPCID)
		life := changeLife[key]
		switch record.Kind {
		case CombatEventDamage:
			inventory.DamageResults++
			if life != nil {
				inventory.DamageWithLife++
				if *life < 0 {
					inventory.DamageLifeGain++
				}
				switch value := int64(*life); {
				case value == record.Amount:
					inventory.DamageLifeEqual++
				case value < record.Amount:
					inventory.DamageLifeLess++
				default:
					inventory.DamageLifeMore++
				}
			}
		case CombatEventHealing:
			inventory.Healing++
			if seen[key] {
				inventory.HealingDuplicate++
			}
			seen[key] = true
			event := healingInventoryEvent{
				Key: key, At: record.ObservedAt.Format(time.RFC3339Nano), Kind: string(record.Kind), Flow: string(record.Flow), Assignment: record.Assignment,
				Source: record.Source.EntityID, SourceRel: record.Source.Relation, Target: record.Target.EntityID, TargetRel: record.Target.Relation,
				Amount: record.Amount, ChangeLife: life, DamageID: record.DamageID, SkillID: record.SkillID,
			}
			if record.SourceRole != nil {
				event.SourceRoleID = record.SourceRole.RoleID
			}
			if record.TargetRole != nil {
				event.TargetRoleID = record.TargetRole.RoleID
			}
			inventory.HealingFlows[event.Flow]++
			if event.Source == event.Target {
				inventory.SelfHealing++
			}
			if event.SourceRoleID == 0 {
				inventory.HealerUnknown++
			}
			if event.TargetRel != playerRelationLocal.String() {
				inventory.TargetUnknown++
			}
			inventory.HealingAssigned[assignmentKind(record.Assignment)]++
			inventory.Reported += record.Amount
			effective, category := int64(0), "no_change_life"
			if life != nil {
				switch value := -int64(*life); {
				case value == record.Amount:
					effective, category = value, "change_life_equals_reported"
				case value == 0:
					category = "change_life_zero"
				case value > 0 && value < record.Amount:
					effective, category = value, "change_life_below_reported"
				case value > record.Amount:
					effective, category = value, "change_life_above_reported"
				default:
					category = "change_life_positive"
				}
			}
			inventory.Categories[category]++
			if life != nil {
				inventory.Effective += effective
				inventory.Overheal += record.Amount - min(record.Amount, effective)
			}
			if record.Target.Relation == playerRelationLocal.String() {
				inventory.LocalTeam += effective
				if event.SourceRoleID != 0 || (record.Source.Relation == playerRelationLocal.String()) {
					inventory.LocalTeamKnown += effective
				}
			}
			inventory.Events = append(inventory.Events, event)
		}
	})
	if schema != nil {
		inventory.Life = checkLifeReports(&hp, changeLife, &records)
	}
	statistics := p.damageStatisticsSnapshot()
	engine := &healingEngineReport{
		Interval: statistics.Healing, NotLocal: statistics.HealingNotLocalEvents, Duplicates: statistics.HealingDuplicateEvents,
		Inconsistent: statistics.HealingInconsistentEvents, PeakMatchesBest: true,
	}
	encounters := append([]EncounterSnapshot(nil), statistics.Encounters.Completed...)
	if statistics.Encounters.Current != nil {
		encounters = append(encounters, *statistics.Encounters.Current)
	}
	timelines := map[string]EncounterTimelineSnapshot{}
	for _, timeline := range p.damageStatistics.encounterTimelines() {
		timelines[timeline.EncounterID] = timeline
	}
	for _, encounter := range encounters {
		engine.Encounters = append(engine.Encounters, encounter.Healing)
		engine.EncounterSum += encounter.Healing.Effective
		engine.PeakDPS = append(engine.PeakDPS, encounter.PeakDPS.Value)
		best := int64(0)
		for _, point := range timelines[encounter.EncounterID].Points {
			best = max(best, point.Damage)
		}
		if encounter.PeakDPS.Damage != best {
			engine.PeakMatchesBest = false
		}
	}
	engine.MatchesRecords = statistics.Healing.Effective == inventory.LocalTeam
	inventory.Engine = engine
	return inventory
}

func keyString(session int, rpc uint64) string {
	return jsonNumber(int64(session)) + "/" + jsonNumber(int64(rpc))
}

func jsonNumber(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func assignmentKind(assignment string) string {
	switch assignment {
	case "", statisticsAssignmentNotCounted, statisticsAssignmentDuplicate:
		return "assignment:" + assignment
	}
	return "encounter_or_outside"
}

// checkLifeReports compares, per entity, consecutive server Life reports with the ChangeLife of the damage and healing
// results observed between them: Life(after) should equal Life(before) - sum(damage ChangeLife) - sum(healing ChangeLife)
// when ChangeLife is the applied HP change, with ChangeLife positive for damage and negative for healing.
func checkLifeReports(hp *bytes.Buffer, changeLife map[string]*int32, records *bytes.Buffer) *healingLifeCheck {
	reports := map[uint64][]lifeReport{}
	maxLife := map[uint64]int64{}
	scanner := bufio.NewScanner(bytes.NewReader(hp.Bytes()))
	scanner.Buffer(make([]byte, 1<<20), 1<<26)
	type hpLine struct {
		FrameOrdinal uint64 `json:"frameOrdinal"`
		Timestamp    string `json:"timestamp"`
		EntityID     uint64 `json:"entityId"`
		Attribute    int64  `json:"attributeType"`
		Base         int64  `json:"baseValue"`
		Current      int64  `json:"currentValue"`
		Order        int    `json:"orderInMessage"`
	}
	for scanner.Scan() {
		var line hpLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, line.Timestamp)
		if err != nil {
			continue
		}
		value := line.Base
		if line.Current != 0 {
			value = line.Current
		}
		if line.Attribute == attributeTypeLifeMax {
			maxLife[line.EntityID] = value
			continue
		}
		reports[line.EntityID] = append(reports[line.EntityID], lifeReport{at: at, ordinal: line.FrameOrdinal, order: line.Order, life: value, max: maxLife[line.EntityID]})
	}
	// Every damage and healing result with its target, time and ChangeLife.
	type change struct {
		at      time.Time
		healing bool
		amount  int64
		life    int64
		key     string
	}
	changes := map[uint64][]change{}
	recordScanner := bufio.NewScanner(bytes.NewReader(records.Bytes()))
	recordScanner.Buffer(make([]byte, 1<<20), 1<<26)
	for recordScanner.Scan() {
		var record struct {
			Type string `json:"type"`
			CombatEventRecord
		}
		if json.Unmarshal(recordScanner.Bytes(), &record) != nil || record.Type != "COMBAT_EVENT_RECORD" {
			continue
		}
		key := keyString(record.Key.ProtocolSession, record.Key.RPCID)
		// An absent ChangeLife is the proto3 default zero.
		var applied int64
		if life := changeLife[key]; life != nil {
			applied = int64(*life)
		}
		changes[record.Target.EntityID] = append(changes[record.Target.EntityID], change{at: record.ObservedAt, healing: record.Kind == CombatEventHealing, amount: record.Amount, life: applied, key: key})
	}
	check := &healingLifeCheck{}
	for entity, list := range reports {
		sort.SliceStable(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
		events := changes[entity]
		for i := 1; i < len(list); i++ {
			before, after := list[i-1], list[i]
			if !after.at.After(before.at) {
				continue
			}
			check.Intervals++
			var sum int64
			healed, damaged := false, false
			for _, c := range events {
				if c.at.After(before.at) && !c.at.After(after.at) {
					sum += c.life
					if c.healing {
						healed = true
						switch {
						case c.life != 0:
						case before.max <= 0:
							check.ZeroHealLifeUnknown++
						case before.life >= before.max:
							check.ZeroHealAtFullLife++
						default:
							check.ZeroHealBelowFullLife++
						}
					} else {
						damaged = true
					}
				}
			}
			matched := before.life-sum == after.life
			switch {
			case healed:
				check.WithHealing++
				if matched {
					check.HealingMatched++
				} else {
					check.HealingMismatch++
					if len(check.Examples) < 12 {
						check.Examples = append(check.Examples, "entity "+jsonNumber(int64(entity))+" "+before.at.Format("15:04:05.000")+"->"+after.at.Format("15:04:05.000")+" life "+jsonNumber(before.life)+"->"+jsonNumber(after.life)+" sum(changeLife) "+jsonNumber(sum)+" max "+jsonNumber(before.max))
					}
				}
			case damaged:
				if matched {
					check.DamageOnlyMatch++
				} else {
					check.DamageOnlyMiss++
				}
			case after.life > before.life:
				check.UnexplainedGain++
				check.UnexplainedGainAmount += after.life - before.life
				if len(check.GainExamples) < 12 {
					check.GainExamples = append(check.GainExamples, "entity "+jsonNumber(int64(entity))+" "+before.at.Format("15:04:05.000")+"->"+after.at.Format("15:04:05.000")+" life "+jsonNumber(before.life)+"->"+jsonNumber(after.life)+" max "+jsonNumber(before.max))
				}
			}
		}
	}
	return check
}
