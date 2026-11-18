package capture

import "testing"

// benchmarkEffectView measures one Effects view over a tracker holding the given number of HUD statuses: classification,
// grouping into cells, ordering and the split into rows, which all run once per publish.
func benchmarkEffectView(b *testing.B, count uint64) {
	p := newEffectViewProcessor()
	for handle := uint64(1); handle <= count; handle++ {
		polarity := EffectPolarityBuff
		if handle%2 == 0 {
			polarity = EffectPolarityDebuff
		}
		p.Effects.ObserveApply(rowApply(handle, 1000+handle, polarity, float64(handle%60)+5))
	}
	p.CurrentFrameTime = effectAt(1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if view := p.viewEffects(); view.Counts.Cells == 0 {
			b.Fatal("no cells")
		}
	}
}

func BenchmarkEffectView12(b *testing.B)  { benchmarkEffectView(b, 12) }
func BenchmarkEffectView128(b *testing.B) { benchmarkEffectView(b, 128) }

// BenchmarkEffectViewMostlyNonHUD is the shape of real combat: hundreds of tracked instances of which few are drawn.
func BenchmarkEffectViewMostlyNonHUD(b *testing.B) {
	p := newEffectViewProcessor()
	for handle := uint64(1); handle <= 400; handle++ {
		event := rowApply(handle, 1000+handle, EffectPolarityBuff, float64(handle%60)+5)
		if handle%20 != 0 {
			event.Static = noHUDStatic()
		}
		p.Effects.ObserveApply(event)
	}
	p.CurrentFrameTime = effectAt(1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if view := p.viewEffects(); view.Counts.Tracked != 400 {
			b.Fatalf("tracked %d", view.Counts.Tracked)
		}
	}
}

// BenchmarkSkillRowNameMixedDamageList is the most expensive naming path a row can take.
func BenchmarkSkillRowNameMixedDamageList(b *testing.B) {
	names := newSkillNameMaster()
	names.damageListSkills = map[uint64]uint64{1: 10, 2: 11}
	names.uiSkillRoles = map[uint64]map[uint64]struct{}{10: {7: {}}, 11: {7: {}}}
	names.uiSkillTypes = map[uint64]int64{10: 5, 11: uiSkillTypeForte}
	names.uiSkillNames = map[uint64]string{10: "x"}
	names.skillGenres = map[uint64]int64{100: 4}
	master := &GameMaster{Skills: map[uint64]MasterRecord{}, skillNames: names}
	row := SkillDamageSnapshot{SkillID: 100, DamageIDs: []uint64{1, 2}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if name, _, _, _ := master.SkillRowName(7, row); name != "x" {
			b.Fatal(name)
		}
	}
}
