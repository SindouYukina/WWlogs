package devcapture

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"wwlogs/internal/capture"
)

func TestBulletSourceCandidatesFollowThePerMessageRule(t *testing.T) {
	common := &decodedMessage{Type: "CombatCommon", Fields: []decodedField{scalarField("EntityId", 11)}}
	create := &decodedMessage{Type: "CreateBulletPush", Fields: []decodedField{scalarField("BulletId", 1), scalarField("OwnerEntityId", 22)}}
	if got := bulletSourceCandidates(create, nil, common); len(got) != 2 || got[0].id != 22 || got[1].id != 11 || got[0].basis != "CreateBulletPush.OwnerEntityId" {
		t.Fatalf("CreateBulletPush must prefer OwnerEntityId then the sender: %+v", got)
	}
	dodge := &decodedMessage{Type: "DodgeInfoPush", Fields: []decodedField{scalarField("BulletOwnerId", 33)}}
	if got := bulletSourceCandidates(dodge, nil, common); len(got) != 1 || got[0].id != 33 {
		t.Fatalf("DodgeInfoPush must use BulletOwnerId only: %+v", got)
	}
	parent := &decodedMessage{Type: "DamageExecuteRequest", Fields: []decodedField{scalarField("AttackerEntityId", 44)}}
	context := &decodedMessage{Type: "DamageContext"}
	if got := bulletSourceCandidates(context, parent, common); len(got) != 1 || got[0].id != 44 {
		t.Fatalf("DamageContext must use the parent AttackerEntityId, not the reporter: %+v", got)
	}
	notify := &decodedMessage{Type: "CreateBulletNotify", Fields: []decodedField{scalarField("OwnerEntityId", 66)}}
	if got := bulletSourceCandidates(notify, nil, common); len(got) != 2 || got[0].id != 66 || got[0].basis != "CreateBulletNotify.OwnerEntityId" {
		t.Fatalf("CreateBulletNotify follows the same owner-then-sender rule: %+v", got)
	}
	record := &decodedMessage{Type: "DamageRecordNotify", Fields: []decodedField{scalarField("Attacker", 77)}}
	if got := bulletSourceCandidates(record, nil, common); len(got) != 1 || got[0].id != 77 {
		t.Fatalf("DamageRecordNotify must use Attacker: %+v", got)
	}
	interrupt := &decodedMessage{Type: "InterruptSkillInfo", Fields: []decodedField{scalarField("EntityId", 88)}}
	if got := bulletSourceCandidates(interrupt, nil, common); len(got) != 1 || got[0].id != 88 {
		t.Fatalf("InterruptSkillInfo names its own EntityId: %+v", got)
	}
	notifyParent := &decodedMessage{Type: "DamageExecuteNotify", Fields: []decodedField{scalarField("AttackerEntityId", 99)}}
	if got := bulletSourceCandidates(&decodedMessage{Type: "DamageContext"}, notifyParent, common); len(got) != 1 || got[0].id != 99 || got[0].basis != "DamageExecuteNotify.AttackerEntityId" {
		t.Fatalf("a DamageContext under a notify uses the notify's attacker: %+v", got)
	}
	hit := &decodedMessage{Type: "HitInformation", Fields: []decodedField{scalarField("Id", 55)}}
	if got := bulletSourceCandidates(hit, nil, common); len(got) != 1 || got[0].id != 55 {
		t.Fatalf("HitInformation must use its own Id: %+v", got)
	}
	other := &decodedMessage{Type: "UnlistedHolder"}
	if got := bulletSourceCandidates(other, nil, common); len(got) != 1 || got[0].id != 11 || !strings.Contains(got[0].basis, "default") {
		t.Fatalf("an unlisted holder falls back to CombatCommon and says so: %+v", got)
	}
	if got := bulletSourceCandidates(context, nil, common); len(got) != 0 {
		t.Fatalf("a missing parent must not fall back to another entity: %+v", got)
	}
}

func TestBulletReferenceKeepsThePrimaryRawSourceWhenResolverCannotResolveIt(t *testing.T) {
	var out bytes.Buffer
	masters := &capture.GameMaster{
		CommonBullets:  map[uint64]string{},
		PreloadBullets: map[uint64]struct{}{111: {}},
		RoleSkinMesh:   map[uint64]uint64{9001: 10},
		ModelActor:     map[uint64]string{10: "actorA"},
		ActorBullets:   map[string]map[uint64]struct{}{"actorA": {111: {}}},
		AssetPath:      map[string]string{"actorA": "/Game/A.A_C"},
	}
	entities := capture.NewEntityResolver()
	entities.Entities[11] = capture.RuntimeEntity{EntityID: 11, RoleSkinID: 9001, HasRoleSkinID: true}
	processor := observed(&processor{Processor: &capture.Processor{Masters: masters, Entities: entities, SessionNumber: 1}, dev: processorDiagnostics{
		bulletRefOut: &out,
		frameOrdinal: 2,
	}})
	message := &decodedMessage{Type: "CreateBulletPush", Fields: []decodedField{
		scalarField("BulletId", 111),
		scalarField("OwnerEntityId", 22),
	}}
	common := &decodedMessage{Type: "CombatCommon", Fields: []decodedField{scalarField("EntityId", 11)}}
	processor.writeBulletReference(capture.GameFrame{}, message, nil, "Data.Push.CreateBulletPush.BulletId", 111, common, "out")
	var record bulletRefRecord
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
		t.Fatalf("one JSON record expected: %v (%q)", err, out.String())
	}
	if record.SourceEntityID == nil || *record.SourceEntityID != 22 || record.SourceBasis != "CreateBulletPush.OwnerEntityId" {
		t.Fatalf("the primary raw owner must be preserved even when the runtime resolver does not know it: %+v", record)
	}
	if record.Status != capture.ResolutionUnknown {
		t.Fatalf("a later sender candidate must not replace the unresolved primary owner: %+v", record)
	}
}

