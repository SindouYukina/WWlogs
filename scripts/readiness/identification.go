package readiness

import (
	"fmt"
	"strings"
)

// Game content identification: whether the product can name what the History and the views show for the current client.
// content.history-identification reads what the History replay of the corpus could name (verify evidence
// history-summary.json identification): every stored enemy instance, main enemy, stage and kind of play, in every bundled
// Master locale, unless the Golden registry records why one cannot be named. (content.prefab-entities is a research check.)

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
