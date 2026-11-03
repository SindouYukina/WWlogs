package capture

import (
	"strings"
	"testing"
)

func TestStatisticsReportShowsEncounter(t *testing.T) {
	p := newEncounterTestProcessor()
	p.damageStatistics.observeBattleState(true, encounterAt(10), 1)
	p.damageStatistics.addCombatEvent(testEncounterRecord(CombatFlowLocalOutgoing, 100, 11))
	p.currentFrameTime = encounterAt(20)
	line := p.currentStatisticsReport().String()
	for _, want := range []string{"encounter_state=ACTIVE", "encounter_dps=10.0", "encounter_confidence=live", "encounters=0"} {
		if !strings.Contains(line, want) {
			t.Fatalf("statistics line %q lacks %q", line, want)
		}
	}
	idle := newEncounterTestProcessor().currentStatisticsReport().String()
	if !strings.Contains(idle, "encounter_state=IDLE encounter_dps=unavailable encounter_confidence=none encounters=0") {
		t.Fatalf("an idle statistics line must state that no encounter exists: %q", idle)
	}
}
