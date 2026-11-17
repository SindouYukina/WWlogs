//go:build wwdev

package capture

import (
	"testing"
)

func TestVisionDevLabelReachesSummonSkillResolution(t *testing.T) {
	master := &GameMaster{
		Locale:  "en-US",
		Visions: map[uint64]struct{}{6013: {}},
		VisionDevLabels: map[uint64]*CombatEventLabelTextJSONL{
			6013: {Locale: "en-US", Text: "Motorcycle - Floating Cannon Active", Kind: "unofficial-translation"},
		},
	}
	entity := RuntimeEntity{SummonSkillID: 6013, HasSummonSkillID: true, SummonType: SummonTypeConcomitantMotorcycle, HasSummonType: true}
	result := ResolveSummonSkill(master, entity)
	if result == nil || result.Namespace != "Vision.Id" || result.Status != ResolutionCorrelated {
		t.Fatalf("Type 5 key must stay a correlated Vision candidate: %+v", result)
	}
	if result.DevLabel == nil || result.DevLabel.Kind != "unofficial-translation" {
		t.Fatalf("the Vision developer label must travel with the resolution: %+v", result)
	}
	view := summonSkillJSONL(EntityIdentity{SummonSkill: result})
	if view == nil || view.DevLabel == nil || view.DevLabel.Text != "Motorcycle - Floating Cannon Active" {
		t.Fatalf("JSONL view must expose the label: %+v", view)
	}
}
