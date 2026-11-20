package devcapture

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"

	"wwlogs/internal/capture"
)

// Test declarations of tests/capture that the Development tests use. A test file of package capture cannot be
// imported, so these are copies: change them together with their originals (named above each group).

// ---- copies from tests/capture/buff_overlap_test.go
func overlapRow(kind string, status capture.ResolutionStatus, target, handle uint64, at time.Time) capture.BuffLifecycleAuditRow {
	row := capture.BuffLifecycleAuditRow{EventKind: kind, TargetStatus: status, SessionNumber: 1, Timestamp: at}
	row.TargetEntityID = &target
	row.HandleID = &handle
	return row
}

// ---- copies from tests/capture/buff_test.go
func appendFixed32TestField(dst []byte, number uint64, value uint32) []byte {
	dst = appendTestVarint(dst, number<<3|5)
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	return append(dst, raw[:]...)
}

func mathFloat32bits(value float32) uint32 {
	return math.Float32bits(value)
}

// ---- copies from tests/capture/client_effect_push_test.go
type testCombatCommon struct {
	entityID, messageID, preMessageID uint64
}

func clientCommonBody(common testCombatCommon) []byte {
	body := []byte(nil)
	if common.preMessageID != 0 {
		body = appendVarintField(body, capture.FieldCombatCommonPreMessageID, common.preMessageID)
	}
	if common.messageID != 0 {
		body = appendVarintField(body, capture.FieldCombatCommonMessageID, common.messageID)
	}
	if common.entityID != 0 {
		body = appendVarintField(body, capture.FieldCombatCommonEntityID, common.entityID)
	}
	return body
}

// clientPushBody wraps one CombatPushData entry in CombatSendPackRequest.Data[].Push.
func clientPushBody(common testCombatCommon, pushField uint64, payload []byte) []byte {
	push := appendBytesField(nil, capture.FieldCombatPushDataCombatCommon, clientCommonBody(common))
	push = appendBytesField(push, pushField, payload)
	return appendBytesField(nil, capture.FieldCombatSendPackRequestData, appendBytesField(nil, capture.FieldCombatSendDataPush, push))
}

func clientApplyPayload(handle, effectID, instigator, stack uint64, duration float32) []byte {
	payload := appendBytesField(nil, capture.FieldApplyGameplayEffectPushReason, []byte("test reason"))
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectPushHandle, handle)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectPushStackCount, stack)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectPushID, effectID)
	payload = effectFloatField(payload, capture.FieldApplyGameplayEffectPushDuration, duration)
	return appendVarintField(payload, capture.FieldApplyGameplayEffectPushInstigatorID, instigator)
}

func clientStackPayload(handle, instigator, stack uint64, duration float32, notRefreshDuration bool) []byte {
	payload := appendVarintField(nil, capture.FieldBuffStackCountPushHandleID, handle)
	if notRefreshDuration {
		payload = appendVarintField(payload, capture.FieldBuffStackCountPushNotRefreshDuration, 1)
	}
	payload = appendVarintField(payload, capture.FieldBuffStackCountPushInstigatorID, instigator)
	payload = effectFloatField(payload, capture.FieldBuffStackCountPushDuration, duration)
	return appendVarintField(payload, capture.FieldBuffStackCountPushNewStackCount, stack)
}

func clientRemovePayload(handle, entity uint64, premature bool) []byte {
	payload := appendVarintField(nil, capture.FieldRemoveGameplayEffectPushHandle, handle)
	payload = appendVarintField(payload, capture.FieldRemoveGameplayEffectPushEntityID, entity)
	if premature {
		payload = appendVarintField(payload, capture.FieldRemoveGameplayEffectPushIsPrematureRemoval, 1)
	}
	return payload
}

func deliverClientPush(t *testing.T, p *processor, seconds float64, body []byte) {
	t.Helper()
	frame := capture.GameFrame{CommandID: capture.CmdCombatSendPackRequest, Direction: "out", Timestamp: effectAt(seconds)}
	if err := p.HandleClientEffectPushes(frame, body); err != nil {
		t.Fatalf("handle client effect push: %v", err)
	}
}

// newClientEffectTestProcessor has local team roles 100, 101 and 102 of player 7 (FightRoleInfos),
// with 100 reported as the current entity, monsters 200 and 201, and no diagnostic outputs.
func newClientEffectTestProcessor() *processor {
	p := newProcessor(nil, io.Discard, io.Discard, nil)
	p.SessionNumber = 1
	addTestPlayerRoleGroupEntity(p, 100, 7, 1312)
	addTestPlayerRoleGroupEntity(p, 101, 7, 1311)
	addTestPlayerRoleGroupEntity(p, 102, 7, 1505)
	monster := func(id uint64) func(*capture.RuntimeEntity) {
		return func(entity *capture.RuntimeEntity) {
			entity.MonsterID, entity.HasMonsterID = id, true
			entity.EntityTypeCode, entity.HasEntityType = capture.EntityTypeMonster, true
		}
	}
	addTestRuntimeEntity(p, 200, monster(9001))
	addTestRuntimeEntity(p, 201, monster(9002))
	p.ObserveLocalRoleEntity(100)
	return p
}

// ---- copies from tests/capture/client_push_test.go
// localRoleReportBody is a CombatSendPackRequest with one ClientCurrentRoleReportPush naming roleID and the given
// CurrentEntityId values (two different values make a conflicting report).
func localRoleReportBody(roleID uint64, entityIDs ...uint64) []byte {
	role := appendVarintField(nil, capture.FieldClientCurrentRoleReportPushCurrentRoleID, roleID) // ClientCurrentRoleReportPush.CurrentRoleId
	for _, entityID := range entityIDs {
		role = appendVarintField(role, capture.FieldClientCurrentRoleReportPushCurrentEntityID, entityID)
	}
	push := appendBytesField(nil, capture.FieldCombatPushDataClientCurrentRoleReportPush, role)
	data := appendBytesField(nil, capture.FieldCombatSendDataPush, push)
	return appendBytesField(nil, capture.FieldCombatSendPackRequestData, data)
}

// ---- copies from tests/capture/client_version_test.go
// currentTestClientVersions are the CheckClientPush versions the 2026-11-12 Global 3.8 capture reported (app, launcher,
// resource); currentTestBuild is its LoginRequest build.
var currentTestClientVersions = []string{"3.8.0", "3.8.8", "3.8.9"}

// ---- copies from tests/capture/combat_response_container_test.go
// receivePackBody is a CombatReceivePackNotify body with the given CombatReceiveData items.
func receivePackBody(items ...[]byte) []byte {
	var body []byte
	for _, item := range items {
		body = appendBytesField(body, capture.FieldCombatReceivePackNotifyData, item)
	}
	return body
}

// ---- copies from tests/capture/combat_state_test.go
func fsmFixture() *capture.GameMaster {
	fsm := &capture.FSMMaster{
		MachinesByNode: map[int64][]string{1: {"A", "B"}, 2: {"A", "B"}, 3: {"A", "B"}, 9: {"C"}},
		Names: map[capture.FSMNodeKey]string{
			{Machine: "A", UUID: 1}: "root", {Machine: "A", UUID: 2}: "idle", {Machine: "A", UUID: 3}: "fight",
			{Machine: "B", UUID: 1}: "root", {Machine: "B", UUID: 2}: "idle", {Machine: "B", UUID: 3}: "attack",
			{Machine: "C", UUID: 9}: "lonely",
		},
		Children:   map[capture.FSMNodeKey][]int64{{Machine: "A", UUID: 1}: {2, 3}, {Machine: "B", UUID: 1}: {2, 3}},
		References: map[capture.FSMNodeKey]int64{},
	}
	return &capture.GameMaster{FSM: fsm}
}

// ---- copies from tests/capture/damage_notify_test.go
const (
	notifyTestScene    = 900 // SceneInformation.SceneEntityId of the test scene
	notifyTestDamage   = 70010001
	notifyTestHeal     = 70020001
	notifyTestHPLoss   = 70030001
	notifyTestAero     = 1001     // negative-status DamageId (AbnormalDamageConfig Abnormal1001)
	notifyTestAeroBuff = 10041000 // a status Buff whose extra effect type is 1001
)

type testNotify struct {
	attacker, target uint64
	damageID         uint64
	damage           int32
	changeLife       int32
	immune           int32
}

// notifyData is one CombatReceiveData item: CombatNotifyData{CombatCommon{EntityId: target}, DamageExecuteNotify}.
func notifyData(n testNotify) []byte {
	common := appendVarintField(nil, capture.FieldCombatCommonEntityID, n.target)
	efn := appendVarintField(nil, capture.FieldDamageExecuteNotifyAttackerEntityID, n.attacker)
	efn = appendVarintField(efn, capture.FieldDamageExecuteNotifyTargetEntityID, n.target)
	efn = appendVarintField(efn, capture.FieldDamageExecuteNotifyDamageID, n.damageID)
	efn = appendVarintField(efn, capture.FieldDamageExecuteNotifyDamage, uint64(uint32(n.damage)))
	if n.changeLife != 0 {
		efn = appendVarintField(efn, capture.FieldDamageExecuteNotifyChangeLife, uint64(uint32(n.changeLife)))
	}
	if n.immune != 0 {
		efn = appendVarintField(efn, capture.FieldDamageExecuteNotifyImmuneType, uint64(n.immune))
	}
	notify := appendBytesField(nil, capture.FieldCombatNotifyDataCombatCommon, common)
	notify = appendBytesField(notify, capture.FieldCombatNotifyDataDamageExecuteNotify, efn)
	return appendBytesField(nil, capture.FieldCombatReceiveDataCombatNotifyData, notify)
}

