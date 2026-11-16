package readiness

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Game content identification: whether the product can name what the History and the views show for the current client.
// content.prefab-entities compares the product's identity of every entity of every prefab with the client's own
// PrefabConfig and TemplateConfig (research input clientContent.prefabEntities); a Master without those relations leaves
// every enemy a prefab spawns unidentified (Global 3.8: the Tower of Adversity and the ShipTower stages showed "未識別" as
// their main enemy). content.history-identification reads what the History replay of the corpus could name (verify evidence
// history-summary.json identification): every stored enemy instance, main enemy, stage and kind of play, in every bundled
// Master locale, unless the Golden registry records why one cannot be named.

// prefabEntity is one entity of a prefab as the client defines it: the TemplateConfig rows and MonsterInfo rows its
// BlueprintType names in the client's own tables (work/compat-3.8/tools/prefab_identity.py).
type prefabEntity struct {
	Prefab    string   `json:"prefab"`
	Entity    string   `json:"entity"`
	Blueprint string   `json:"blueprint"`
	Templates []string `json:"templates"`
	Monsters  []string `json:"monsters"`
}

// prefabInventory is the prefab part of the client content inventory.
type prefabInventory struct {
	Client         string          `json:"client"`
	PrefabEntities *[]prefabEntity `json:"prefabEntities"`
	Exclusions     []struct {
		Table  string `json:"table"`
		ID     string `json:"id"`
		Reason string `json:"reason"`
	} `json:"exclusions"`
}

// prefabExclusionTable is the exclusion table name of a prefab entity; its id is "<prefab>/<entity>".
const prefabExclusionTable = "prefabentity"

