package capture

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func newViewTestProcessor() *Processor {
	p := newPairingTestProcessor()
	p.Masters.Characters = map[uint64]MasterRecord{
		1409: {ID: 1409, Name: "Verified Role", NameStatus: string(ResolutionVerified)},
		1508: {ID: 1508, Name: "Correlated Role", NameStatus: string(ResolutionCorrelated)},
	}
	return p
}

func TestViewSnapshotProjectsEngineTotals(t *testing.T) {
	p := newViewTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	pairingFrame(p, 1)
	writeTestDamage(p, 100, 200, 1, 120)
	writeTestDamage(p, 101, 200, 2, 30)
	writeTestDamage(p, 200, 100, 3, 9)
	view := p.ViewSnapshot()
	statistics := p.DamageStatistics.Snapshot()
	if view.Statistics.DamageDealt != statistics.DamageDealt || view.Statistics.DamageTaken != statistics.DamageTaken ||
		view.CaptureSessionID != statistics.CaptureSessionID || view.StatisticsIntervalID != statistics.StatisticsIntervalID {
		t.Fatalf("view totals differ from the engine: view=%+v engine dealt=%d taken=%d", view.Statistics, statistics.DamageDealt, statistics.DamageTaken)
	}
	if view.SchemaVersion != ViewSnapshotSchemaVersion || view.Health.DataIntegrity != statistics.Integrity.Status {
		t.Fatalf("view header = %+v", view)
	}
	names := map[uint64]ViewRole{}
	for _, role := range view.Roles {
		names[role.RoleID] = role
	}
	if role := names[1409]; role.Name != "Verified Role" || role.DamageDealt != 120 || role.DamageTaken != 9 {
		t.Fatalf("role 1409 = %+v", role)
	}
	// Only verified Master names are shown; anything else stays UNKNOWN with its RoleId.
	if role := names[1508]; role.Name != "" || role.NameStatus != string(ResolutionUnknown) || role.DamageDealt != 30 {
		t.Fatalf("role 1508 = %+v", role)
	}
}

func TestViewSnapshotIsIndependentOfLaterChanges(t *testing.T) {
	p := newViewTestProcessor()
	pairingFrame(p, 1)
	writeTestDamage(p, 100, 200, 1, 50)
	view := p.ViewSnapshot()
	before, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	writeTestDamage(p, 100, 200, 2, 70)
	p.Context.invalidate("in", pairingAt(2), "test_gap")
	after, _ := json.Marshal(view)
	if !bytes.Equal(before, after) {
		t.Fatal("a published view changed when the processor changed")
	}
	// Changing a view's slices does not reach the engine or the next view.
	view.Roles[0].DamageDealt = 999
	view.Statistics.Integrity.Risks = append(view.Statistics.Integrity.Risks, "tampered")
	next := p.ViewSnapshot()
	if next.Roles[0].DamageDealt != 120 || strings.Contains(strings.Join(next.Statistics.Integrity.Risks, ","), "tampered") {
		t.Fatalf("a view shares state with the engine: %+v", next.Roles)
	}
}