func TestWalkBulletReferencesFindsNestedBulletIdsWithoutAMaster(t *testing.T) {
	var out bytes.Buffer
	p := observed(&processor{Processor: &capture.Processor{Entities: capture.NewEntityResolver()}, dev: processorDiagnostics{
		bulletRefOut: &out,
		frameOrdinal: 1,
	}})
	request := &decodedMessage{Type: "DamageExecuteRequest", Fields: []decodedField{
		scalarField("AttackerEntityId", 44),
		messageField("DamageContext", &decodedMessage{Type: "DamageContext", Fields: []decodedField{scalarField("BulletId", 210002005), scalarField("SkillId", 230006)}}),
	}}
	data := &decodedMessage{Type: "CombatSendData", Fields: []decodedField{messageField("Request", &decodedMessage{Type: "CombatRequestData", Fields: []decodedField{messageField("DamageExecuteRequest", request)}})}}
	p.observeBulletReferences(capture.GameFrame{}, data, "out")
	var record bulletRefRecord
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &record); err != nil {
		t.Fatalf("one JSON record expected: %v (%q)", err, out.String())
	}
	if record.BulletID != 210002005 || record.FieldPath != "Request.DamageExecuteRequest.DamageContext.BulletId" || record.SkillID == nil || *record.SkillID != 230006 {
		t.Fatalf("nested BulletId and its sibling SkillId must be captured: %+v", record)
	}
	if record.Status != capture.ResolutionUnknown || record.Evidence != "no master database loaded" {
		t.Fatalf("without a master the reference stays unknown with a stated reason: %+v", record)
	}
}

func TestInterruptSkillBulletIdIsResolvedAsABulletHandle(t *testing.T) {
	var out bytes.Buffer
	p := observed(&processor{Processor: &capture.Processor{Entities: capture.NewEntityResolver(), SessionNumber: 1}, dev: processorDiagnostics{
		bulletRefOut: &out,
		frameOrdinal: 1,
	}})
	handle := func(id int64) decodedField {
		return messageField("Handle", &decodedMessage{Type: "ActiveBulletHandle", Fields: []decodedField{scalarField("PlayerId", 7), scalarField("HandleId", id)}})
	}
	create := &decodedMessage{Type: "CreateBulletPush", Fields: []decodedField{scalarField("BulletId", 1409003011), scalarField("SkillId", 1409003), handle(11272193)}}
	interrupt := func(handleValue, skill int64) *decodedMessage {
		return &decodedMessage{Type: "InterruptSkillInfo", Fields: []decodedField{scalarField("EntityId", 5), scalarField("SkillId", skill), scalarField("BulletId", handleValue)}}
	}
	data := &decodedMessage{Type: "CombatSendData", Fields: []decodedField{messageField("Push", &decodedMessage{Type: "CombatPushData", Fields: []decodedField{
		messageField("CreateBulletPush", create),
		messageField("EndSkillPush", &decodedMessage{Type: "EndSkillPush", Fields: []decodedField{
			messageField("InterruptSkillInfo", interrupt(11272193, 1409003)),
			messageField("InterruptSkillInfo", interrupt(11272193, 99999)),
			messageField("InterruptSkillInfo", interrupt(555, 1409003)),
		}}),
	}})}}
	p.observeBulletReferences(capture.GameFrame{}, data, "out")
	var interrupts []bulletRefRecord
	for _, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n")) {
		var record bulletRefRecord
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(record.FieldPath, "InterruptSkillInfo.BulletId") {
			interrupts = append(interrupts, record)
		}
	}
	if len(interrupts) != 3 {
		t.Fatalf("three interrupt records expected: %d", len(interrupts))
	}
	if r := interrupts[0]; r.HandleID == nil || r.ResolvedBulletID == nil || *r.ResolvedBulletID != 1409003011 || r.Namespace != "ActiveBulletHandle.HandleId" {
		t.Fatalf("a matching handle and SkillId must resolve to the created bullet: %+v", r)
	}
	if r := interrupts[1]; r.ResolvedBulletID != nil || r.Status != capture.ResolutionUnknown || !strings.Contains(r.Evidence, "SkillId differs") {
		t.Fatalf("a SkillId mismatch must reject the handle match: %+v", r)
	}
	if r := interrupts[2]; r.ResolvedBulletID != nil || r.Status != capture.ResolutionUnknown || !strings.Contains(r.Evidence, "not the HandleId") {
		t.Fatalf("an unknown handle must stay unresolved and say it is not a row key: %+v", r)
	}
	p2 := observed(&processor{Processor: &capture.Processor{Entities: capture.NewEntityResolver(), SessionNumber: 2}, dev: processorDiagnostics{
		bulletRefOut: &out,
		frameOrdinal: 1,
	}})
	if _, ok := p2.dev.bulletHandles[bulletHandleKey{2, 11272193}]; ok {
		t.Fatal("handles must be scoped to the session")
	}
}
