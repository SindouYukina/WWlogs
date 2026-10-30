//go:build wwdev

package main

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestResolveSkillRefRequiresTheSourceActorToListTheSkill(t *testing.T) {
	master := &gameMaster{
		roleSkinMesh:    map[uint64]uint64{9001: 10},
		modelActor:      map[uint64]string{10: "actorA", 20: "actorV"},
		blueprintModel:  map[string]uint64{"VisionBP": 20},
		summonBlueprint: map[uint64]string{1: "VisionBP"},
		visionCast:      map[uint64][]uint64{6001: {1}},
		actorSkills:     map[string]map[uint64]struct{}{"actorA": {5001: {}, 6001: {}}, "actorB": {5002: {}, 6001: {}}, "actorV": {6001: {}}},
		skillActors:     map[uint64]map[string]struct{}{5001: {"actorA": {}}, 5002: {"actorB": {}}, 6001: {"actorA": {}, "actorB": {}, "actorV": {}}},
		commonSkills:    map[uint64]masterRecord{7001: {ID: 7001}},
		assetPath:       map[string]string{"actorA": "/Game/A.A_C"},
	}
	source := bulletSource{RoleSkinID: 9001, HasRoleSkin: true}
	if status, ns, actor, _ := resolveSkillRef(master, 5001, source); status != resolutionCorrelated || ns != "EntitySkillPreload.SkillId" || actor != "/Game/A.A_C" {
		t.Fatalf("a skill listed by the source actor must correlate: %v %q %q", status, ns, actor)
	}
	if status, _, _, _ := resolveSkillRef(master, 5002, source); status != resolutionUnknown {
		t.Fatalf("a skill the source actor does not list must stay unknown: %v", status)
	}
	if status, ns, actor, _ := resolveSkillRef(master, 5001, bulletSource{}); status != resolutionCorrelated || ns != "EntitySkillPreload.SkillId" || actor != "/Game/A.A_C" {
		t.Fatalf("a unique static skill owner must resolve without runtime identity: %v %q %q", status, ns, actor)
	}
	if status, _, _, _ := resolveSkillRef(master, 5002, source); status != resolutionUnknown {
		t.Fatalf("a known source actor must not be overridden by a different static skill owner: %v", status)
	}
	if status, _, _, _ := resolveSkillRef(master, 6001, bulletSource{}); status != resolutionUnknown {
		t.Fatalf("a SkillId shared by multiple actors must stay ambiguous: %v", status)
	}
	if status, ns, _, _ := resolveSkillRef(master, 7001, source); status != resolutionCorrelated || ns != "CommonSkillPreload.Id" {
		t.Fatalf("a common skill is not listed per actor but must still correlate: %v %q", status, ns)
	}
	if status, ns, _, _ := resolveSkillRef(master, 7001, bulletSource{}); status != resolutionCorrelated || ns != "CommonSkillPreload.Id" {
		t.Fatalf("a common skill needs no actor chain: %v %q", status, ns)
	}
	// The skill-derived echo link must not choose the validating actor when static ownership is ambiguous.
	echo := bulletSource{SkillID: 6001}
	if status, _, _, _ := resolveSkillRef(master, 6001, echo); status != resolutionUnknown {
		t.Fatalf("the skill itself must not select the actor that validates it: %v", status)
	}
	if status, _, _, evidence := resolveSkillRef(nil, 5001, source); status != resolutionUnknown || evidence != "no master database loaded" {
		t.Fatalf("no master, no resolution: %v %q", status, evidence)
	}
}

func TestSkillSourceCandidatesAndWalk(t *testing.T) {
	common := &decodedMessage{Type: "CombatCommon", Fields: []decodedField{scalarField("EntityId", 11)}}
	use := &decodedMessage{Type: "UseSkillInformation"}
	if got := skillSourceCandidates(use, nil, common); len(got) != 1 || got[0].id != 11 || got[0].basis != "UseSkillInformation.CombatCommon.EntityId" {
		t.Fatalf("UseSkillInformation uses its CombatCommon sender: %+v", got)
	}
	hit := &decodedMessage{Type: "HitInformation", Fields: []decodedField{scalarField("Id", 55)}}
	if got := skillSourceCandidates(hit, nil, common); len(got) != 1 || got[0].id != 55 {
		t.Fatalf("bullet holders reuse the bullet source rule: %+v", got)
	}
	if got := skillSourceCandidates(&decodedMessage{Type: "SomethingElse"}, nil, nil); len(got) != 0 {
		t.Fatalf("no CombatCommon means no source: %+v", got)
	}

	var out bytes.Buffer
	p := &processor{skillRefOut: &out, entities: newEntityResolver(), frameOrdinal: 1}
	info := &decodedMessage{Type: "UseSkillInformation", Fields: []decodedField{scalarField("SkillId", 1409003), messageField("CombatCommon", common)}}
	data := &decodedMessage{Type: "CombatSendData", Fields: []decodedField{messageField("Request", &decodedMessage{Type: "CombatRequestData", Fields: []decodedField{
		messageField("UseSkillRequest", &decodedMessage{Type: "UseSkillRequest", Fields: []decodedField{messageField("UseSkillInfo", info)}}),
	}})}}
	p.observeSkillReferences(gameFrame{}, data, "out")
	var record skillRefRecord
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
		t.Fatalf("one JSON record expected: %v (%q)", err, out.String())
	}
	if record.SkillID != 1409003 || record.Holder != "UseSkillInformation" || record.FieldPath != "Request.UseSkillRequest.UseSkillInfo.SkillId" || record.Status != resolutionUnknown {
		t.Fatalf("holder, path and unknown status without a master: %+v", record)
	}
}
