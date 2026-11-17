package devcapture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wwlogs/internal/capture"
)

type firstEncounterSchemaSink struct {
	observe func(uint64)
}

type firstEncounterEventSink struct{ digest *equivalenceHashSink }

func (s firstEncounterEventSink) Write(data []byte) (int, error) {
	var row struct {
		EventType     string          `json:"eventType"`
		Timestamp     json.RawMessage `json:"timestamp"`
		SessionNumber int             `json:"sessionNumber"`
		RPCID         uint64          `json:"rpcId"`
		Source        struct {
			EntityID *uint64 `json:"entityId"`
		} `json:"source"`
		Target struct {
			EntityID *uint64 `json:"entityId"`
		} `json:"target"`
		Raw          json.RawMessage `json:"raw"`
		CombatResult json.RawMessage `json:"combatResult"`
		Healing      json.RawMessage `json:"healing,omitempty"`
		Response     json.RawMessage `json:"response,omitempty"`
		Push         json.RawMessage `json:"push,omitempty"`
	}
	if err := json.Unmarshal(data, &row); err != nil {
		return 0, err
	}
	if strings.HasPrefix(row.EventType, "DAMAGE_") || strings.HasPrefix(row.EventType, "HEALING_") {
		encoded, err := json.Marshal(row)
		if err != nil {
			return 0, err
		}
		if _, err = s.digest.Write(append(encoded, '\n')); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

// TestInitialAttributionReplayMatrix compares event identities and pairing independently of attribution totals.
func TestInitialAttributionReplayMatrix(t *testing.T) {
	inputs, enabled := equivalenceEnvironment(t)
	if !enabled {
		t.Skip("explicit replay matrix inputs are not supplied")
	}
	key, err := capture.LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	masters, err := capture.LoadGameMasterDatabase(inputs.master, inputs.locale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(inputs.out, 0700); err != nil {
		t.Fatal(err)
	}
	for _, item := range inputs.pcaps {
		t.Run(item.name, func(t *testing.T) {
			sink := newEquivalenceHashSink()
			p, packets := equivalenceReplay(t, inputs, masters, key, item.path, func(p *processor) { p.dev.combatEventsJSONL = firstEncounterEventSink{sink} }, nil)
			p.FinishCapture()
			statistics := p.DamageStatisticsSnapshot()
			assertEncounterTotalsConserved(t, statistics)
			assertRoleTotalsReconcile(t, statistics)
			result := struct {
				Events                                                              map[string]any `json:"events"`
				Requests, Responses, Matched, Unpaired, Invalid, Duplicate, Packets int
				Pairing                                                             capture.RPCPairingSnapshot       `json:"pairing"`
				Statistics                                                          capture.DamageStatisticsSnapshot `json:"statistics"`
			}{map[string]any{"lines": sink.lines, "sha256": fmt.Sprintf("%x", sink.hash.Sum(nil))}, p.Stats.DamageRequests, p.Stats.CandidateResponses, p.Stats.DecodedMatchedEvents, p.Stats.UnpairedResponses, p.Stats.InvalidResponses, p.Stats.DuplicateResponses, packets.UDPPackets, p.RPCPairingSnapshot(), statistics}
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(inputs.out, item.name+".json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func (w firstEncounterSchemaSink) Write(data []byte) (int, error) {
	var row struct {
		CommandID uint16 `json:"commandId"`
		Direction string `json:"direction"`
		Decoded   struct {
			Fields []struct {
				Number uint64          `json:"number"`
				Value  json.RawMessage `json:"value"`
			} `json:"fields"`
		} `json:"decoded"`
	}
	// Only retain the scalar identity in memory for an equality check. Never persist the decoded body or ID.
	if json.Unmarshal(data, &row) == nil && row.CommandID == capture.CmdBasicInfoNotify && row.Direction == "in" {
		for _, f := range row.Decoded.Fields {
			if f.Number == 15 {
				var id uint64
				if json.Unmarshal(f.Value, &id) == nil && id > 0 {
					w.observe(id)
				}
			}
		}
	}
	return len(data), nil
}

// TestFirstEncounterReplayTrace records bounded, identifier-free stage counters for an explicitly supplied capture.
// It does not open a live adapter, persist packet contents, or require private repository fixtures.
func TestFirstEncounterReplayTrace(t *testing.T) {
	path, master, out := os.Getenv("WWLOGS_FIRST_ENCOUNTER_PCAP"), os.Getenv("WWLOGS_FIRST_ENCOUNTER_MASTER"), os.Getenv("WWLOGS_FIRST_ENCOUNTER_OUT")
	if path == "" || master == "" || out == "" {
		t.Skip("explicit first-encounter replay inputs are not supplied")
	}
	key, err := capture.LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	masters, err := capture.LoadGameMasterDatabase(master, "en-US")
	if err != nil {
		t.Fatal(err)
	}
	var schema *protocolSchema
	if schemaPath := os.Getenv("WWLOGS_FIRST_ENCOUNTER_SCHEMA"); schemaPath != "" {
		schema, err = loadProtocolSchema(schemaPath)
		if err != nil {
			t.Fatal(err)
		}
	}
	var basicID uint64
	var basicAt string
	var basicBeforeDamage, basicMatchesLaterAnchor bool
	type stage struct {
		At                                                            string
		ProtocolSession, RoleReports, LocalPlayers, LocalRoleEntities int
		Requests, Responses, Matched, Unpaired, Duplicate             int
		Statistics                                                    capture.DamageStatisticsSnapshot
		ViewDamage                                                    int64
		ViewRoles                                                     int
	}
	var stages []stage
	truncated := false
	last := ""
	observe := func(_ int, p *processor) {
		s := p.DamageStatisticsSnapshotAt(capture.CaptureQueryTime{CaptureSessionID: p.DamageStatistics.CaptureSessionID, Timestamp: p.CurrentFrameTime})
		state := fmt.Sprintf("%d/%d/%d/%d/%d/%d/%s/%t/%t", p.SessionNumber, p.Stats.PlayerRoleReports, len(p.PlayerRelevance.LocalPlayerIDs), len(p.PlayerRelevance.RoleEntities), len(p.ScenePlayerIDs), len(s.Encounters.Completed), s.Encounters.State, p.Stats.DamageRequests > 0, p.Stats.DecodedMatchedEvents > 0)
		if state == last {
			return
		}
		if len(stages) >= 128 {
			truncated = true
			return
		}
		last = state
		v := p.ViewSnapshot()
		// Keep totals and trust, but omit player/entity identity tables from the trace.
		s.ByPlayerEntity, s.Roles = nil, nil
		for i := range s.Encounters.Completed {
			s.Encounters.Completed[i].Roles = nil
		}
		if s.Encounters.Current != nil {
			s.Encounters.Current.Roles = nil
		}
		stages = append(stages, stage{At: p.CurrentFrameTime.Format(encounterReplayTimeLayout), ProtocolSession: p.SessionNumber,
			RoleReports: p.Stats.PlayerRoleReports, LocalPlayers: len(p.PlayerRelevance.LocalPlayerIDs), LocalRoleEntities: len(p.PlayerRelevance.RoleEntities),
			Requests: p.Stats.DamageRequests, Responses: p.Stats.CandidateResponses, Matched: p.Stats.DecodedMatchedEvents,
			Unpaired: p.Stats.UnpairedResponses, Duplicate: p.Stats.DuplicateResponses, Statistics: s,
			ViewDamage: v.Statistics.DamageDealt, ViewRoles: len(v.Roles)})
		if v.Statistics.DamageDealt != s.DamageDealt {
			t.Fatal("statistics and view diverged")
		}
	}
	p, _ := equivalenceReplay(t, equivalenceInputs{localIP: "192.168.1.2"}, masters, key, path, func(p *processor) {
		p.IntervalPerProtocolSession = true
		p.dev.protocolSchema = schema
		p.dev.schemaDecodeOut = firstEncounterSchemaSink{observe: func(id uint64) {
			basicID = id
			basicAt = p.CurrentFrameTime.Format(encounterReplayTimeLayout)
			basicBeforeDamage = p.Stats.DamageRequests == 0
		}}
	}, func(index int, p *processor) {
		if basicID != 0 && p.PlayerRelevance.IsLocalPlayerID(basicID) {
			basicMatchesLaterAnchor = true
		}
		observe(index, p)
	})
	p.FinishCapture()
	observe(0, p)
	assertEncounterTotalsConserved(t, p.DamageStatisticsSnapshot())
	assertRoleTotalsReconcile(t, p.DamageStatisticsSnapshot())
	data, err := json.MarshalIndent(struct {
		Stages                      []stage `json:"stages"`
		Truncated                   bool    `json:"truncated"`
		BasicInfoAuditEnabled       bool    `json:"basicInfoAuditEnabled"`
		BasicInfoSeen               bool    `json:"basicInfoSeen"`
		BasicInfoAt                 string  `json:"basicInfoAt"`
		BasicInfoBeforeDamage       bool    `json:"basicInfoBeforeDamage"`
		BasicInfoMatchesLaterAnchor bool    `json:"basicInfoMatchesLaterAnchor"`
	}{stages, truncated, schema != nil, basicID != 0, basicAt, basicBeforeDamage, basicMatchesLaterAnchor}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0600); err != nil {
		t.Fatal(err)
	}
}