// deliverNotifyFrame hands one standalone CombatReceivePackNotify frame with the given server sequence.
func deliverNotifyFrame(t *testing.T, p *processor, seconds float64, sequence uint32, items ...[]byte) {
	t.Helper()
	pairingFrame(p, seconds)
	frame := capture.GameFrame{Direction: "in", CommandID: capture.CmdCombatReceivePackNotify, Sequence: sequence, Timestamp: pairingAt(seconds)}
	if err := p.HandleCombatReceivePacks(frame, receivePackBody(items...)); err != nil {
		t.Fatal(err)
	}
}

// newNotifyTestProcessor is the pairing processor (local role 100 of player 77, monster 200) with a second local role
// 101, a second monster 201, the scene's combat entity, a single-player scene and the Master rows the tests use.
func newNotifyTestProcessor() *processor {
	p := newPairingTestProcessor()
	addTestPlayerRoleGroupEntity(p, 101, 77, 1505)
	p.ObserveLocalRoleEntity(101)
	addTestRuntimeEntity(p, 201, func(entity *capture.RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = capture.EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9002, true
	})
	p.Entities.SceneCombatEntityID, p.Entities.HasSceneCombatEntityID = notifyTestScene, true
	p.ScenePlayerIDs = []uint64{77}
	hash := strings.Repeat("a", 64)
	p.Masters.Damage[notifyTestHeal] = capture.DamageMasterRecord{ID: notifyTestHeal, CalculateType: 1, SourceHash: hash, SourceTable: "damage"}
	p.Masters.Damage[notifyTestHPLoss] = capture.DamageMasterRecord{ID: notifyTestHPLoss, CalculateType: 2, SourceHash: hash, SourceTable: "damage"}
	p.Masters.Damage[notifyTestAero] = capture.DamageMasterRecord{ID: notifyTestAero, CalculateType: 0, SourceHash: hash, SourceTable: "damage"}
	p.Masters.AbnormalDamageIDs = map[uint64]struct{}{notifyTestAero: {}}
	p.Masters.NegativeStatusExecutors = map[uint64]uint64{notifyTestAeroBuff: notifyTestAero}
	return p
}

const (
	notifyTestSpectro     = 1005     // negative-status DamageId (AbnormalDamageConfig Abnormal1005)
	notifyTestSpectroBuff = 10051000 // a status Buff whose extra effect type is 1005
	notifyTestAeroBuff2   = 10040000 // a second Aero Erosion status Buff
	notifyTestInvokeAero  = 1409303001
	notifyTestInvokeBoth  = 900266000023
)

// newCreditTestProcessor adds the Master rows of the InvokePeriod Buffs and a second negative status.
func newCreditTestProcessor() *processor {
	p := newNotifyTestProcessor()
	hash := strings.Repeat("a", 64)
	p.Masters.Damage[notifyTestSpectro] = capture.DamageMasterRecord{ID: notifyTestSpectro, CalculateType: 0, SourceHash: hash, SourceTable: "damage"}
	p.Masters.AbnormalDamageIDs[notifyTestSpectro] = struct{}{}
	p.Masters.NegativeStatusExecutors[notifyTestSpectroBuff] = notifyTestSpectro
	p.Masters.NegativeStatusExecutors[notifyTestAeroBuff2] = notifyTestAero
	p.Masters.InvokePeriodTargets = map[uint64][]uint64{
		notifyTestInvokeAero: {notifyTestAeroBuff},
		notifyTestInvokeBoth: {notifyTestAeroBuff, notifyTestSpectroBuff},
	}
	return p
}

// triggerData is one CombatReceiveData item: the server's instant ApplyGameplayEffectNotify (handle -2) of a Buff on target.
func triggerData(target, buff, instigator uint64) []byte {
	payload := appendVarintField(nil, capture.FieldApplyGameplayEffectNotifyHandle, uint64(0xFFFFFFFFFFFFFFFE)) // int32 -2
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyID, buff)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyEntityID, target)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyInstigatorID, instigator)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyStackCount, 1)
	notify := appendBytesField(nil, capture.FieldCombatNotifyDataCombatCommon, appendVarintField(nil, capture.FieldCombatCommonEntityID, target))
	notify = appendBytesField(notify, capture.FieldCombatNotifyDataApplyGameplayEffectNotify, payload)
	return appendBytesField(nil, capture.FieldCombatReceiveDataCombatNotifyData, notify)
}

// ---- copies from tests/capture/damage_push_event_test.go
func contains(text, part string) bool {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

// ---- copies from tests/capture/effect_cell_semantics_test.go
// cellInstance is one team member's timed HUD status. Its expiry is seconds after applied at remaining; every other
// field is the same across instances unless mutate changes it.
func cellInstance(handle, effectID, holder uint64, applied, remaining float64, mutate func(*capture.EffectApplyEvent)) capture.EffectApplyEvent {
	return displayApply(handle, effectID, applied, func(e *capture.EffectApplyEvent) {
		e.HolderEntityID, e.HolderRelation = holder, capture.EffectRelationLocalRole
		e.DurationSeconds, e.RemainingSeconds = effectFloat(20), effectFloat(remaining)
		e.DefinitionLinked, e.Static = true, hudStatic()
		if mutate != nil {
			mutate(e)
		}
	})
}

// ---- copies from tests/capture/effect_combat_class_test.go
// classifiedTestMaster defines a combat attribute effect (12), a non-combat tag effect (13) and an
// effect with no classifying evidence (14).
func classifiedTestMaster() *capture.GameMaster {
	return &capture.GameMaster{
		BuffDefinitions: map[uint64]struct{}{12: {}, 13: {}, 14: {}},
		BuffStatics: map[uint64]capture.BuffStaticParameters{
			12: {Attribute: 9, HasAttribute: true, HasModifier: true},
			13: {Tags: []string{"功能.逻辑状态标识.跌落无伤"}},
		},
	}
}

// ---- copies from tests/capture/effect_display_test.go
// newEffectViewProcessor returns a processor whose view reads its own tracker.
func newEffectViewProcessor() *processor {
	return newProcessor(nil, io.Discard, io.Discard, nil)
}

// hudStatic is the definition of a status the game draws in a buff bar with a DEBUFF arrow.
func hudStatic() *capture.EffectStaticInfo {
	return &capture.EffectStaticInfo{DurationPolicy: finitePolicy(), HUDCueTypes: []int32{2}, HUDPolarity: capture.EffectPolarityDebuff}
}

// displayApply is a verified HUD status on enemy holder 10 from local role 20, on handle and effect id.
func displayApply(handle, effectID uint64, seconds float64, mutate func(*capture.EffectApplyEvent)) capture.EffectApplyEvent {
	return testEffectApply(handle, seconds, func(event *capture.EffectApplyEvent) {
		event.EffectID = effectID
		event.CombatClass, event.CombatBasis = capture.EffectCombatClassCombat, capture.EffectCombatBasisAttribute+"Atk"
		event.DefinitionLinked, event.Static = true, hudStatic()
		if mutate != nil {
			mutate(event)
		}
	})
}

// ---- copies from tests/capture/effect_feed_test.go
func effectPackBody(commonEntityID uint64, innerField uint64, payload []byte) []byte {
	notify := appendBytesField(nil, capture.FieldCombatNotifyDataCombatCommon, appendVarintField(nil, capture.FieldCombatCommonEntityID, commonEntityID)) // CombatCommon.EntityId
	notify = appendBytesField(notify, innerField, payload)
	return appendBytesField(nil, capture.FieldCombatReceivePackNotifyData, appendBytesField(nil, capture.FieldCombatReceiveDataCombatNotifyData, notify))
}

func effectFloatField(dst []byte, number uint64, value float32) []byte {
	return appendFixed32TestField(dst, number, mathFloat32bits(value))
}

func effectApplyPayload(handle, effectID, target, instigator uint64, stack uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, capture.FieldApplyGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyID, effectID)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyEntityID, target)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyInstigatorID, instigator)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyStackCount, stack)
	payload = effectFloatField(payload, capture.FieldApplyGameplayEffectNotifyDuration, duration)
	return effectFloatField(payload, capture.FieldApplyGameplayEffectNotifyLeftDuration, remaining)
}

func effectStackPayload(handle, stack uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, capture.FieldBuffStackCountNotifyHandleID, handle)
	payload = appendVarintField(payload, capture.FieldBuffStackCountNotifyNewStackCount, stack)
	payload = effectFloatField(payload, capture.FieldBuffStackCountNotifyDuration, duration)
	return effectFloatField(payload, capture.FieldBuffStackCountNotifyLeftDuration, remaining)
}

func effectDurationPayload(handle uint64, duration, remaining float32) []byte {
	payload := appendVarintField(nil, capture.FieldBuffDurationNotifyHandleID, handle)
	payload = effectFloatField(payload, capture.FieldBuffDurationNotifyDuration, duration)
	return effectFloatField(payload, capture.FieldBuffDurationNotifyLeftDuration, remaining)
}

