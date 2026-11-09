package capture

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openProductMaster(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join("..", "..", "data", "master_data.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestElementOfAttributeCoversTheElementFamiliesOnly(t *testing.T) {
	cases := map[uint64]uint64{22: 1, 27: 6, 29: 1, 34: 6, 38: 1, 43: 6, 89: 1, 94: 6, 101: 1, 106: 6}
	for attribute, want := range cases {
		if got, ok := elementOfAttribute(attribute); !ok || got != want {
			t.Errorf("attribute %d = %d/%v, want %d", attribute, got, ok, want)
		}
	}
	// Physical bonus, resistance, reduction, penetration, and ATK, crit and general damage attributes state no element.
	for _, attribute := range []uint64{7, 8, 9, 15, 21, 28, 37, 95, 97, 100, 107} {
		if got, ok := elementOfAttribute(attribute); ok {
			t.Errorf("attribute %d must state no element, got %d", attribute, got)
		}
	}
}

func TestEffectElementUsesTheDefinitionOnly(t *testing.T) {
	modifier := func(attribute uint64) buffStaticParameters {
		return buffStaticParameters{attribute: attribute, hasAttribute: true, hasModifier: true}
	}
	cases := []struct {
		name    string
		term    uint64
		hasTerm bool
		static  buffStaticParameters
		want    uint64
		basis   string
	}{
		{"ailment term", 850009, true, buffStaticParameters{}, 4, effectElementBasisTerm},
		{"a term wins over an attribute", 850012, true, modifier(22), 6, effectElementBasisTerm},
		{"element bonus", 0, false, modifier(24), 3, effectElementBasisAttribute},
		{"unknown term falls back to the attribute", 1, true, modifier(25), 4, effectElementBasisAttribute},
		{"attack is not an element", 0, false, modifier(7), 0, ""},
		{"an attribute without a modifier states nothing", 0, false, buffStaticParameters{attribute: 24, hasAttribute: true}, 0, ""},
		{"no evidence", 0, false, buffStaticParameters{}, 0, ""},
	}
	for _, tc := range cases {
		got, basis := effectElementFor(tc.term, tc.hasTerm, tc.static)
		if got != tc.want || basis != tc.basis {
			t.Errorf("%s: element %d (%s), want %d (%s)", tc.name, got, basis, tc.want, tc.basis)
		}
	}
}

// The element numbers are ElementInfo ids; the official English names of the terms and attributes must agree with them.
func TestEffectElementsMatchTheOfficialMasterNames(t *testing.T) {
	db := openProductMaster(t)
	defer db.Close()
	elementName := func(id uint64) string {
		var name string
		if err := db.QueryRow(`SELECT name FROM elementinfo_locale WHERE id = ? AND locale = 'en-US'`, id).Scan(&name); err != nil {
			t.Fatalf("element %d: %v", id, err)
		}
		return name
	}
	for term, element := range termElements {
		var name string
		if err := db.QueryRow(`SELECT name FROM term_locale WHERE id = ? AND locale = 'en-US'`, term).Scan(&name); err != nil {
			t.Fatalf("term %d: %v", term, err)
		}
		if !strings.HasPrefix(name, elementName(element)) {
			t.Errorf("term %d %q is not element %d %q", term, name, element, elementName(element))
		}
	}
	for _, first := range []uint64{22, 29, 38} {
		for offset := uint64(0); offset < 6; offset++ {
			var name string
			if err := db.QueryRow(`SELECT name FROM propertyindex_locale WHERE id = ? AND locale = 'en-US'`, first+offset).Scan(&name); err != nil {
				t.Fatalf("attribute %d: %v", first+offset, err)
			}
			element, ok := elementOfAttribute(first + offset)
			if !ok || !strings.HasPrefix(name, elementName(element)) {
				t.Errorf("attribute %d %q is not element %d", first+offset, name, element)
			}
		}
	}
}

func TestGenericHUDIconsAreThoseSharedByManyDefinitions(t *testing.T) {
	db := openProductMaster(t)
	defer db.Close()
	statics := map[uint64]buffStaticParameters{}
	if err := loadBuffHUDCues(db, statics); err != nil {
		t.Fatal(err)
	}
	for id, generic := range map[uint64]bool{110038111: true, 110025020: true, 110006406: true, 1312010033: false, 10041000: false, 2002202312: false} {
		if statics[id].hudIconGeneric != generic {
			t.Errorf("effect %d generic = %v, want %v", id, statics[id].hudIconGeneric, generic)
		}
	}
}

func TestViewProjectsTheStripFieldsWithoutChangingTheInstance(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(displayApply(1, 900, 0, func(e *EffectApplyEvent) {
		e.DefinitionLinked, e.Static = true, &EffectStaticInfo{HUDCueTypes: []int32{2}, HUDIconGeneric: true, ElementID: 3, ElementBasis: effectElementBasisAttribute}
	}))
	view := p.viewEffects()
	if len(view.Active) != 1 {
		t.Fatalf("listed %d", len(view.Active))
	}
	got := view.Active[0]
	if !got.HUDIcon || !got.HUDIconGeneric || got.ElementID == nil || *got.ElementID != 3 {
		t.Fatalf("strip fields: %+v", got)
	}
	if got.Polarity != EffectPolarityUnclassified {
		t.Fatalf("an element must not decide polarity: %s", got.Polarity)
	}
	if got.ExpectedExpiry == nil {
		t.Fatal("a timed effect must carry its expected expiry")
	}
}

func TestViewClockLeadOnlyAdvancesTheCountdownsOfTheView(t *testing.T) {
	p := newEffectViewProcessor()
	p.effects.ObserveApply(displayApply(1, 900, 0, nil))
	before := effectViewAt(p, 1).Active[0]
	frozen := p.effectSnapshotAtLastFrame().Effects[0]
	p.viewClockLead = 500 * time.Millisecond
	after := p.viewEffects().Active[0]
	if before.RemainingSeconds == nil || after.RemainingSeconds == nil {
		t.Fatal("expected timed effects")
	}
	if diff := *before.RemainingSeconds - *after.RemainingSeconds; diff < 0.499 || diff > 0.501 {
		t.Fatalf("the view advanced %.3f s", diff)
	}
	if !before.ExpectedExpiry.Equal(*after.ExpectedExpiry) || after.Key != before.Key || after.Lifecycle != before.Lifecycle {
		t.Fatalf("the lead must not move the expiry or the identity: %+v vs %+v", before, after)
	}
	// Diagnostics and statistics keep reading at the newest frame.
	still := p.effectSnapshotAtLastFrame().Effects[0]
	if *still.RemainingSeconds != *frozen.RemainingSeconds {
		t.Fatalf("frame-time readers moved: %v vs %v", *still.RemainingSeconds, *frozen.RemainingSeconds)
	}
}
