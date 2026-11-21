package desktop

import (
	"slices"
	"testing"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
)

// The library keeps a failed role lookup instead of showing the role as itself or in the neutral color: the static roles
// of its filters, and the role facets, which HistoryLibrary builds before it checks for one.
func TestLibraryReportsAFailedRoleLookup(t *testing.T) {
	resolver := brokenRoleMaster(t)
	names := &nameProjection{ctx: t.Context(), resolver: resolver, locale: "en-US"}
	if static := names.staticRoles(map[uint64]int{13127004: 1, 1409: 1}); static[13127004] != 1312 || static[1409] != 1409 || names.err != nil {
		t.Fatalf("static roles = %v %v, want the trial's parent and a role of its own", static, names.err)
	}
	names.staticRoles(map[uint64]int{13127005: 1})
	if names.err == nil {
		t.Fatal("a trial row whose parent is not an ID was read as a role of its own")
	}
	facetNames := &nameProjection{ctx: t.Context(), resolver: resolver, locale: "en-US"}
	libraryFacets(history.LibraryFacets{Roles: map[uint64]int{1312: 1}}, map[uint64]instanceNames{}, map[uint64]uint64{1312: 1312}, facetNames)
	if facetNames.err != nil {
		t.Fatalf("sound facet: %v", facetNames.err)
	}
	libraryFacets(history.LibraryFacets{Roles: map[uint64]int{1313: 2}}, map[uint64]instanceNames{}, map[uint64]uint64{1313: 1313}, facetNames)
	if facetNames.err == nil {
		t.Fatal("a role facet whose element is not a number was shown in the neutral color")
	}
	// Without a resolver a role shows as itself, with no element, and nothing fails.
	none := &nameProjection{ctx: t.Context()}
	if static, element := none.staticRole(13127004), none.element(13127004); static != 13127004 || element != 0 || none.err != nil {
		t.Fatalf("no resolver: %d %d %v", static, element, none.err)
	}
}

// The library's Master-backed filters select stored IDs: a mode or stage selects instances, a character selects every
// runtime RoleId shown as it (a trial role is its parent), and a text search matches mode, stage and character names.
func TestLibraryFiltersResolveToStoredIDs(t *testing.T) {
	instances := map[uint64]instanceNames{
		20016: {modeKey: "category:Tower", mode: historyMode{Kind: capture.InstanceModeCategory, Name: "Tower"}, stage: historyName{Name: "V1-1-4"}},
		8:     {modeKey: "open_world", mode: historyMode{Kind: capture.InstanceModeOpenWorld}, stage: historyName{Name: "Huanglong"}},
	}
	static := map[uint64]uint64{1512: 1512, 15127004: 1512, 1409: 1409}
	names := map[uint64]string{1512: "Lily", 1409: "Cartethyia"}
	roleName := func(role uint64) string { return names[role] }
	sorted := func(values []uint64) []uint64 { out := slices.Clone(values); slices.Sort(out); return out }

	byMode := resolveLibraryFilters(HistoryLibraryQuery{Modes: []string{"open_world"}}, instances, static, roleName)
	if !slices.Equal(byMode.Instances, []uint64{8}) || byMode.RoleIDs != nil {
		t.Fatalf("mode = %+v", byMode)
	}
	byStage := resolveLibraryFilters(HistoryLibraryQuery{Stages: []uint64{20016}}, instances, static, roleName)
	if !slices.Equal(byStage.Instances, []uint64{20016}) {
		t.Fatalf("stage = %+v", byStage)
	}
	if none := resolveLibraryFilters(HistoryLibraryQuery{Modes: []string{"category:Unknown"}}, instances, static, roleName); none.Instances == nil || len(none.Instances) != 0 {
		t.Fatalf("an unmatched mode must select nothing, not everything: %+v", none)
	}
	byRole := resolveLibraryFilters(HistoryLibraryQuery{Roles: []uint64{1512}}, instances, static, roleName)
	if !slices.Equal(sorted(byRole.RoleIDs), []uint64{1512, 15127004}) {
		t.Fatalf("role = %+v", byRole)
	}
	text := resolveLibraryFilters(HistoryLibraryQuery{Text: " lily "}, instances, static, roleName)
	if len(text.MatchInstances) != 0 || !slices.Equal(sorted(text.MatchRoleIDs), []uint64{1512, 15127004}) {
		t.Fatalf("text = %+v", text)
	}
	stageText := resolveLibraryFilters(HistoryLibraryQuery{Text: "v1-1"}, instances, static, roleName)
	if !slices.Equal(stageText.MatchInstances, []uint64{20016}) || len(stageText.MatchRoleIDs) != 0 {
		t.Fatalf("stage text = %+v", stageText)
	}
	if plain := resolveLibraryFilters(HistoryLibraryQuery{}, instances, static, roleName); plain.Instances != nil || plain.RoleIDs != nil || plain.MatchInstances != nil {
		t.Fatalf("no filter = %+v", plain)
	}
}