func effectRemovePayload(handle, target, instigator uint64) []byte {
	payload := appendVarintField(nil, capture.FieldRemoveGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, capture.FieldRemoveGameplayEffectNotifyEntityID, target)
	return appendVarintField(payload, capture.FieldRemoveGameplayEffectNotifyInstigatorID, instigator)
}

func deliverEffectMessage(t *testing.T, p *processor, seconds float64, body []byte) {
	t.Helper()
	frame := capture.GameFrame{CommandID: capture.CmdCombatReceivePackNotify, Direction: "in", Timestamp: effectAt(seconds)}
	if err := p.HandleCombatReceivePackNotify(frame, body); err != nil {
		t.Fatalf("handle CombatReceivePackNotify: %v", err)
	}
}

func effectQueryFor(p *processor, seconds float64) capture.CaptureQueryTime {
	return capture.CaptureQueryTime{CaptureSessionID: p.Effects.CaptureSessionID, Timestamp: effectAt(seconds)}
}

func effectSnapshotResponse(handle, buffID, instigator, stack uint64, duration, remaining float32) []byte {
	record := appendVarintField(nil, capture.FieldFightBuffInformationHandleID, handle)
	record = appendVarintField(record, capture.FieldFightBuffInformationBuffID, buffID)
	record = appendVarintField(record, capture.FieldFightBuffInformationStackCount, stack)
	record = appendVarintField(record, capture.FieldFightBuffInformationInstigatorID, instigator)
	record = effectFloatField(record, capture.FieldFightBuffInformationDuration, duration)
	record = effectFloatField(record, capture.FieldFightBuffInformationLeftDuration, remaining)
	components := appendBytesField(nil, capture.FieldEntityComponentPbFightBuffComponent, appendBytesField(nil, capture.FieldFightBuffComponentPbFightBuffInfos, record))
	return appendBytesField(nil, capture.FieldEntityActiveResponseComponentPbs, components)
}

func deliverEffectSnapshot(t *testing.T, p *processor, token uint16, holder uint64, seconds float64, response []byte) {
	t.Helper()
	if err := p.HandleActiveRequest(token, appendVarintField(nil, capture.FieldEntityActiveRequestEntityID, holder)); err != nil {
		t.Fatal(err)
	}
	frame := capture.GameFrame{CommandID: capture.CmdEntityActiveResponse, Direction: "in", Token: token, Timestamp: effectAt(seconds)}
	if err := p.HandleActiveResponse(frame, response); err != nil {
		t.Fatal(err)
	}
}

// ---- copies from tests/capture/effect_inventory_dev_test.go
// byteOrderMark may start a PCAP list written by Windows PowerShell.
const byteOrderMark = string(rune(0xFEFF))

func readReplayPCAPList(t *testing.T, listPath string) []equivalencePCAP {
	t.Helper()
	file, err := os.Open(listPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var pcaps []equivalencePCAP
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), byteOrderMark))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, found := strings.Cut(line, "|")
		if !found {
			t.Fatalf("malformed PCAP list line %q", line)
		}
		pcaps = append(pcaps, equivalencePCAP{name: name, path: path})
	}
	if len(pcaps) == 0 {
		t.Fatal("PCAP list is empty")
	}
	return pcaps
}

// ---- copies from tests/capture/effect_live_accuracy_test.go
func infinitePolicy() *int64 {
	policy := int64(1)
	return &policy
}

func finitePolicy() *int64 {
	policy := int64(2)
	return &policy
}

// passiveApply is a permanent modifier without a HUD cue on local role 10.
func passiveApply(handle, effectID uint64, seconds float64, mutate func(*capture.EffectApplyEvent)) capture.EffectApplyEvent {
	return displayApply(handle, effectID, seconds, func(e *capture.EffectApplyEvent) {
		e.HolderRelation = capture.EffectRelationLocalRole
		e.DurationSeconds, e.RemainingSeconds = nil, nil
		e.DefinitionLinked, e.Static = true, &capture.EffectStaticInfo{DurationPolicy: infinitePolicy()}
		if mutate != nil {
			mutate(e)
		}
	})
}

// ---- copies from tests/capture/effect_state_test.go
var effectTestBase = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

func effectAt(seconds float64) time.Time {
	return effectTestBase.Add(time.Duration(seconds * float64(time.Second)))
}

func effectFloat(value float64) *float64 { return &value }

// testEffectApply is a finite two-second effect on holder 10, applied by local role 20.
func testEffectApply(handle uint64, seconds float64, mutate func(*capture.EffectApplyEvent)) capture.EffectApplyEvent {
	event := capture.EffectApplyEvent{
		SessionNumber: 1, HolderEntityID: 10, HandleID: handle,
		EffectID: 100, HasEffectID: true, SourceEntityID: 20, HasSource: true,
		HolderRelation: capture.EffectRelationNonPlayer, SourceRelation: capture.EffectRelationLocalRole,
		StackCount: 1, HasStackCount: true,
		DurationSeconds: effectFloat(2), RemainingSeconds: effectFloat(2),
		ObservedAt: effectAt(seconds),
	}
	if mutate != nil {
		mutate(&event)
	}
	return event
}

func requireNear(t *testing.T, label string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", label, want)
	}
	if math.Abs(*got-want) > 1e-6 {
		t.Fatalf("%s = %v, want %v", label, *got, want)
	}
}

func onlyEffect(t *testing.T, snapshot capture.EffectSnapshot) capture.EffectInstanceSnapshot {
	t.Helper()
	if len(snapshot.Effects) != 1 {
		t.Fatalf("effects = %+v, want exactly one", snapshot.Effects)
	}
	return snapshot.Effects[0]
}

// ---- copies from tests/capture/encounter_boundary_test.go
func assertEncountersDoNotOverlap(t *testing.T, snapshot capture.DamageStatisticsSnapshot) {
	t.Helper()
	encounters := snapshot.Encounters.Completed
	if snapshot.Encounters.Current != nil {
		encounters = append(append([]capture.EncounterSnapshot(nil), encounters...), *snapshot.Encounters.Current)
	}
	for i := 1; i < len(encounters); i++ {
		previous, next := encounters[i-1], encounters[i]
		if previous.ProtocolSession == next.ProtocolSession && !next.StartedAt.After(previous.EndedAt) {
			t.Fatalf("encounter %s starts at %s, not after %s ended at %s", next.EncounterID, next.StartedAt, previous.EncounterID, previous.EndedAt)
		}
	}
}

// ---- copies from tests/capture/encounter_processor_test.go
func encounterWireVarint(field, value uint64) []byte {
	out := binary.AppendUvarint(nil, field<<3)
	return binary.AppendUvarint(out, value)
}

