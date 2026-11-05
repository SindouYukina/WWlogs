//go:build wwdev

package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// TestEntityIDAllocationCollector measures, on archived captures, whether runtime EntityIds are ever reused inside one
// protocol session: removed and added again, added twice with different configurations, or seen again in a later
// scene. It also checks whether newly seen IDs are always larger than every earlier ID of the session. The result
// decides whether a client reference to a known EntityId after an inbound loss can identify the same entity
// (research note transport-recovery-backlog, Phase 2B). Skipped unless WWLOGS_ENTITY_ID_OUT is set.
//
// Environment: WWLOGS_ENTITY_ID_OUT (output directory), WWLOGS_SEGMENT_DROP_PCAPS, WWLOGS_SEGMENT_DROP_MASTER_DB.

type entityIDAllocation struct {
	Name                  string `json:"name"`
	Sessions              int    `json:"protocolSessions"`
	Scenes                int    `json:"sceneJoins"`
	DistinctIDs           int    `json:"distinctEntityIds"`
	Adds                  uint64 `json:"entityAdds"`
	ReAddAfterRemove      int    `json:"reAddedAfterRemove"`
	ReAddDifferentConfig  int    `json:"reAddedWithDifferentConfig"`
	DuplicateAddNoRemove  int    `json:"addedTwiceWithoutRemove"`
	SeenInLaterScene      int    `json:"idsPresentInMoreThanOneScene"`
	NonMonotonicNewIDs    int    `json:"newIdsBelowSessionMaximum"`
	LocalRoleEntities     int    `json:"localRoleEntities"`
	LocalRoleIDsAcrossScn int    `json:"localRoleEntityIdsKeptAcrossScenes"`
	// Reappeared counts IDs that were absent (removed or left out of a scene snapshot) and later present again.
	Reappeared            int `json:"reappearedAfterAbsence"`
	ReappearedOtherConfig int `json:"reappearedWithDifferentConfig"`
	ReappearedOtherKind   int `json:"reappearedAsDifferentKind"`
	LocalRoleIDReappeared int `json:"localRoleIdsReappeared"`
	LocalRoleIDAsNonLocal int `json:"localRoleIdsLaterNonLocal"`
}

func TestEntityIDAllocationCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_ENTITY_ID_OUT")
	if out == "" {
		t.Skip("WWLOGS_ENTITY_ID_OUT is not set")
	}
	pcaps := readSegmentDropList(t, os.Getenv("WWLOGS_SEGMENT_DROP_PCAPS"))
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	masters, err := loadGameMasterDatabase(os.Getenv("WWLOGS_SEGMENT_DROP_MASTER_DB"), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	var all []entityIDAllocation
	for _, capture := range pcaps {
		result := entityIDAllocation{Name: capture.name}
		type seen struct {
			adds, removes uint64
			config        uint64
			hasConfig     bool
			scenes        map[int]bool
			absent        bool
			kind          string
			wasLocalRole  bool
		}
		kindOf := func(p *processor, id uint64) string {
			entity, ok := p.entities.entities[id]
			switch {
			case !ok:
				return ""
			case entity.hasRoleID && entity.roleID != 0:
				return "role"
			case isExplicitMonsterEntity(entity):
				return "monster"
			case entity.hasSummonerID && entity.summonerID != 0:
				return "summon"
			default:
				return "other"
			}
		}
		ids := map[uint64]*seen{}
		session, scene := -1, 0
		var rolePointer uintptr
		var maxID uint64
		localRoleScenes := map[uint64]map[int]bool{}
		previous := map[uint64]entityLifecycle{}
		p, _ := equivalenceReplay(t, equivalenceInputs{localIP: "192.168.1.2"}, masters, key, capture.path, nil, func(_ int, p *processor) {
			if p.sessionNumber != session {
				session, maxID, previous = p.sessionNumber, 0, map[uint64]entityLifecycle{}
				result.Sessions++
			}
			// JoinSceneNotify replaces the role-entity set, so a new map marks a scene join.
			if pointer := reflect.ValueOf(p.playerRelevance.roleEntities).Pointer(); pointer != rolePointer {
				if rolePointer != 0 {
					scene++
					result.Scenes++
				}
				rolePointer = pointer
			}
			for id, record := range p.entities.lifecycles {
				before, known := previous[id]
				if known && before.addCount == record.addCount && before.removeCount == record.removeCount && before.state == record.state {
					continue
				}
				entry := ids[id]
				if entry == nil {
					entry = &seen{scenes: map[int]bool{}}
					ids[id] = entry
					if id < maxID {
						result.NonMonotonicNewIDs++
					}
				}
				if id > maxID {
					maxID = id
				}
				if record.addCount > before.addCount {
					if before.removeCount > 0 && before.state == lifecycleRemoved {
						result.ReAddAfterRemove++
					} else if before.addCount > 0 && before.state == lifecyclePresent {
						result.DuplicateAddNoRemove++
					}
					if entry.hasConfig && record.hasConfigID && entry.config != record.lastConfigID {
						result.ReAddDifferentConfig++
					}
				}
				present := record.state == lifecyclePresent
				kind := kindOf(p, id)
				if entry.absent && present {
					result.Reappeared++
					if entry.hasConfig && record.hasConfigID && entry.config != record.lastConfigID {
						result.ReappearedOtherConfig++
					}
					if entry.kind != "" && kind != "" && entry.kind != kind {
						result.ReappearedOtherKind++
					}
					if entry.wasLocalRole {
						result.LocalRoleIDReappeared++
					}
				}
				entry.absent = record.state == lifecycleRemoved
				if kind != "" {
					entry.kind = kind
				}
				if record.hasConfigID {
					entry.config, entry.hasConfig = record.lastConfigID, true
				}
				entry.scenes[scene] = true
				previous[id] = record
			}
			for id := range p.playerRelevance.roleEntities {
				if entry := ids[id]; entry != nil {
					entry.wasLocalRole = true
				}
				if localRoleScenes[id] == nil {
					localRoleScenes[id] = map[int]bool{}
				}
				localRoleScenes[id][scene] = true
			}
		})
		result.DistinctIDs = len(ids)
		for _, entry := range ids {
			if len(entry.scenes) > 1 {
				result.SeenInLaterScene++
			}
		}
		result.LocalRoleEntities = len(localRoleScenes)
		for id := range localRoleScenes {
			if entity, ok := p.entities.entities[id]; ok && !p.playerRelevance.isRoleEntity(id) && !p.playerRelevance.isLocalPlayerRoleEntity(entity) {
				result.LocalRoleIDAsNonLocal++
			}
		}
		for _, scenes := range localRoleScenes {
			if len(scenes) > 1 {
				result.LocalRoleIDsAcrossScn++
			}
		}
		result.Adds = uint64(p.stats.entityAdds)
		all = append(all, result)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "entity-id-allocation.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
