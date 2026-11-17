//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestCombatEventJSONLExposesRawAndNormalizedLifecycle checks the two output
// layers together: raw holds only the observed request values, and the
// lifecycle appears under the normalized source entity.
func TestCombatEventJSONLExposesRawAndNormalizedLifecycle(t *testing.T) {
	resolver := NewEntityResolver()
	mustAdd(t, resolver, 8001, 500)
	mustRemove(t, resolver, 8001)
	mustAdd(t, resolver, 8001, 900)

	var jsonl bytes.Buffer
	p := NewProcessor(nil, &bytes.Buffer{}, &bytes.Buffer{}, nil)
	p.Dev.combatEventsJSONL = &jsonl
	event := CombatEvent{
		SessionNumber: 1, RPCID: 5, DamageID: 1001, HasDamageID: true,
		SourceEntityID: 8001, HasSourceEntity: true, TargetEntityID: 8002, HasTargetEntity: true,
		Damage: 438, ServerResultRaw: -438, HasServerResult: true, ChangeLifeRaw: -438, HasChangeLife: true,
		CharacterStatus: ResolutionUnknown, CharacterNameStatus: ResolutionUnknown,
		SkillStatus: ResolutionUnknown, SkillNameStatus: ResolutionUnknown, EventTypeStatus: ResolutionUnknown,
		Source: ResolveEntityIdentity(resolver, nil, 8001),
		Target: ResolveEntityIdentity(resolver, nil, 8002),
	}
	p.WriteDamageEvent(event)

	var got struct {
		SchemaVersion int `json:"schemaVersion"`
		Raw           struct {
			DamageID         uint64 `json:"damageId"`
			AttackerEntityID uint64 `json:"attackerEntityId"`
			TargetEntityID   uint64 `json:"targetEntityId"`
			ServerResultRaw  int32  `json:"serverResultRaw"`
			ChangeLifeRaw    int32  `json:"changeLifeRaw"`
		} `json:"raw"`
		Source struct {
			Lifecycle entityLifecycleJSONL `json:"lifecycle"`
		} `json:"source"`
		Target struct {
			Lifecycle entityLifecycleJSONL `json:"lifecycle"`
		} `json:"target"`
	}
	if err := json.Unmarshal(jsonl.Bytes(), &got); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if got.SchemaVersion != 5 {
		t.Fatalf("schemaVersion = %d, want 5", got.SchemaVersion)
	}
	if got.Raw.DamageID != 1001 || got.Raw.AttackerEntityID != 8001 || got.Raw.TargetEntityID != 8002 || got.Raw.ServerResultRaw != -438 || got.Raw.ChangeLifeRaw != -438 {
		t.Fatalf("raw block does not hold the observed request values: %+v", got.Raw)
	}
	source := got.Source.Lifecycle
	if source.State != LifecyclePresent || source.Status != ResolutionVerified || source.AddCount != 2 || source.RemoveCount != 1 || source.ReusedAfterRemove != 1 || source.Evidence != "EntityAddNotify.EntityPb.Id" {
		t.Fatalf("source lifecycle = %+v", source)
	}
	target := got.Target.Lifecycle
	if target.State != LifecycleUnknown || target.Status != ResolutionUnknown {
		t.Fatalf("unobserved target lifecycle should be UNKNOWN, got %+v", target)
	}
}