func encounterWireBytes(field uint64, data []byte) []byte {
	out := binary.AppendUvarint(nil, field<<3|2)
	out = binary.AppendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

// battleStateReceivePack builds CombatReceivePackNotify{Data[]{CombatNotifyData{PlayerBattleStateChangeNotify}}}.
func battleStateReceivePack(inBattle bool, playerID uint64) []byte {
	var notify []byte
	if inBattle {
		notify = append(notify, encounterWireVarint(capture.FieldPlayerBattleStateChangeNotifyInBattle, 1)...)
	}
	notify = append(notify, encounterWireVarint(capture.FieldPlayerBattleStateChangeNotifyPlayerID, playerID)...)
	notifyData := encounterWireBytes(capture.FieldCombatNotifyDataPlayerBattleStateChangeNotify, notify)
	receiveData := encounterWireBytes(capture.FieldCombatReceiveDataCombatNotifyData, notifyData)
	return encounterWireBytes(capture.FieldCombatReceivePackNotifyData, receiveData)
}

// ---- copies from tests/capture/encounter_test.go
var encounterEpoch = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func encounterAt(seconds float64) time.Time {
	return encounterEpoch.Add(time.Duration(seconds * float64(time.Second)))
}

func assertEncounterTotalsConserved(t *testing.T, snapshot capture.DamageStatisticsSnapshot) {
	t.Helper()
	var sum capture.EncounterTotals
	for _, encounter := range snapshot.Encounters.Completed {
		sum.Merge(encounter.Totals)
	}
	if snapshot.Encounters.Current != nil {
		sum.Merge(snapshot.Encounters.Current.Totals)
	}
	sum.Merge(snapshot.Encounters.OutsideEncounters)
	sum.Merge(snapshot.Encounters.CompletedDroppedTotals)
	if sum.DamageDealt != snapshot.DamageDealt || sum.DamageTaken != snapshot.DamageTaken ||
		sum.OutgoingEvents != snapshot.OutgoingEvents || sum.IncomingEvents != snapshot.IncomingEvents ||
		sum.UnattributedOutgoingDamage != snapshot.UnattributedOutgoingDamage || sum.UnattributedIncomingDamage != snapshot.UnattributedIncomingDamage {
		t.Fatalf("encounter partition does not add up to the interval totals: partition=%+v interval=%+v", sum, snapshot)
	}
}

// ---- copies from tests/capture/encounter_timeline_test.go
func timelineConservationError(timeline capture.EncounterTimelineSnapshot, encounter capture.EncounterSnapshot) error {
	if timeline.EncounterID != encounter.EncounterID {
		return fmt.Errorf("timeline %s describes another encounter than %s", timeline.EncounterID, encounter.EncounterID)
	}
	var sum int64
	perContributor := make([]int64, len(timeline.Contributors))
	duration := 0.0
	for i, point := range timeline.Points {
		var parts int64
		for c, amount := range point.ContributorDamage {
			parts += amount
			perContributor[c] += amount
		}
		if parts != point.Damage {
			return fmt.Errorf("point %d: contributors add up to %d, point says %d", i, parts, point.Damage)
		}
		if point.Damage < 0 || point.DurationSeconds < 0 {
			return fmt.Errorf("point %d is negative: %+v", i, point)
		}
		if i > 0 && math.Abs(timeline.Points[i-1].StartSeconds+timeline.Points[i-1].DurationSeconds-point.StartSeconds) > 1e-6 {
			return fmt.Errorf("point %d does not start where point %d ended", i, i-1)
		}
		if point.State == capture.TimelinePointComplete && math.Abs(point.DurationSeconds-timeline.IntervalSeconds) > 1e-9 {
			return fmt.Errorf("complete point %d lasts %.3f s", i, point.DurationSeconds)
		}
		sum += point.Damage
		duration += point.DurationSeconds
	}
	if sum != encounter.Totals.DamageDealt || timeline.DamageDealt != encounter.Totals.DamageDealt {
		return fmt.Errorf("timeline damage %d (reported %d) != encounter damage dealt %d", sum, timeline.DamageDealt, encounter.Totals.DamageDealt)
	}
	if len(timeline.Points) > 0 && math.Abs(duration-encounter.EncounterDPS.Duration.Seconds()) > 1e-6 {
		return fmt.Errorf("timeline spans %.6f s, Encounter DPS uses %.6f s", duration, encounter.EncounterDPS.Duration.Seconds())
	}
	want := map[capture.TimelineContributor]int64{}
	for _, role := range encounter.Roles {
		if role.DamageDealt > 0 {
			want[capture.TimelineContributor{Kind: capture.TimelineContributorRole, PlayerID: role.PlayerID, RoleID: role.RoleID}] = role.DamageDealt
		}
	}
	if encounter.Vehicle != nil && encounter.Vehicle.DamageDealt > 0 {
		want[capture.TimelineContributor{Kind: capture.TimelineContributorVehicle, VehicleKind: encounter.Vehicle.Kind}] = encounter.Vehicle.DamageDealt
	}
	if encounter.UnassignedRole.DamageDealt > 0 {
		want[capture.TimelineContributor{Kind: capture.TimelineContributorUnassigned}] = encounter.UnassignedRole.DamageDealt
	}
	if len(want) != len(timeline.Contributors) {
		return fmt.Errorf("timeline has %d contributors, encounter %d: %+v", len(timeline.Contributors), len(want), timeline.Contributors)
	}
	for c, contributor := range timeline.Contributors {
		if want[contributor] != perContributor[c] {
			return fmt.Errorf("contributor %+v: timeline %d, encounter %d", contributor, perContributor[c], want[contributor])
		}
	}
	return nil
}

// ---- copies from tests/capture/initial_player_test.go
// initialHandshakeWithBuild exchanges a session key and, like a real client right after it, reports build in a
// LoginRequest; the report decides whether the protocol session is decoded.
func initialHandshakeWithBuild(t *testing.T, p *processor, key *rsa.PrivateKey, at time.Time, build string) {
	t.Helper()
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, &key.PublicKey, bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	body := appendBytesField(appendVarintField(nil, capture.FieldProtoKeyResponseType, 2), capture.FieldProtoKeyResponseKey, ciphertext)
	p.HandleFrame(capture.GameFrame{CommandID: capture.CmdProtoKeyResponse, Direction: "in", Timestamp: at, Body: body})
	initialEncryptedFrame(t, p, capture.CmdLoginRequest, "out", loginRequestBody(build), 0)
}

// loginRequestBody encodes LoginRequest.ClientBasicInfo with only its build string (ClientVersion).
func loginRequestBody(build string) []byte {
	return appendBytesField(nil, capture.FieldLoginRequestClientBasicInfo, appendBytesField(nil, capture.FieldClientBasicInfoClientVersion, []byte(build)))
}

// initialEncryptedFrame encrypts body under the session key and sends it as the client does, with the given command.
func initialEncryptedFrame(t *testing.T, p *processor, command uint16, direction string, body []byte, seconds float64) {
	t.Helper()
	const sequence = 1
	plain := append([]byte{}, body...)
	if len(plain) > 0 {
		plain[sequence%len(plain)] ^= p.Session[sequence&31]
	}
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(p.Session)
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, len(plain))
	for i := 0; i < len(plain); i += aes.BlockSize {
		block.Encrypt(encoded[i:i+aes.BlockSize], plain[i:i+aes.BlockSize])
	}
	p.HandleFrame(capture.GameFrame{CommandID: command, Direction: direction, Sequence: sequence, Timestamp: pairingAt(seconds), Body: encoded})
}

func initialJoinBody() []byte {
	player := appendVarintField(nil, capture.FieldScenePlayerInformationPlayerID, 77)
	role := appendVarintField(appendVarintField(nil, capture.FieldSceneRoleInformationRoleID, 1409), capture.FieldSceneRoleInformationEntityID, 100)
	player = appendBytesField(player, capture.FieldScenePlayerInformationPlayerSceneGroupData, appendBytesField(nil, capture.FieldPlayerSceneGroupDataFightRoleInfos, role))
	scene := appendBytesField(nil, capture.FieldSceneInformationPlayerInfos, player)
	return appendBytesField(nil, capture.FieldJoinSceneNotifySceneInfo, scene)
}

func initialProcessor(t *testing.T) (*processor, *rsa.PrivateKey) {
	t.Helper()
	return initialProcessorWithBuild(t, currentTestBuild)
}

func initialProcessorWithBuild(t *testing.T, build string) (*processor, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := newPairingTestProcessor()
	discardConsole(p)
	p.Diagnostic = io.Discard
	p.RSA = &capture.StaticRSA{E: key.E, N: key.N, D: key.D}
	p.IntervalPerProtocolSession = true
	initialHandshakeWithBuild(t, p, key, pairingAt(0), build)
	return p, key
}

// ---- copies from tests/capture/lifecycle_test.go
// lifecycleAddNotify builds an EntityAddNotify carrying one EntityPb with the
// given ID and ConfigId. Field numbers follow the protocol tests in this package.
func lifecycleAddNotify(entityID, configID uint64) []byte {
	entity := appendVarintField(nil, capture.FieldEntityPbID, entityID)
	entity = appendVarintField(entity, capture.FieldEntityPbConfigID, configID)
	return appendBytesField(appendVarintField(nil, capture.FieldEntityAddNotifyIsAdd, 1), capture.FieldEntityAddNotifyEntityPbs, entity)
}

// lifecycleRemoveNotify builds an EntityRemoveNotify (remove flag 4, one
// EntityRemoveInfo in field 5 carrying the entity ID in field 1).
func lifecycleRemoveNotify(entityIDs ...uint64) []byte {
	notify := appendVarintField(nil, capture.FieldEntityRemoveNotifyIsRemove, 1)
	for _, id := range entityIDs {
		notify = appendBytesField(notify, capture.FieldEntityRemoveNotifyRemoveInfos, appendVarintField(nil, capture.FieldEntityRemoveInfoEntityID, id))
	}
	return notify
}

func mustAdd(t *testing.T, resolver *capture.EntityResolver, entityID, configID uint64) {
	t.Helper()
	if _, err := resolver.ApplyEntityAddNotify(lifecycleAddNotify(entityID, configID)); err != nil {
		t.Fatalf("EntityAddNotify(%d): %v", entityID, err)
	}
}

func mustRemove(t *testing.T, resolver *capture.EntityResolver, entityIDs ...uint64) {
	t.Helper()
	if _, _, err := resolver.ApplyEntityRemoveNotify(lifecycleRemoveNotify(entityIDs...)); err != nil {
		t.Fatalf("EntityRemoveNotify(%v): %v", entityIDs, err)
	}
}