func TestViewSnapshotCarriesNoProtocolIdentifiers(t *testing.T) {
	p := newViewTestProcessor()
	pairingFrame(p, 1)
	writeTestDamage(p, 100, 200, 4242, 50)
	data, err := json.Marshal(p.ViewSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"rpcId"`, `"entityId"`, `"handleId"`, `"holderEntityId"`, `"sourceEntityId"`, `"byPlayerEntity"`} {
		if bytes.Contains(data, []byte(field)) {
			t.Fatalf("view snapshot exposes %s", field)
		}
	}
}

// writeSessionDamage writes a local outgoing damage event of the processor's current protocol session.
func writeSessionDamage(p *Processor, rpcID uint64, amount int64) {
	event := testDamageEvent(p, 100, 200)
	event.EventTypeCandidate, event.Damage, event.RPCID, event.SessionNumber = "DAMAGE_CANDIDATE", amount, rpcID, p.SessionNumber
	p.WriteDamageEvent(event)
}

func TestHostStartsNewIntervalPerProtocolSession(t *testing.T) {
	for _, host := range []bool{false, true} {
		p := newViewTestProcessor()
		p.IntervalPerProtocolSession = host
		p.SessionNumber = 1
		p.startProtocolSessionStatistics(pairingAt(0))
		pairingFrame(p, 1)
		writeSessionDamage(p, 1, 40)
		first := p.DamageStatistics.Snapshot()
		p.SessionNumber = 2
		p.startProtocolSessionStatistics(pairingAt(2))
		pairingFrame(p, 3)
		writeSessionDamage(p, 1, 15) // RPC ids restart with the protocol session
		second := p.DamageStatistics.Snapshot()
		if !host {
			// The CLI keeps one interval per capture, exactly as before.
			if second.StatisticsIntervalID != first.StatisticsIntervalID || second.DamageDealt != 55 {
				t.Fatalf("CLI interval changed: first=%d second=%d dealt=%d", first.StatisticsIntervalID, second.StatisticsIntervalID, second.DamageDealt)
			}
			continue
		}
		if second.StatisticsIntervalID != first.StatisticsIntervalID+1 || second.DamageDealt != 15 || first.DamageDealt != 40 {
			t.Fatalf("host interval did not restart: first=%+v second dealt=%d interval=%d", first.StatisticsIntervalID, second.DamageDealt, second.StatisticsIntervalID)
		}
		if second.Rolling5sDPS.Reason == "multiple_sessions_require_separate_statistics" {
			t.Fatal("the new interval still mixes protocol sessions")
		}
		view := p.ViewSnapshot()
		if view.StatisticsIntervalID != second.StatisticsIntervalID || view.Statistics.DamageDealt != 15 || len(view.Encounters.Recent) != 0 {
			t.Fatalf("view mixes protocol sessions: %+v", view.Encounters)
		}
	}
}

func TestViewSnapshotsRepeatExactly(t *testing.T) {
	p := newViewTestProcessor()
	pairingFrame(p, 1)
	writeTestDamage(p, 100, 200, 1, 50)
	if !reflect.DeepEqual(p.ViewSnapshot(), p.ViewSnapshot()) {
		t.Fatal("two views of the same state differ")
	}
}

func TestViewRoleSharesUseConfirmedIntervalTotals(t *testing.T) {
	p := newViewTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1508, true)
	pairingFrame(p, 1)
	writeTestDamage(p, 100, 200, 1, 120)
	writeTestDamage(p, 101, 200, 2, 30)
	view := p.ViewSnapshot()
	for _, role := range view.Roles {
		if role.DamageShare == nil || *role.DamageShare != float64(role.DamageDealt)/150 {
			t.Fatal("role share does not use confirmed totals")
		}
	}
}

func TestViewElapsedUsesCaptureBoundariesAndWithholdsUnreliableTime(t *testing.T) {
	p := newViewTestProcessor()
	encounter := EncounterSnapshot{StartedAt: pairingAt(1), EndedAt: pairingAt(11), Integrity: intactIntegrity()}
	view := p.viewEncounter(encounter)
	if view.ElapsedSeconds == nil || *view.ElapsedSeconds != 10 {
		t.Fatal("elapsed time did not use capture boundaries")
	}
	encounter.Integrity.Status = IntegrityUnreliable
	if p.viewEncounter(encounter).ElapsedSeconds != nil {
		t.Fatal("uncertain boundaries produced a precise elapsed time")
	}
}

func TestUnassignedShareRetainsConfirmedDenominator(t *testing.T) {
	if unassignedRoleShare(UnassignedRoleTotals{}, 0) != nil {
		t.Fatal("empty denominator has a share")
	}
	share := unassignedRoleShare(UnassignedRoleTotals{DamageDealt: 25}, 100)
	if share == nil || *share != 0.25 {
		t.Fatal("unassigned share changed confirmed denominator")
	}
	p := newViewTestProcessor()
	addTestRuntimeEntity(p, 150, nil)
	p.ObserveLocalRoleEntity(150)
	writeTestDamage(p, 100, 200, 1, 75)
	writeTestDamage(p, 150, 200, 2, 25)
	view := p.ViewSnapshot()
	assertRoleTotalsReconcile(t, p.DamageStatistics.Snapshot())
	if view.Statistics.DamageDealt != 100 || view.UnassignedRole.DamageDealt != 25 || view.UnassignedRole.OutgoingEvents != 1 || view.UnassignedRoleShare == nil || *view.UnassignedRoleShare != 0.25 || *view.Roles[0].DamageShare != 0.75 {
		t.Fatal("view hid or reassigned local damage")
	}
}
