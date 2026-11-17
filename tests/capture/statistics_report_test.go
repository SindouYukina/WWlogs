package capture

import (
	"strings"
	"testing"
)

func TestStatisticsReportShowsEncounter(t *testing.T) {
	p := newEncounterTestProcessor()
	p.DamageStatistics.ObserveBattleState(true, encounterAt(10), 1)
	p.DamageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 100, 11))
	p.CurrentFrameTime = encounterAt(20)
	line := p.CurrentStatisticsReport().String()
	for _, want := range []string{"encounter_state=ACTIVE", "encounter_dps=10.0", "encounter_confidence=live", "encounters=0"} {
		if !strings.Contains(line, want) {
			t.Fatalf("statistics line %q lacks %q", line, want)
		}
	}
	idle := newEncounterTestProcessor().CurrentStatisticsReport().String()
	if !strings.Contains(idle, "encounter_state=IDLE encounter_dps=unavailable encounter_confidence=none encounters=0") {
		t.Fatalf("an idle statistics line must state that no encounter exists: %q", idle)
	}
}
