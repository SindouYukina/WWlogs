package capture

import (
	"testing"
)

func TestDamageStatisticsResetClearsSnapshot(t *testing.T) {
	statistics := NewDamageStatistics()
	statistics.addDamageEvent(combatEvent{
		SessionNumber: 1, SourceEntityID: 100, HasSourceEntity: true, TargetEntityID: 200, HasTargetEntity: true,
		EventTypeCandidate: "DAMAGE_CANDIDATE", Damage: 10,
	}, playerRelationLocal, playerRelationUnrelated)
	statistics.Reset()
	snapshot := statistics.Snapshot()
	if snapshot.DamageDealt != 0 || snapshot.DamageTaken != 0 || snapshot.UnattributedOutgoingDamage != 0 || len(snapshot.ByPlayerEntity) != 0 {
		t.Fatalf("reset left stale statistics: %+v", snapshot)
	}
	if snapshot.ByPlayerEntity == nil {
		t.Fatal("empty snapshot must return an initialized entity list")
	}
}
