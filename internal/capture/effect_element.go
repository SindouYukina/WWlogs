package capture

// An effect's element colors its countdown on the Effects Overlay. It comes only from what the effect's own definition
// says, never from the character who applied it, its name or the look of its icon, so most effects have none. Element
// numbers are ElementInfo ids: 1 Glacio, 2 Fusion, 3 Electro, 4 Aero, 5 Spectro, 6 Havoc.

// Element bases name the evidence of an effect's element.
const (
	effectElementBasisTerm      = "element_ailment_term"
	effectElementBasisAttribute = "element_attribute_modifier"
)

// termElements maps the official glossary terms of the six element ailments to their element: the term's localized name
// and description name it (Glacio Chafe, Fusion Burst, Electro Flare, Aero Erosion, Spectro Frazzle, Havoc Bane).
var termElements = map[uint64]uint64{850010: 1, 850011: 2, 850013: 3, 850009: 4, 850008: 5, 850012: 6}

// elementAttributeFamilies are the runs of EAttributeType values that exist once per element, in ElementInfo order:
// PropertyIndex names attributes 22..27 Glacio to Havoc DMG Bonus, 29..34 the RES, 38..43 the DMG Reduction, 89..94
// "Attribute N Energy Value" and 101..106 "Ignore Attribute N Damage Resistance".
var elementAttributeFamilies = []uint64{22, 29, 38, 89, 101}

// elementOfAttribute returns the element of an element-specific attribute. Physical attributes have none.
func elementOfAttribute(attribute uint64) (uint64, bool) {
	for _, first := range elementAttributeFamilies {
		if attribute >= first && attribute < first+6 {
			return attribute - first + 1, true
		}
	}
	return 0, false
}

// effectElementFor returns the element a definition states and the evidence for it. A glossary term decides first, then
// a modifier of an element-specific attribute. It says what element the effect concerns, not who benefits from it.
func effectElementFor(termID uint64, hasTerm bool, static buffStaticParameters) (uint64, string) {
	if hasTerm {
		if element, known := termElements[termID]; known {
			return element, effectElementBasisTerm
		}
	}
	if static.hasAttribute && static.hasModifier {
		if element, known := elementOfAttribute(static.attribute); known {
			return element, effectElementBasisAttribute
		}
	}
	return 0, ""
}
