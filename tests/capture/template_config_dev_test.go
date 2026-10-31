//go:build wwdev

package capture

import (
	"testing"
)

func TestVisionDevLabelReachesSummonSkillResolution(t *testing.T) {
	master := &gameMaster{
		locale:  "en-US",
		visions: map[uint64]struct{}{6013: {}},
		visionDevLabels: map[uint64]*combatEventLabelTextJSONL{
			6013: {Locale: "en-US", Text: "Motorcycle - Floating Cannon Active", Kind: "unofficial-translation"},
		},
	}
	entity := runtimeEntity{summonSkillID: 6013, hasSummonSkillID: true, summonType: summonTypeConcomitantMotorcycle, hasSummonType: true}
	result := resolveSummonSkill(master, entity)
	if result == nil || result.Namespace != "Vision.Id" || result.Status != resolutionCorrelated {
		t.Fatalf("Type 5 key must stay a correlated Vision candidate: %+v", result)
	}
	if result.DevLabel == nil || result.DevLabel.Kind != "unofficial-translation" {
		t.Fatalf("the Vision developer label must travel with the resolution: %+v", result)
	}
	view := summonSkillJSONL(entityIdentity{SummonSkill: result})
	if view == nil || view.DevLabel == nil || view.DevLabel.Text != "Motorcycle - Floating Cannon Active" {
		t.Fatalf("JSONL view must expose the label: %+v", view)
	}
}