// ---- copies from tests/capture/master_schema_test.go
// completeMasterFixture adds to the fixture Master at path every table, view and column of the runtime schema
// (capture.MasterRuntimeSchema) that it lacks: empty, and declared with the first type the runtime accepts, so the strict
// loader accepts a fixture that creates only what its test needs.
func completeMasterFixture(t testing.TB, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	column := func(c capture.MasterSchemaColumn) string {
		if len(c.Types) == 0 {
			return quote(c.Name)
		}
		return quote(c.Name) + " " + c.Types[0]
	}
	for _, table := range capture.MasterRuntimeSchema() {
		var kind string
		switch err := db.QueryRow("SELECT type FROM sqlite_master WHERE name = ?", table.Name).Scan(&kind); {
		case err == sql.ErrNoRows:
			columns := make([]string, len(table.Columns))
			for i, c := range table.Columns {
				if table.View {
					columns[i] = "NULL AS " + quote(c.Name)
				} else {
					columns[i] = column(c)
				}
			}
			if len(table.Key) > 0 {
				key := make([]string, len(table.Key))
				for i, column := range table.Key {
					key[i] = quote(column)
				}
				columns = append(columns, "PRIMARY KEY ("+strings.Join(key, ", ")+")")
			}
			statement := "CREATE TABLE " + quote(table.Name) + " (" + strings.Join(columns, ", ") + ")"
			if table.View {
				statement = "CREATE VIEW " + quote(table.Name) + " AS SELECT " + strings.Join(columns, ", ") + " WHERE 0"
			}
			if _, err := db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			continue
		case err != nil:
			t.Fatal(err)
		}
		if table.View {
			continue
		}
		present := map[string]bool{}
		rows, err := db.Query("SELECT name FROM pragma_table_info(?)", table.Name)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			present[name] = true
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, c := range table.Columns {
			if !present[c.Name] {
				if _, err := db.Exec("ALTER TABLE " + quote(table.Name) + " ADD COLUMN " + column(c)); err != nil {
					t.Fatal(err)
				}
			}
		}
		// A table the fixture created itself gets the declared key as a unique index (a second one is harmless).
		if len(table.Key) > 0 {
			key := make([]string, len(table.Key))
			for i, column := range table.Key {
				key[i] = quote(column)
			}
			if _, err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS " + quote(table.Name+"_runtime_key") + " ON " + quote(table.Name) + " (" + strings.Join(key, ", ") + ")"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// ---- copies from tests/capture/master_sqlite_test.go
func buildMasterDatabaseFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "master_data.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		"CREATE TABLE roleinfo(id TEXT PRIMARY KEY,skill_id TEXT,skin_id TEXT)",
		"CREATE TABLE roleinfo_locale(id TEXT,locale TEXT,name TEXT,text_key TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE monsterinfo(id TEXT PRIMARY KEY)",
		"CREATE TABLE monsterinfo_locale(id TEXT,locale TEXT,name TEXT,text_key TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE skill(id TEXT PRIMARY KEY,skill_group_id TEXT,skill_level_group_id TEXT)",
		"CREATE TABLE skilldescription(id TEXT PRIMARY KEY,skill_level_group_id TEXT)",
		"CREATE TABLE skilldescription_locale(id TEXT,locale TEXT,name TEXT,text_key TEXT,PRIMARY KEY(id,locale))",
		"CREATE TABLE role_skill_group_ref(role_id TEXT,skill_group_id TEXT,PRIMARY KEY(role_id,skill_group_id))",
		"CREATE TABLE damage(id TEXT PRIMARY KEY,calculate_type INTEGER)",
		"CREATE TABLE map_instance(instance_id TEXT PRIMARY KEY,map_id TEXT)",
		"CREATE TABLE map_entity_monster(map_id TEXT,entity_id TEXT,monster_id TEXT,PRIMARY KEY(map_id,entity_id,monster_id))",
		"CREATE TABLE templateconfig(id TEXT PRIMARY KEY,blueprint_type TEXT,raw_name TEXT)",
		"CREATE TABLE map_entity_template_ref(map_id TEXT,entity_id TEXT,ordinal INTEGER,template_id TEXT,PRIMARY KEY(map_id,entity_id,ordinal))",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	rows := []string{
		"INSERT INTO roleinfo VALUES('1312','77','9001')",
		"INSERT INTO roleinfo_locale VALUES('1312','zh-TW','測試角色','Role.Name')",
		"INSERT INTO roleinfo_locale VALUES('1312','ko-KR','시험 캐릭터','Role.Name')",
		"INSERT INTO roleinfo_locale VALUES('1312','en-US','Test Character','Role.Name')",
		"INSERT INTO monsterinfo VALUES('700')",
		"INSERT INTO monsterinfo VALUES('701')",
		"INSERT INTO monsterinfo_locale VALUES('700','zh-TW','測試怪物','Monster.Name')",
		"INSERT INTO monsterinfo_locale VALUES('700','en-US','Test Monster','Monster.Name')",
		"INSERT INTO skill VALUES('1312','77','88')",
		"INSERT INTO skilldescription VALUES('1312','88')",
		"INSERT INTO skilldescription_locale VALUES('1312','zh-TW','測試技能','Skill.Name')",
		"INSERT INTO skilldescription_locale VALUES('1312','ko-KR','시험 기술','Skill.Name')",
		"INSERT INTO role_skill_group_ref VALUES('1312','77')",
		"INSERT INTO damage VALUES('18446744073709551614',1)",
		"INSERT INTO map_instance VALUES('50','80')",
		"INSERT INTO map_entity_monster VALUES('80','900','700')",
		"INSERT INTO map_entity_monster VALUES('80','901','700')",
		"INSERT INTO map_entity_monster VALUES('80','901','701')",
		"INSERT INTO templateconfig VALUES('18446744073709551613','Monster_Test','')",
		"INSERT INTO map_entity_template_ref VALUES('80','500',0,'18446744073709551613')",
	}
	for _, statement := range rows {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- copies from tests/capture/master_test.go
func testSceneTemplateMaster(candidates []capture.SceneTemplateCandidate) *capture.GameMaster {
	identity := &capture.SceneIdentityMaster{
		InstanceMaps:    map[uint64]uint64{50: 3926},
		LevelTemplates:  map[capture.SceneLevelKey][]capture.SceneTemplateCandidate{{MapID: 3926, EntityID: 244750005}: candidates},
		TemplateCatalog: make(map[uint64]capture.SceneTemplateCandidate, len(candidates)),
	}
	for _, candidate := range candidates {
		identity.TemplateCatalog[candidate.TemplateID] = candidate
	}
	return &capture.GameMaster{Locale: "zh-TW", SceneIdentity: identity, Monsters: map[uint64]capture.MasterRecord{}}
}

// ---- copies from tests/capture/negative_status_causality_test.go
const (
	causalEnemy3      = 202
	causalEnemyActor  = 203       // an enemy that applies statuses to other enemies
	causalInstantBuff = 150721001 // an instant status Buff executing Spectro Frazzle (no instance, no handle)
	fusionLimit       = 10
)

// newCausalityTestProcessor is the settlement test processor with a third enemy, an enemy that applies statuses, the
// stack limits of the statuses without a period and an instant Spectro Frazzle Buff.
func newCausalityTestProcessor() *processor {
	p := newSettlementTestProcessor()
	for _, id := range []uint64{causalEnemy3, causalEnemyActor} {
		id := id
		addTestRuntimeEntity(p, id, func(entity *capture.RuntimeEntity) {
			entity.EntityTypeCode, entity.HasEntityType = capture.EntityTypeMonster, true
			entity.MonsterID, entity.HasMonsterID = 9000+id, true
		})
	}
	if p.Masters.BuffStatics == nil {
		p.Masters.BuffStatics = map[uint64]capture.BuffStaticParameters{}
	}
	for _, buff := range []uint64{settleFusionBuff, settleGlacioBuff} {
		p.Masters.BuffStatics[buff] = capture.BuffStaticParameters{StackLimit: fusionLimit, HasStackLimit: true}
	}
	p.Masters.NegativeStatusExecutors[causalInstantBuff] = notifyTestSpectro
	p.Masters.NegativeStatusSettlements[causalInstantBuff] = capture.NegativeStatusSettlementRule{Known: true, ExecutesOnCreation: true, Instant: true}
	return p
}

func fusionTick(target uint64, damage int32) []byte { return statusTick(target, settleFusion, damage) }

func spectroTick(target uint64, damage int32) []byte {
	return notifyData(testNotify{attacker: notifyTestScene, target: target, damageID: notifyTestSpectro, damage: damage, changeLife: damage})
}

// buildFusionToLimit puts a Fusion Burst instance at its stack limit on holder: created by creator with nine stacks, the
// tenth applied by tenth.
func buildFusionToLimit(t *testing.T, p *processor, seconds float64, sequence uint32, holder, handle, creator, tenth uint64) {
	t.Helper()
	deliverNotifyFrame(t, p, seconds, sequence, statusApplyItem(holder, handle, settleFusionBuff, creator, 9), statusStackItem(holder, handle, fusionLimit, tenth))
}

// overflow is the server's report of an application at the stack limit (the count stays) followed by the clear.
func overflow(holder, handle, by uint64) [][]byte {
	return [][]byte{statusStackItem(holder, handle, fusionLimit, by), statusRemoveItem(holder, handle)}
}

func items(groups ...[][]byte) [][]byte {
	var out [][]byte
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

// ---- copies from tests/capture/negative_status_settlement_test.go
const (
	settleGlacio      = 1003     // negative-status DamageId without a period (applied)
	settleGlacioBuff  = 10011000 // its status Buff
	settleFusion      = 1004
	settleFusionBuff  = 10021000
	settleHavoc       = 1006 // DEF reduction, no damage per the client glossary
	settleHavocBuff   = 10061000
	settleGlacioNew   = 1007     // the FormulaType 9 Glacio Chafe variant
	settleGlacioNewBf = 10011100 // replaces 10011000 for some appliers
)

// newSettlementTestProcessor is the credit test processor with a third local role, the statuses without a period and the
// settlement of every status Buff the tests use.
func newSettlementTestProcessor() *processor {
	p := newCreditTestProcessor()
	addTestPlayerRoleGroupEntity(p, 102, 77, 1608)
	p.ObserveLocalRoleEntity(102)
	hash := strings.Repeat("a", 64)
	for _, damageID := range []uint64{settleGlacio, settleFusion, settleHavoc, settleGlacioNew} {
		p.Masters.Damage[damageID] = capture.DamageMasterRecord{ID: damageID, CalculateType: 0, SourceHash: hash, SourceTable: "damage"}
		p.Masters.AbnormalDamageIDs[damageID] = struct{}{}
	}
	p.Masters.NegativeStatusExecutors[settleGlacioBuff] = settleGlacio
	p.Masters.NegativeStatusExecutors[settleFusionBuff] = settleFusion
	p.Masters.NegativeStatusExecutors[settleHavocBuff] = settleHavoc
	p.Masters.NegativeStatusExecutors[settleGlacioNewBf] = settleGlacioNew
	p.Masters.NegativeStatusSettlements = map[uint64]capture.NegativeStatusSettlementRule{
		notifyTestAeroBuff:    {Known: true, Periodic: true, ExecutesOnCreation: true},
		notifyTestAeroBuff2:   {Known: true, Periodic: true, ExecutesOnCreation: true},
		notifyTestSpectroBuff: {Known: true, Periodic: true},
		settleGlacioBuff:      {Known: true, ExecutesOnCreation: true, ClearsOnOverflow: true},
		settleFusionBuff:      {Known: true, ExecutesOnCreation: true, ClearsOnOverflow: true},
		settleHavocBuff:       {Known: true, ExecutesOnCreation: true},
		settleGlacioNewBf:     {Known: true, ExecutesOnCreation: true},
	}
	return p
}

// statusApplyItem is the server's ApplyGameplayEffectNotify of a status instance (positive handle) on target.
func statusApplyItem(target, handle, buff, instigator, stack uint64) []byte {
	payload := appendVarintField(nil, capture.FieldApplyGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyID, buff)
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyEntityID, target)
	if instigator != 0 {
		payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyInstigatorID, instigator)
	}
	payload = appendVarintField(payload, capture.FieldApplyGameplayEffectNotifyStackCount, stack)
	notify := appendBytesField(nil, capture.FieldCombatNotifyDataCombatCommon, appendVarintField(nil, capture.FieldCombatCommonEntityID, target))
	notify = appendBytesField(notify, capture.FieldCombatNotifyDataApplyGameplayEffectNotify, payload)
	return appendBytesField(nil, capture.FieldCombatReceiveDataCombatNotifyData, notify)
}

// statusStackItem is the server's BuffStackCountNotify of one stack application by instigator.
func statusStackItem(target, handle, stack, instigator uint64) []byte {
	payload := appendVarintField(nil, capture.FieldBuffStackCountNotifyHandleID, handle)
	payload = appendVarintField(payload, capture.FieldBuffStackCountNotifyNewStackCount, stack)
	payload = appendVarintField(payload, capture.FieldBuffStackCountNotifyInstigatorID, instigator)
	notify := appendBytesField(nil, capture.FieldCombatNotifyDataCombatCommon, appendVarintField(nil, capture.FieldCombatCommonEntityID, target))
	notify = appendBytesField(notify, capture.FieldCombatNotifyDataBuffStackCountNotify, payload)
	return appendBytesField(nil, capture.FieldCombatReceiveDataCombatNotifyData, notify)
}

// statusRemoveItem is the server's RemoveGameplayEffectNotify of a status instance.
func statusRemoveItem(target, handle uint64) []byte {
	payload := appendVarintField(nil, capture.FieldRemoveGameplayEffectNotifyHandle, handle)
	payload = appendVarintField(payload, capture.FieldRemoveGameplayEffectNotifyEntityID, target)
	notify := appendBytesField(nil, capture.FieldCombatNotifyDataCombatCommon, appendVarintField(nil, capture.FieldCombatCommonEntityID, target))
	notify = appendBytesField(notify, capture.FieldCombatNotifyDataRemoveGameplayEffectNotify, payload)
	return appendBytesField(nil, capture.FieldCombatReceiveDataCombatNotifyData, notify)
}

func statusTick(target, damageID uint64, damage int32) []byte {
	return notifyData(testNotify{attacker: notifyTestScene, target: target, damageID: damageID, damage: damage, changeLife: damage})
}

// ---- copies from tests/capture/no_amount_event_test.go
func decodeJSONLLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// ---- copies from tests/capture/pipeline_perf_test.go
type rawCapturePacket struct {
	info gopacket.CaptureInfo
	data []byte
}

type rawCapture struct {
	name     string
	link     layers.LinkType
	packets  []rawCapturePacket
	duration time.Duration
}

func loadRawCaptures(t *testing.T, listPath string) []rawCapture {
	t.Helper()
	var captures []rawCapture
	for _, entry := range readSegmentDropList(t, listPath) {
		handle, err := pcap.OpenOffline(entry.path)
		if err != nil {
			t.Fatalf("open %s: %v", entry.path, err)
		}
		if err := handle.SetBPFFilter(equivalenceFilter); err != nil {
			t.Fatal(err)
		}
		capture := rawCapture{name: entry.name, link: handle.LinkType()}
		for {
			data, info, err := handle.ReadPacketData()
			if err != nil {
				break
			}
			capture.packets = append(capture.packets, rawCapturePacket{info: info, data: append([]byte(nil), data...)})
		}
		handle.Close()
		if n := len(capture.packets); n > 1 {
			capture.duration = capture.packets[n-1].info.Timestamp.Sub(capture.packets[0].info.Timestamp)
		}
		captures = append(captures, capture)
	}
	return captures
}

// readSegmentDropList reads a "name|absolute path" capture list; blank lines and # comments are skipped.
func readSegmentDropList(t *testing.T, path string) []equivalencePCAP {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var pcaps []equivalencePCAP
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, found := strings.Cut(line, "|")
		if !found {
			t.Fatalf("malformed PCAP list line %q", line)
		}
		pcaps = append(pcaps, equivalencePCAP{name: name, path: path})
	}
	if len(pcaps) == 0 {
		t.Fatal("PCAP list is empty")
	}
	return pcaps
}

// cpuSeconds is the process CPU time the Go runtime has used: available time minus idle time.
func cpuSeconds() float64 {
	samples := []metrics.Sample{{Name: "/cpu/classes/total:cpu-seconds"}, {Name: "/cpu/classes/idle:cpu-seconds"}}
	metrics.Read(samples)
	if samples[0].Value.Kind() != metrics.KindFloat64 || samples[1].Value.Kind() != metrics.KindFloat64 {
		return 0
	}
	return samples[0].Value.Float64() - samples[1].Value.Float64()
}

type phaseMeasure struct {
	Packets       int     `json:"packets"`
	WallSeconds   float64 `json:"wallSeconds"`
	CPUSeconds    float64 `json:"cpuSeconds"`
	PacketsPerSec float64 `json:"packetsPerSecond"`
	NsPerPacket   float64 `json:"nsPerPacket"`
	AllocsPerPkt  float64 `json:"allocsPerPacket"`
	BytesPerPkt   float64 `json:"bytesPerPacket"`
	GCCycles      uint32  `json:"gcCycles"`
	GCPauseMs     float64 `json:"gcPauseMs"`
	HeapInuseMB   float64 `json:"heapInuseMBAtEnd"`
}

func measurePhase(packets int, run func()) phaseMeasure {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	cpu := cpuSeconds()
	started := time.Now()
	run()
	wall := time.Since(started)
	cpuUsed := cpuSeconds() - cpu
	runtime.ReadMemStats(&after)
	m := phaseMeasure{Packets: packets, WallSeconds: wall.Seconds(), CPUSeconds: cpuUsed,
		GCCycles: after.NumGC - before.NumGC, GCPauseMs: float64(after.PauseTotalNs-before.PauseTotalNs) / 1e6,
		HeapInuseMB: float64(after.HeapInuse) / (1 << 20)}
	if packets > 0 {
		m.PacketsPerSec = float64(packets) / wall.Seconds()
		m.NsPerPacket = float64(wall.Nanoseconds()) / float64(packets)
		m.AllocsPerPkt = float64(after.Mallocs-before.Mallocs) / float64(packets)
		m.BytesPerPkt = float64(after.TotalAlloc-before.TotalAlloc) / float64(packets)
	}
	return m
}

// decodeCapturePacket mirrors the executables: decode the packet, find UDP, decide the direction.
func decodeCapturePacket(link layers.LinkType, raw rawCapturePacket) ([]byte, string, bool) {
	packet := gopacket.NewPacket(raw.data, link, gopacket.Default)
	udpLayer := packet.Layer(layers.LayerTypeUDP)
	if udpLayer == nil {
		return nil, "", false
	}
	udp := udpLayer.(*layers.UDP)
	return udp.Payload, capture.PacketDirection(packet, "192.168.1.2", udp, 13100, 13120), true
}

func replayRawCapture(p *processor, recording rawCapture, rewrite func([]byte), offset time.Duration, streams map[string]*capture.Stream, stats *capture.CaptureStats) {
	for _, raw := range recording.packets {
		payload, direction, ok := decodeCapturePacket(recording.link, raw)
		if !ok {
			continue
		}
		stats.UDPPackets++
		if rewrite != nil {
			payload = append([]byte(nil), payload...)
			rewrite(payload)
		}
		capture.ConsumeDatagram(payload, direction, raw.info.Timestamp.Add(offset), streams, p.Processor, stats)
	}
}

// ---- copies from tests/capture/player_relevance_test.go
func addTestRuntimeEntity(p *processor, entityID uint64, update func(*capture.RuntimeEntity)) {
	entity := capture.RuntimeEntity{EntityID: entityID, SourceMessageID: capture.CmdEntityAddNotify, SourceFieldPath: "EntityAddNotify.EntityPb.Id"}
	if update != nil {
		update(&entity)
	}
	p.Entities.Entities[entityID] = entity
	p.Entities.ObserveLifecycleAdd(entity)
}

func addTestPlayerRoleGroupEntity(p *processor, entityID, playerID, roleID uint64, update ...bool) {
	roleSourceFieldPath := "JoinSceneNotify.SceneInfo.PlayerInfos[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
	sourceMessageID := uint16(capture.CmdJoinSceneNotify)
	if len(update) > 0 && update[0] {
		roleSourceFieldPath = "UpdateSceneGroupDataNotify.PlayerSceneGroupDataList[].PlayerSceneGroupData[].FightRoleInfos[].RoleId"
		sourceMessageID = capture.CmdUpdateSceneGroupDataNotify
	}
	entity := capture.RuntimeEntity{
		EntityID: entityID, PlayerID: playerID, HasPlayerID: true,
		RoleID: roleID, HasRoleID: true,
		RoleSourceFieldPath: roleSourceFieldPath,
		SourceMessageID:     sourceMessageID,
	}
	p.Entities.Entities[entityID] = entity
	p.Entities.ObserveLifecycleAdd(entity)
}

func testDamageEvent(p *processor, sourceID, targetID uint64) capture.CombatEvent {
	source := capture.ResolveEntityIdentity(p.Entities, p.Masters, sourceID)
	target := capture.ResolveEntityIdentity(p.Entities, p.Masters, targetID)
	return capture.CombatEvent{
		SourceEntityID: sourceID, HasSourceEntity: sourceID != 0, Source: source,
		TargetEntityID: targetID, HasTargetEntity: targetID != 0, Target: target,
		Damage: 10, CharacterStatus: capture.ResolutionUnknown, SkillStatus: capture.ResolutionUnknown,
		EventTypeStatus: capture.ResolutionUnknown,
	}
}

// ---- copies from tests/capture/protocol_session_test.go
// currentTestBuild is a LoginRequest build of the current game version, as the 2026-11-12 Global 3.8 capture reported it.
const currentTestBuild = "OS_Windows_Product_3.8.0_9244589_3.8.9_9436312"

// sendCurrentProtocolFight sends a two-hit fight of the current protocol after the handshake of p.
func sendCurrentProtocolFight(t *testing.T, p *processor) {
	t.Helper()
	initialEncryptedFrame(t, p, capture.CmdBasicInfoNotify, "in", appendVarintField(nil, capture.FieldBasicInfoNotifyID, 77), 0.1)
	initialEncryptedFrame(t, p, capture.CmdJoinSceneNotify, "in", initialJoinBody(), 0.2)
	addTestRuntimeEntity(p, 200, func(e *capture.RuntimeEntity) { e.EntityTypeCode, e.HasEntityType = capture.EntityTypeMonster, true })
	initialEncryptedFrame(t, p, capture.CmdCombatReceivePackNotify, "in", battleStateReceivePack(true, 77), 1)
	initialEncryptedFrame(t, p, capture.CmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 1.1)
	initialEncryptedFrame(t, p, capture.CmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2)
	initialEncryptedFrame(t, p, capture.CmdCombatSendPackRequest, "out", damageRequestBody(2, 100, 200), 2.1)
	initialEncryptedFrame(t, p, capture.CmdCombatSendPackResponse, "in", damageResponseBody(2, 250), 3)
}

// otherVersionBuilds are client builds of other game versions, whose frames parse under the current numbers.
var otherVersionBuilds = []string{"OS_Windows_Product_3.7.0_9052414_3.7.18_9350105", "OS_Windows_Product_3.9.0_9999999_3.9.1_9999999"}

// sendOtherVersionFight sends a one-hit fight after the handshake of p.
func sendOtherVersionFight(t *testing.T, p *processor) {
	t.Helper()
	for _, frame := range []struct {
		command   uint16
		direction string
		body      []byte
		seconds   float64
	}{
		{capture.CmdJoinSceneNotify, "in", initialJoinBody(), 0.2},
		{capture.CmdCombatSendPackRequest, "out", damageRequestBody(1, 100, 200), 1.1},
		{capture.CmdCombatSendPackResponse, "in", damageResponseBody(1, 500), 2},
	} {
		initialEncryptedFrame(t, p, frame.command, frame.direction, frame.body, frame.seconds)
	}
}

// ---- copies from tests/capture/protocol_test.go
func appendVarintField(dst []byte, number, value uint64) []byte {
	dst = appendTestVarint(dst, number<<3)
	return appendTestVarint(dst, value)
}

func appendBytesField(dst []byte, number uint64, value []byte) []byte {
	dst = appendTestVarint(dst, number<<3|2)
	dst = appendTestVarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendTestVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

// ---- copies from tests/capture/role_aggregation_test.go
// assertRoleTotalsReconcile checks the role aggregation invariants for an interval and every retained encounter.
func assertRoleTotalsReconcile(t *testing.T, snapshot capture.DamageStatisticsSnapshot) {
	t.Helper()
	check := func(scope string, roles []capture.RoleDamageSnapshot, unassigned capture.UnassignedRoleTotals, vehicle *capture.VehicleDamageSnapshot, dealt, taken int64, outgoing, incoming uint64) {
		t.Helper()
		sumDealt, sumTaken := unassigned.DamageDealt, unassigned.DamageTaken
		sumOutgoing, sumIncoming := unassigned.OutgoingEvents, unassigned.IncomingEvents
		if vehicle != nil {
			sumDealt += vehicle.DamageDealt
			sumOutgoing += vehicle.OutgoingEvents
			assertSkillsReconcile(t, scope+" vehicle", vehicle.Skills, vehicle.UnassignedSkill, vehicle.DamageDealt, vehicle.OutgoingEvents)
		}
		for _, role := range roles {
			if role.DirectDamage+role.SummonDamage+role.StatusDamage != role.DamageDealt {
				t.Fatalf("%s role %d/%d: direct %d + summon %d + status %d != dealt %d", scope, role.PlayerID, role.RoleID, role.DirectDamage, role.SummonDamage, role.StatusDamage, role.DamageDealt)
			}
			var statusDamage int64
			var statusEvents uint64
			for i, row := range role.Statuses {
				if row.DamageID == 0 || row.OutgoingEvents == 0 || (i > 0 && role.Statuses[i-1].DamageID >= row.DamageID) || row.TriggerEvents > row.OutgoingEvents || row.TriggerDamage > row.DamageDealt {
					t.Fatalf("%s role %d status row %d is invalid: %+v", scope, role.RoleID, i, row)
				}
				statusDamage += row.DamageDealt
				statusEvents += row.OutgoingEvents
			}
			if statusDamage != role.StatusDamage {
				t.Fatalf("%s role %d: status rows %d != status damage %d", scope, role.RoleID, statusDamage, role.StatusDamage)
			}
			assertSkillsReconcile(t, scope+" role "+strconv.FormatUint(role.RoleID, 10), role.Skills, role.UnassignedSkill, role.DamageDealt-statusDamage, role.OutgoingEvents-statusEvents)
			sumDealt += role.DamageDealt
			sumTaken += role.DamageTaken
			sumOutgoing += role.OutgoingEvents
			sumIncoming += role.IncomingEvents
		}
		if sumDealt != dealt || sumTaken != taken || sumOutgoing != outgoing || sumIncoming != incoming {
			t.Fatalf("%s roles do not reconcile: roles+unassigned dealt=%d taken=%d out=%d in=%d, totals dealt=%d taken=%d out=%d in=%d",
				scope, sumDealt, sumTaken, sumOutgoing, sumIncoming, dealt, taken, outgoing, incoming)
		}
	}
	check("interval", snapshot.Roles, snapshot.UnassignedRole, snapshot.Vehicle, snapshot.DamageDealt, snapshot.DamageTaken, snapshot.OutgoingEvents, snapshot.IncomingEvents)
	encounters := snapshot.Encounters.Completed
	if snapshot.Encounters.Current != nil {
		encounters = append(append([]capture.EncounterSnapshot(nil), encounters...), *snapshot.Encounters.Current)
	}
	for _, e := range encounters {
		check("encounter "+e.EncounterID, e.Roles, e.UnassignedRole, e.Vehicle, e.Totals.DamageDealt, e.Totals.DamageTaken, e.Totals.OutgoingEvents, e.Totals.IncomingEvents)
	}
}

// assertSkillsReconcile checks one contributor's skill split: rows plus the unassigned-skill bucket equal its confirmed
// damage and events, every row has a SkillId, no SkillId repeats, and rows are ordered by damage then SkillId.
func assertSkillsReconcile(t *testing.T, scope string, skills []capture.SkillDamageSnapshot, unassigned capture.UnassignedSkillTotals, dealt int64, events uint64) {
	t.Helper()
	sumDealt, sumEvents := unassigned.DamageDealt, unassigned.OutgoingEvents
	if unassigned.MissingSkillEvents+unassigned.OverflowEvents != unassigned.OutgoingEvents {
		t.Fatalf("%s unassigned skill events %+v do not split into missing and overflow", scope, unassigned)
	}
	seen := make(map[uint64]bool, len(skills))
	for i, skill := range skills {
		if skill.SkillID == 0 || seen[skill.SkillID] || skill.OutgoingEvents == 0 {
			t.Fatalf("%s skill row %d is invalid: %+v", scope, i, skill)
		}
		seen[skill.SkillID] = true
		if i > 0 && (skills[i-1].DamageDealt < skill.DamageDealt || (skills[i-1].DamageDealt == skill.DamageDealt && skills[i-1].SkillID > skill.SkillID)) {
			t.Fatalf("%s skill rows are out of order at %d: %+v", scope, i, skills)
		}
		sumDealt += skill.DamageDealt
		sumEvents += skill.OutgoingEvents
	}
	if sumDealt != dealt || sumEvents != events {
		t.Fatalf("%s skills do not reconcile: skills+unassigned dealt=%d events=%d, contributor dealt=%d events=%d", scope, sumDealt, sumEvents, dealt, events)
	}
}

func writeTestDamage(p *processor, sourceID, targetID, rpcID uint64, amount int64) {
	event := testDamageEvent(p, sourceID, targetID)
	event.EventTypeCandidate, event.Damage, event.RPCID = "DAMAGE_CANDIDATE", amount, rpcID
	p.WriteDamageEvent(event)
}

// ---- copies from tests/capture/rpc_pairing_test.go
var pairingTestBase = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func pairingAt(seconds float64) time.Time {
	return pairingTestBase.Add(time.Duration(seconds * float64(time.Second)))
}

// pairingFrame advances the processor clock the way handleFrame does for one decoded frame.
func pairingFrame(p *processor, seconds float64) {
	p.CurrentFrameTime = pairingAt(seconds)
	p.ObservePairingClock(p.CurrentFrameTime)
}

func damageRequestBody(rpcID, attackerID, targetID uint64) []byte {
	u3n := appendVarintField(nil, capture.FieldDamageExecuteRequestDamageID, 70010001)      // DamageId
	u3n = appendVarintField(u3n, capture.FieldDamageExecuteRequestTargetEntityID, targetID) // TargetEntityId
	u3n = appendVarintField(u3n, capture.FieldDamageExecuteRequestAttackerEntityID, attackerID)
	request := appendVarintField(nil, capture.FieldCombatRequestDataRequestID, rpcID)
	request = appendBytesField(request, capture.FieldCombatRequestDataDamageExecuteRequest, u3n)
	return appendBytesField(nil, capture.FieldCombatSendPackRequestData, appendBytesField(nil, capture.FieldCombatSendDataRequest, request))
}

func damageResponseBody(rpcID uint64, amount int32) []byte {
	lis := appendVarintField(nil, capture.FieldDamageExecuteResponseErrorCode, 0)
	lis = appendVarintField(lis, capture.FieldDamageExecuteResponseImmuneType, 0)
	lis = appendVarintField(lis, capture.FieldDamageExecuteResponseDamage, uint64(uint32(amount)))
	response := appendVarintField(nil, capture.FieldCombatResponseDataRequestID, rpcID)
	response = appendBytesField(response, capture.FieldCombatResponseDataDamageExecuteResponse, lis)
	item := appendBytesField(nil, capture.FieldCombatReceiveDataCombatResponseData, response)
	return appendBytesField(nil, capture.FieldCombatSendPackResponseReceivePackNotify, appendBytesField(nil, capture.FieldCombatReceivePackNotifyData, item))
}

// newPairingTestProcessor has one local role entity (100) and one monster (200).
func newPairingTestProcessor() *processor {
	p := newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.Entities = capture.NewEntityResolver()
	p.Masters = &capture.GameMaster{Damage: map[uint64]capture.DamageMasterRecord{
		70010001: {ID: 70010001, CalculateType: 0, SourceHash: strings.Repeat("a", 64), SourceTable: "damage"},
	}}
	addTestPlayerRoleGroupEntity(p, 100, 77, 1409)
	p.ObserveLocalRoleEntity(100)
	addTestRuntimeEntity(p, 200, func(entity *capture.RuntimeEntity) {
		entity.EntityTypeCode, entity.HasEntityType = capture.EntityTypeMonster, true
		entity.MonsterID, entity.HasMonsterID = 9001, true
	})
	return p
}

// ---- copies from tests/capture/transport_gap_test.go
var transportGapBase = time.Date(2026, 11, 4, 1, 0, 0, 0, time.UTC)

func newTransportGapHarness() (*processor, map[string]*capture.Stream, *capture.CaptureStats) {
	return newProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil), make(map[string]*capture.Stream), &capture.CaptureStats{}
}

func sendTransportGapFrame(p *processor, streams map[string]*capture.Stream, stats *capture.CaptureStats, sequence uint32, at time.Duration) {
	frame := makeTransportTestFrame(false, false)
	capture.ConsumeDatagram(makeKCPSegmentForTest(9, sequence, frame), "in", transportGapBase.Add(at), streams, p.Processor, stats)
}

// ---- copies from tests/capture/transport_perturbation_dev_test.go
type perturbationPacket struct {
	payload   []byte
	direction string
	at        time.Time
}

func readPerturbationPackets(t *testing.T, path string) []perturbationPacket {
	t.Helper()
	handle, err := pcap.OpenOffline(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer handle.Close()
	if err := handle.SetBPFFilter(equivalenceFilter); err != nil {
		t.Fatal(err)
	}
	source := gopacket.NewPacketSource(handle, handle.LinkType())
	var packets []perturbationPacket
	for {
		packet, err := source.NextPacket()
		if err != nil {
			break
		}
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		udp := udpLayer.(*layers.UDP)
		packets = append(packets, perturbationPacket{
			payload:   append([]byte(nil), udp.Payload...),
			direction: capture.PacketDirection(packet, "192.168.1.2", udp, 13100, 13120),
			at:        packet.Metadata().CaptureInfo.Timestamp,
		})
	}
	return packets
}

// ---- copies from tests/capture/transport_recovery_test.go
func containsRisk(risks []string, risk string) bool {
	for _, candidate := range risks {
		if candidate == risk {
			return true
		}
	}
	return false
}

// ---- copies from tests/capture/transport_test.go
func makeTransportTestFrame(compressed, includeLengthPrefixInCRC bool) []byte {
	body := []byte{0x31, 0x42, 0x53, 0x64}
	transmittedBody := body
	if compressed {
		transmittedBody = make([]byte, 4+len(body))
		binary.LittleEndian.PutUint32(transmittedBody[:4], uint32(len(body)))
		copy(transmittedBody[4:], body)
	}
	frame := make([]byte, 14+len(transmittedBody))
	binary.LittleEndian.PutUint16(frame[:2], uint16(len(frame)-3))
	frame[3] = 4
	if compressed {
		frame[3] |= 0x10
	}
	binary.LittleEndian.PutUint32(frame[4:8], 7)
	binary.LittleEndian.PutUint16(frame[8:10], 65000)
	crcBody := transmittedBody
	if compressed && !includeLengthPrefixInCRC {
		crcBody = transmittedBody[4:]
	}
	binary.LittleEndian.PutUint32(frame[10:14], crc32.ChecksumIEEE(crcBody))
	copy(frame[14:], transmittedBody)
	return frame
}

func makeKCPSegmentForTest(conv, sequence uint32, payload []byte) []byte {
	segment := make([]byte, 24+len(payload))
	binary.LittleEndian.PutUint32(segment[:4], conv)
	segment[4] = 81
	binary.LittleEndian.PutUint32(segment[12:16], sequence)
	binary.LittleEndian.PutUint32(segment[20:24], uint32(len(payload)))
	copy(segment[24:], payload)
	return segment
}

// ---- copies from tests/capture/vehicle_contributor_test.go
func vehicleDamageContributorFixture() (*capture.EntityResolver, *capture.GameMaster, capture.CombatEvent) {
	source := capture.RuntimeEntity{
		EntityID: 10, FollowerSummonConfigID: 290101, HasFollowerSummonConfigID: true,
		FollowEntityID: 11, HasFollowEntityID: true, SourceMessageID: capture.CmdEntityAddNotify,
	}
	motorcycle := capture.RuntimeEntity{
		EntityID: 11, SummonerID: 10, HasSummonerID: true,
		SummonType: capture.SummonTypeConcomitantMotorcycle, HasSummonType: true,
		SummonSkillID: 6013, HasSummonSkillID: true, SourceMessageID: capture.CmdEntityAddNotify,
	}
	resolver := &capture.EntityResolver{Entities: map[uint64]capture.RuntimeEntity{10: source, 11: motorcycle}}
	master := &capture.GameMaster{
		Visions:         map[uint64]struct{}{6013: {}},
		SummonBlueprint: map[uint64]string{290101: "branch3.0_Motorcycle_Skill"},
		BlueprintEntityTypes: map[string]map[string]struct{}{
			"branch3.0_Motorcycle_Skill": {"BattleVehicle": {}},
		},
		BlueprintModel: map[string]uint64{"branch3.0_Motorcycle_Skill": 900000},
		ModelActor:     map[uint64]string{900000: "motorcycle-actor"},
		AssetPath:      map[string]string{"motorcycle-actor": "/Game/Vehicle/BP_Motorcycle_C"},
		ActorBullets:   map[string]map[uint64]struct{}{"motorcycle-actor": {100010101: {}}},
		ActorSkills:    map[string]map[uint64]struct{}{"motorcycle-actor": {10001009: {}}},
	}
	event := capture.CombatEvent{
		SessionNumber: 1, EventTypeCandidate: "DAMAGE_CANDIDATE", SourceEntityID: 10, HasSourceEntity: true,
		Source:  capture.EntityIdentity{Runtime: source, RuntimeStatus: capture.ResolutionVerified},
		SkillID: 10001009, SkillMessageID: 50, HasBulletID: true, BulletID: 100010101,
		CharacterStatus: capture.ResolutionUnknown, CharacterNameStatus: capture.ResolutionUnknown,
	}
	return resolver, master, event
}
