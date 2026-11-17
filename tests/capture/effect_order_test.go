package capture

import (
	"reflect"
	"testing"
)

// The order of an Effects Overlay row is the engine's. These cases were checked on the frontend's sort until it moved to Go;
// the row order is read from the published view so the whole decision is covered, not only the comparison.

func TestEffectRowPlacesANewStatusByItsExpiry(t *testing.T) {
	apply := func(extra float64) []uint64 {
		p := newEffectViewProcessor()
		p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 2))
		p.Effects.ObserveApply(rowApply(2, 502, EffectPolarityBuff, 4))
		p.Effects.ObserveApply(rowApply(3, 503, EffectPolarityBuff, 6))
		p.Effects.ObserveApply(rowApply(4, 504, EffectPolarityBuff, extra))
		return rowIDs(effectViewAt(p, 0).Buffs)
	}
	for _, test := range []struct {
		remaining float64
		want      []uint64
	}{
		{1, []uint64{504, 501, 502, 503}},
		{5, []uint64{501, 502, 504, 503}},
		{9, []uint64{501, 502, 503, 504}},
	} {
		if got := apply(test.remaining); !reflect.DeepEqual(got, test.want) {
			t.Errorf("remaining %v: order %v, want %v", test.remaining, got, test.want)
		}
	}
}

func TestEffectRowMovesARefreshedStatusToItsNewExpiry(t *testing.T) {
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 2))
	p.Effects.ObserveApply(rowApply(2, 502, EffectPolarityBuff, 4))
	if got, want := rowIDs(effectViewAt(p, 0).Buffs), []uint64{501, 502}; !reflect.DeepEqual(got, want) {
		t.Fatalf("before the refresh: %v, want %v", got, want)
	}
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 9))
	if got, want := rowIDs(effectViewAt(p, 0).Buffs), []uint64{502, 501}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the refresh: %v, want %v", got, want)
	}
}

func TestEffectRowTimesAStatusOnceItsDurationIsKnown(t *testing.T) {
	p := newEffectViewProcessor()
	untimed := rowApply(1, 501, EffectPolarityBuff, 2)
	untimed.DurationSeconds, untimed.RemainingSeconds = nil, nil
	p.Effects.ObserveApply(untimed)
	p.Effects.ObserveApply(rowApply(2, 502, EffectPolarityBuff, 4))
	if got, want := rowIDs(effectViewAt(p, 0).Buffs), []uint64{502, 501}; !reflect.DeepEqual(got, want) {
		t.Fatalf("while unknown: %v, want %v", got, want)
	}
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 1))
	if got, want := rowIDs(effectViewAt(p, 0).Buffs), []uint64{501, 502}; !reflect.DeepEqual(got, want) {
		t.Fatalf("once known: %v, want %v", got, want)
	}
}

func TestEffectRowPutsUnreliableTimesLast(t *testing.T) {
	// A transport loss leaves the status unconfirmed, so its time no longer orders it.
	p := newEffectViewProcessor()
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 30))
	p.Effects.ObserveApply(rowApply(2, 502, EffectPolarityBuff, 20))
	p.Effects.MarkTransportLoss(effectAt(1))
	refreshed := rowApply(2, 502, EffectPolarityBuff, 40)
	refreshed.ObservedAt = effectAt(2)
	p.Effects.ObserveApply(refreshed)
	if got, want := rowIDs(effectViewAt(p, 3).Buffs), []uint64{502, 501}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stale: %v, want %v", got, want)
	}
	// A status past its time is overdue: it stays listed through the grace, behind the ones still counting down.
	p = newEffectViewProcessor()
	p.Effects.ObserveApply(rowApply(1, 501, EffectPolarityBuff, 1))
	p.Effects.ObserveApply(rowApply(2, 502, EffectPolarityBuff, 40))
	view := effectViewAt(p, 1.5)
	if got, want := rowIDs(view.Buffs), []uint64{502, 501}; !reflect.DeepEqual(got, want) {
		t.Fatalf("overdue: %v, want %v (%+v)", got, want, view.Buffs.Effects)
	}
}