func (g *gate) prefabEntityCheck(c *Check, m *masterView) {
	if g.in.ClientContent == "" {
		c.unverified("no client content inventory was supplied (research input clientContent; PROCEDURE.md S4 step 9)")
		return
	}
	var inventory prefabInventory
	if err := readJSON(g.in.ClientContent, &inventory); err != nil {
		c.unverified(fmt.Sprintf("the client content inventory cannot be read: %v", err))
		return
	}
	if inventory.PrefabEntities == nil {
		c.unverified("the client content inventory lists no prefab entities: regenerate it with client_content_inventory.py (PROCEDURE.md S4 step 9)")
		return
	}
	if m.product == nil {
		c.unverified("the product Master could not be loaded")
		return
	}
	if g.source != nil && g.source.protocolClient != "" && inventory.Client != g.source.protocolClient {
		c.fail("the inventory describes client %q, the decoder reads %q: extract the inventory and the Master from the same client (PROCEDURE.md S4)", inventory.Client, g.source.protocolClient)
	}
	excluded := map[string]bool{}
	for _, exclusion := range inventory.Exclusions {
		if exclusion.Table != prefabExclusionTable {
			continue
		}
		if exclusion.Reason == "" {
			c.fail("the exclusion of prefab entity %s states no reason", exclusion.ID)
			continue
		}
		excluded[exclusion.ID] = true
	}
	entities := *inventory.PrefabEntities
	clientKeys := make(map[string]bool, len(entities))
	passed := 0
	for _, entity := range entities {
		key := entity.Prefab + "/" + entity.Entity
		clientKeys[key] = true
		if excluded[key] {
			passed++
			continue
		}
		prefabID, errPrefab := strconv.ParseUint(entity.Prefab, 10, 64)
		entityID, errEntity := strconv.ParseUint(entity.Entity, 10, 64)
		if errPrefab != nil || errEntity != nil {
			c.fail("prefab entity %q of the inventory is not numeric", key)
			continue
		}
		got, ok := m.product.PrefabEntity(prefabID, entityID)
		var templates []string
		for _, id := range got.TemplateIDs {
			templates = append(templates, strconv.FormatUint(id, 10))
		}
		monster, wantMonster := "", ""
		if got.MonsterID != 0 {
			monster = strconv.FormatUint(got.MonsterID, 10)
		}
		if len(entity.Monsters) == 1 {
			wantMonster = entity.Monsters[0]
		}
		subject := "prefab entity " + key
		switch {
		case !ok && (len(entity.Templates) > 0 || wantMonster != ""):
			c.failSubject(subject, "prefab %s entity %s (%s): the Master has no relation, so the product leaves this enemy unidentified", entity.Prefab, entity.Entity, entity.Blueprint)
		case !slices.Equal(templates, entity.Templates):
			c.failSubject(subject, "prefab %s entity %s (%s): the product names TemplateConfig %v, the client %v", entity.Prefab, entity.Entity, entity.Blueprint, templates, entity.Templates)
		case monster != wantMonster:
			c.failSubject(subject, "prefab %s entity %s (%s): the product names MonsterInfo %q, the client's single MonsterInfo is %q (of %v)", entity.Prefab, entity.Entity, entity.Blueprint, monster, wantMonster, entity.Monsters)
		default:
			passed++
		}
	}
	// A relation the Master holds for an entity the client no longer defines would name an enemy from another client.
	var stale []string
	for _, table := range []string{"prefab_entity_template_ref", "prefab_entity_monster"} {
		exists, err := m.count("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table)
		if err != nil {
			c.fail("the Master's %s cannot be inspected: %v", table, err)
			continue
		}
		if exists == 0 {
			continue
		}
		keys, err := m.strings("SELECT DISTINCT prefab_id || '/' || entity_id FROM " + table)
		if err != nil {
			c.fail("the Master's %s cannot be read: %v", table, err)
			continue
		}
		for _, key := range keys {
			if !clientKeys[key] && !slices.Contains(stale, key) {
				stale = append(stale, key)
			}
		}
	}
	slices.Sort(stale)
	for i, key := range stale {
		if i == 20 {
			c.fail("and %d more", len(stale)-20)
			break
		}
		c.failSubject("prefab entity "+key, "the Master relates prefab entity %s, which the client does not define", key)
	}
	c.Coverage = &Coverage{Passed: passed, Total: len(entities), Denominator: "entities of the client's prefabs (PrefabConfig.Entities), identified by capture.MasterData.PrefabEntity"}
	c.Evidence = append(c.Evidence, fmt.Sprintf("inventory %s: %d prefab entities, %d explicit exclusions", inventory.Client, len(entities), len(excluded)))
}

// historyIdentification is the identification part of the History replay summary (tests/history replay_test.go).
type historyIdentification struct {
	EnemyInstances      int `json:"enemyInstances"`
	EnemiesUnidentified int `json:"enemiesUnidentified"`
	MainEnemies         int `json:"mainEnemies"`
	ScenesKnown         int `json:"scenesKnown"`
	Unidentified        []struct {
		Capture        string   `json:"capture"`
		Encounter      string   `json:"encounter"`
		Ordinal        int      `json:"ordinal"`
		MonsterID      uint64   `json:"monsterId"`
		TemplateID     uint64   `json:"templateId"`
		Conflict       bool     `json:"conflict"`
		MissingLocales []string `json:"missingLocales"`
	} `json:"unidentified"`
	UnresolvedScenes []struct {
		Capture        string   `json:"capture"`
		Encounter      string   `json:"encounter"`
		InstanceID     uint64   `json:"instanceId"`
		StageMissing   []string `json:"stageMissing"`
		ModeUnresolved []string `json:"modeUnresolved"`
	} `json:"unresolvedScenes"`
}

// identificationLimit is a recorded reason why one stored enemy, stage or kind of play of a capture cannot be named
// (tests/capture/golden_registry.json captures[].identificationLimits). kind is "enemy" (encounter and ordinal), "stage"
// or "mode" (scene InstanceId).
type identificationLimit struct {
	Kind      string `json:"kind"`
	Scene     uint64 `json:"scene,omitempty"`
	Encounter string `json:"encounter,omitempty"`
	Ordinal   *int   `json:"ordinal,omitempty"`
	Reason    string `json:"reason"`
}

func (g *gate) historyIdentificationCheck(c *Check, summary historySummary) {
	id := summary.Identification
	if id == nil {
		c.unverified("the History replay summary has no identification counts (rerun scripts/verify.ps1 with this tree's tests/history collector)")
		return
	}
	var registry goldenRegistry
	if err := readJSON(g.in.GoldenRegistry, &registry); err != nil {
		c.fail("the Golden registry cannot be read: %v", err)
		return
	}
	limitsOf := func(capture string) []identificationLimit {
		var out []identificationLimit
		for _, entry := range registry.Captures {
			if strings.Contains(capture, entry.Name) {
				out = append(out, entry.IdentificationLimits...)
			}
		}
		return out
	}
	for _, entry := range registry.Captures {
		for _, limit := range entry.IdentificationLimits {
			if limit.Reason == "" {
				c.fail("the identification limit %s of %s states no reason", limit.Kind, entry.Name)
			}
		}
	}
	excused := func(capture, kind string, match func(identificationLimit) bool) bool {
		for _, limit := range limitsOf(capture) {
			if limit.Kind == kind && limit.Reason != "" && match(limit) {
				return true
			}
		}
		return false
	}
	unexcused := 0
	for _, enemy := range id.Unidentified {
		if excused(enemy.Capture, "enemy", func(l identificationLimit) bool {
			return l.Encounter == enemy.Encounter && l.Ordinal != nil && *l.Ordinal == enemy.Ordinal
		}) {
			continue
		}
		unexcused++
		what := "has no MonsterInfo or TemplateConfig identity"
		switch {
		case enemy.Conflict:
			what = "reported conflicting identities"
		case enemy.MonsterID != 0 || enemy.TemplateID != 0:
			what = fmt.Sprintf("(monster %d, template %d) has no name in %s", enemy.MonsterID, enemy.TemplateID, strings.Join(enemy.MissingLocales, ", "))
		}
		c.failSubject(fmt.Sprintf("enemy %s/%s/%d", enemy.Capture, enemy.Encounter, enemy.Ordinal), "%s encounter %s enemy %d %s", enemy.Capture, enemy.Encounter, enemy.Ordinal, what)
	}
	scenesFailed := 0
	for _, scene := range id.UnresolvedScenes {
		failed := false
		if len(scene.StageMissing) > 0 && !excused(scene.Capture, "stage", func(l identificationLimit) bool { return l.Scene == scene.InstanceID }) {
			failed = true
			c.failSubject(fmt.Sprintf("stage %s/%s", scene.Capture, scene.Encounter), "%s encounter %s: scene %d has no stage name in %s", scene.Capture, scene.Encounter, scene.InstanceID, strings.Join(scene.StageMissing, ", "))
		}
		if len(scene.ModeUnresolved) > 0 && !excused(scene.Capture, "mode", func(l identificationLimit) bool { return l.Scene == scene.InstanceID }) {
			failed = true
			c.failSubject(fmt.Sprintf("mode %s/%s", scene.Capture, scene.Encounter), "%s encounter %s: scene %d has no kind of play in %s", scene.Capture, scene.Encounter, scene.InstanceID, strings.Join(scene.ModeUnresolved, ", "))
		}
		if failed {
			scenesFailed++
		}
	}
	c.Coverage = &Coverage{Passed: id.EnemyInstances - unexcused, Total: id.EnemyInstances, Denominator: "enemy instances the History replay stored, named in every bundled Master locale"}
	c.Evidence = append(c.Evidence, fmt.Sprintf("%d enemy instances (%d without identity), %d main enemies, %d known scenes (%d without a stage name or kind of play), %d recorded limits excused",
		id.EnemyInstances, id.EnemiesUnidentified, id.MainEnemies, id.ScenesKnown, scenesFailed, len(id.Unidentified)-unexcused+len(id.UnresolvedScenes)-scenesFailed))
}
