package readiness

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"wwlogs/internal/capture"
)

// The Master-backed domains. The Master is read twice: as plain SQLite for the client definitions the gate states
// independently (which buffs the HUD draws, which roles are playable, which guide categories the Adventure Guide uses), and
// through the product's own loader for the decisions under test (effect definitions, trial parents, instance modes).

// masterLocales are the Master display-name locales of the bundled UI languages (frontend/locales/manifest.json).
var masterLocales = []string{"zh-TW", "zh-CN", "en-US", "ja-JP", "ko-KR"}

// hudCueTypes and the Path condition are the client's HUD buff-bar rule (BuffItemContainer, BuffItem.Activate), stated
// here from the client so the product's classification can be checked against it.
var hudCueTypes = []int{2, 14, 24, 37}

type masterView struct {
	db       *sql.DB
	product  *capture.MasterData
	borrowed bool
	sha256   string
	playable []uint64
}

func (m *masterView) close() {
	if m.product != nil && !m.borrowed {
		_ = m.product.Close()
	}
	if m.db != nil {
		_ = m.db.Close()
	}
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func openReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro&immutable=1&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, db.Ping()
}

func (m *masterView) ids(query string, args ...any) ([]uint64, error) {
	rows, err := m.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uint64
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		id, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("id %q: %w", raw, err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (m *masterView) count(query string, args ...any) (int, error) {
	var n int
	err := m.db.QueryRow(query, args...).Scan(&n)
	return n, err
}

func (g *gate) masterChecks() {
	integrity := g.check("master.integrity", DomainMaster, "SQLite integrity_check and foreign_key_check of the tracked Master", KindStatic, true)
	loads := g.check("master.loads", DomainMaster, "The product loader opens the Master in every bundled Master locale", KindStatic, true)
	relations := g.check("master.relations", DomainMaster, "Required lookup relations resolve (trial parents, skill groups, HUD cues, guide categories, entrances)", KindStatic, true)
	names := g.check("master.role-names", DomainMaster, "Every playable role has a name in every bundled Master locale", KindStatic, true)
	art := g.check("characters.art", DomainCharacters, "Every playable role has its avatar and portrait in the image pack", KindStatic, true)
	trial := g.check("characters.trial-parent", DomainCharacters, "Every trial of a playable role resolves to that role through the product (art, name, element)", KindStatic, true)
	skillIcons := g.check("characters.skill-icons", DomainCharacters, "Every UI skill of a playable role has its icon in the image pack", KindStatic, true)
	skillNames := g.check("characters.skill-names", DomainCharacters, "Every playable role names its UI skills in every bundled Master locale", KindStatic, true)
	hudClass := g.check("effects.hud-classification", DomainEffects, "Every effect the client HUD draws gets a displayable classification from the product (buff, debuff or neutral)", KindStatic, true)
	hudIcons := g.check("effects.hud-icons", DomainEffects, "Every effect the client HUD draws has its icon in the image pack", KindStatic, true)
	categories := g.check("content.guide-categories", DomainContent, "Every Adventure Guide category the client's detection rows use is named in every bundled Master locale", KindStatic, true)
	modes := g.check("content.instance-modes", DomainContent, "Every Adventure Guide category with an instance only it names resolves through the product's mode resolver", KindStatic, true)
	// Advisory: content the client data itself cannot place is reported, never guessed and never a blocker.
	unresolved := g.check("content.unresolved-categories", DomainContent, "Guide categories the client data names only ambiguously (reported limitation)", KindStatic, false)
	content := g.researchCheck("master.client-content", DomainMaster, "The Master holds what the current client defines (private research check)")
	prefabs := g.researchCheck("content.prefab-entities", DomainContent, "The product identifies every entity of the client's prefabs (private research check)")
	settlement := g.check("combat.negative-status-settlement", DomainCharacters, "Every negative-status DamageId (server-only FormulaType 9) is executed by status Buffs whose settlement the product reads (timed, on application, instant, spreading on overflow), and every InvokePeriod forces only timed statuses", KindStatic, true)
	dependents := []*Check{loads, relations, names, art, trial, skillIcons, skillNames, hudClass, hudIcons, categories, modes, unresolved, content, prefabs, settlement}

	hash, err := fileSHA256(g.in.MasterPath)
	if err != nil {
		integrity.fail("the Master cannot be read: %v", err)
		for _, c := range dependents {
			c.unverified("the Master cannot be read")
		}
		return
	}
	g.report.MasterSHA256 = hash
	db, err := openReadOnly(g.in.MasterPath)
	if err != nil {
		integrity.fail("the Master cannot be opened: %v", err)
		for _, c := range dependents {
			c.unverified("the Master cannot be opened")
		}
		return
	}
	m := &masterView{db: db, sha256: hash}
	g.master = m
	integrity.Evidence = append(integrity.Evidence, "Master SHA-256 "+hash)
	known, ok := integrityResults.Load(hash)
	if !ok {
		var result integrityResult
		if err := db.QueryRow("PRAGMA integrity_check").Scan(&result.check); err != nil {
			result.err = err
		} else if result.foreignKeys, err = m.count("SELECT count(*) FROM pragma_foreign_key_check"); err != nil {
			result.err = err
		}
		integrityResults.Store(hash, result)
		known = result
	}
	switch result := known.(integrityResult); {
	case result.err != nil:
		integrity.fail("the integrity checks cannot run: %v", result.err)
	case result.check != "ok":
		integrity.fail("PRAGMA integrity_check: %q", result.check)
	case result.foreignKeys != 0:
		integrity.fail("PRAGMA foreign_key_check: %d violations", result.foreignKeys)
	}

	// The product loader, once per locale; the first locale's data answers the product decisions below.
	if g.in.Product != nil {
		m.product, m.borrowed = g.in.Product, true
		if g.in.Product.SHA256() != hash {
			loads.fail("the supplied product Master is %s, the file is %s", short(g.in.Product.SHA256()), short(hash))
		}
		loads.Evidence = append(loads.Evidence, "product Master supplied by the caller")
	}
	for _, locale := range g.in.Locales {
		// The loader's answer for the same bytes is remembered; the first locale's data is still needed for the product
		// decisions when no caller supplied it.
		if known, ok := loaderResults.Load(hash + "/" + locale); ok && (m.product != nil || known.(string) != "") {
			if text := known.(string); text != "" {
				loads.fail("locale %s: %s", locale, text)
			} else {
				loads.Evidence = append(loads.Evidence, locale+" loaded (same bytes earlier in this process)")
			}
			continue
		}
		started := time.Now()
		product, err := capture.LoadMasterData(g.in.MasterPath, locale)
		if err != nil {
			loaderResults.Store(hash+"/"+locale, err.Error())
			loads.fail("locale %s: %v", locale, err)
			continue
		}
		loads.Evidence = append(loads.Evidence, fmt.Sprintf("%s loaded in %s", locale, time.Since(started).Round(time.Millisecond)))
		if product.SHA256() != hash {
			loads.fail("locale %s: the loader read Master %s, the file is %s", locale, short(product.SHA256()), short(hash))
		} else {
			loaderResults.Store(hash+"/"+locale, "")
		}
		if m.product == nil {
			m.product = product
		} else {
			_ = product.Close()
		}
	}
	loaded := 0
	for _, locale := range masterLocales {
		for _, checked := range g.in.Locales {
			if checked == locale {
				loaded++
			}
		}
	}
	loads.Coverage = &Coverage{Passed: loaded, Total: len(masterLocales), Denominator: "bundled Master locales loaded by capture.LoadMasterData"}
	if loaded < len(masterLocales) {
		loads.unverified(fmt.Sprintf("only %d of %d Master locales were loaded", loaded, len(masterLocales)))
	}

	playable, err := m.ids("SELECT id FROM roleinfo WHERE role_type = 1 ORDER BY CAST(id AS INTEGER)")
	if err != nil || len(playable) == 0 {
		names.fail("no playable role (roleinfo.role_type 1) can be read: %v", err)
		for _, c := range []*Check{art, trial, skillIcons, skillNames} {
			c.unverified("no playable role could be read")
		}
	} else {
		m.playable = playable
		g.relationChecks(relations, m)
		g.roleNameCheck(names, m)
		g.artCheck(art, m)
		g.trialCheck(trial, m)
		g.skillChecks(skillIcons, skillNames, m)
	}
	g.hudChecks(hudClass, hudIcons, m)
	g.contentChecks(categories, modes, unresolved, m)
	g.research(content)
	g.research(prefabs)
	g.negativeStatusCheck(settlement, m)
}

// negativeStatusCheck holds the inputs of the negative-status role credit (internal/capture/negative_status_credit.go,
// negative_status_causes.go) to the Master: every DamageId of the server-only formula has executing status Buffs, each with
// the settlement columns the credit reads (period, on-creation execution, overflow clear, duration policy), all taken by
// the product as executing it and read the same as their row: timed, settled on an application, instant (no instance), or
// spreading on overflow to other enemies (no period and an overflow that clears the instance); a status that spreads on
// overflow needs a positive stack limit, since its overflow is an application at that limit; and every InvokePeriod (extra
// effect 67) names only statuses with a period, the only ones whose execution it can force (InvokePeriod.OnExecute ->
// ApplyPeriodExecution). A new negative status, or one that settles in a new way, fails here until the settlement research
// covers it.
func (g *gate) negativeStatusCheck(c *Check, m *masterView) {
	damageIDs, err := m.ids(`SELECT id FROM damage WHERE CAST(formula_type AS TEXT) = '9' ORDER BY CAST(id AS INTEGER)`)
	if err != nil || len(damageIDs) == 0 {
		c.fail("no negative-status damage row (damage.formula_type 9) can be read: %v", err)
		return
	}
	type executor struct {
		id, damageID                                                  uint64
		period, onCreation, clearOnOverflow, durationPolicy, stackMax sql.NullString
	}
	var executors []executor
	byDamage := map[uint64]int{}
	for _, damageID := range damageIDs {
		rows, err := m.db.Query(`SELECT id, CAST(period AS TEXT), CAST(b_execute_periodic_effect_on_application AS TEXT), CAST(b_clear_stack_on_overflow AS TEXT),
			CAST(duration_policy AS TEXT), CAST(stack_limit_count AS TEXT) FROM buff WHERE CAST(extra_effect_id AS TEXT) = ? ORDER BY CAST(id AS INTEGER)`, fmt.Sprint(damageID))
		if err != nil {
			c.fail("the status Buffs of DamageId %d cannot be read: %v", damageID, err)
			return
		}
		for rows.Next() {
			var rawID string
			found := executor{damageID: damageID}
			if err := rows.Scan(&rawID, &found.period, &found.onCreation, &found.clearOnOverflow, &found.durationPolicy, &found.stackMax); err != nil {
				rows.Close()
				c.fail("a status Buff of DamageId %d cannot be read: %v", damageID, err)
				return
			}
			if _, err := fmt.Sscan(rawID, &found.id); err != nil {
				rows.Close()
				c.fail("status Buff id %q of DamageId %d is not a number", rawID, damageID)
				return
			}
			executors = append(executors, found)
			byDamage[damageID]++
		}
		rows.Close()
		if byDamage[damageID] == 0 {
			c.failSubject(fmt.Sprintf("DamageId %d", damageID), "negative-status DamageId %d has no executing status Buff (buff.extra_effect_id %d)", damageID, damageID)
		}
	}
	timed := map[uint64]bool{}
	complete, recognized, periodic, applied, instant, spreading := 0, 0, 0, 0, 0, 0
	for _, e := range executors {
		if !e.period.Valid || !e.onCreation.Valid || !e.clearOnOverflow.Valid || !e.durationPolicy.Valid {
			c.failSubject(fmt.Sprintf("status Buff %d", e.id), "status Buff %d of DamageId %d lacks a settlement column (period, on-creation execution, overflow clear or duration policy)", e.id, e.damageID)
			continue
		}
		complete++
		var period float64
		if _, err := fmt.Sscan(e.period.String, &period); err == nil && period > 0 {
			timed[e.id] = true
			periodic++
		} else {
			applied++
		}
		isInstant := e.durationPolicy.String == "0"
		if isInstant {
			instant++
		}
		spreads := !timed[e.id] && e.clearOnOverflow.String == "1"
		if spreads {
			spreading++
			var limit int64
			if _, err := fmt.Sscan(e.stackMax.String, &limit); err != nil || limit <= 0 {
				c.failSubject(fmt.Sprintf("status Buff %d", e.id), "status Buff %d of DamageId %d spreads on overflow but has no positive stack limit: its overflow cannot be told", e.id, e.damageID)
			}
		}
		if m.product == nil {
			continue
		}
		reading := m.product.NegativeStatusSettlement(e.id, e.damageID)
		switch {
		case !reading.Executes:
			c.failSubject(fmt.Sprintf("status Buff %d", e.id), "the product does not take status Buff %d as executing negative-status DamageId %d", e.id, e.damageID)
		case !reading.Known || reading.Periodic != timed[e.id] || reading.ExecutesOnCreation != (e.onCreation.String == "1") || reading.ClearsOnOverflow != (e.clearOnOverflow.String == "1") ||
			reading.Instant != isInstant || reading.SpreadsOnOverflow != spreads:
			c.failSubject(fmt.Sprintf("status Buff %d", e.id), "the product reads the settlement of status Buff %d differently from its Buff row", e.id)
		default:
			recognized++
		}
	}
	rows, err := m.db.Query(`SELECT b.id, CAST(p.value AS TEXT) FROM buff b JOIN buff_extra_effect_parameters p ON p.owner_id = b.id AND p.ordinal = 0
		WHERE CAST(b.extra_effect_id AS TEXT) = '67' ORDER BY CAST(b.id AS INTEGER)`)
	if err != nil {
		c.fail("the InvokePeriod Buffs cannot be read: %v", err)
		return
	}
	defer rows.Close()
	executing := map[uint64]uint64{}
	for _, e := range executors {
		executing[e.id] = e.damageID
	}
	invokes, named := 0, map[uint64]bool{}
	for rows.Next() {
		var rawID, value string
		if err := rows.Scan(&rawID, &value); err != nil {
			c.fail("an InvokePeriod Buff cannot be read: %v", err)
			return
		}
		forcesStatus := false
		for _, part := range strings.Split(value, "#") {
			var status uint64
			if _, err := fmt.Sscan(strings.TrimSpace(part), &status); err != nil {
				continue
			}
			if _, ok := executing[status]; !ok {
				continue
			}
			forcesStatus = true
			named[status] = true
			if !timed[status] {
				c.failSubject(fmt.Sprintf("InvokePeriod %s", rawID), "InvokePeriod %s names status Buff %d, which has no period: the credit has no rule for forcing it", rawID, status)
			}
		}
		if forcesStatus {
			invokes++
		}
	}
	if m.product == nil {
		c.unverified("the product loader did not load the Master")
	}
	c.Coverage = &Coverage{Passed: recognized, Total: len(executors), Denominator: "status Buffs that execute a FormulaType 9 DamageId, read the same by the product and their Buff row"}
	c.Evidence = append(c.Evidence, fmt.Sprintf("negative-status DamageIds %v; status Buffs %d (timed %d, without a period %d, of which instant %d and spreading on overflow %d; with every settlement column %d); InvokePeriods forcing a status %d, naming %d status Buffs",
		damageIDs, len(executors), periodic, applied, instant, spreading, complete, invokes, len(named)))
}

// relationRule is one lookup relation the product needs, stated as a query counting rows that break it.
type relationRule struct {
	name  string
	query string
}

var relationRules = []relationRule{
	{"trialroleinfo.parent_role_id -> roleinfo", `SELECT count(*) FROM trialroleinfo t WHERE t.parent_role_id IS NOT NULL AND t.parent_role_id NOT IN ('', '0') AND NOT EXISTS (SELECT 1 FROM roleinfo r WHERE r.id = t.parent_role_id)`},
	{"role_skill_group_ref.role_id -> roleinfo", `SELECT count(*) FROM role_skill_group_ref g WHERE NOT EXISTS (SELECT 1 FROM roleinfo r WHERE r.id = g.role_id)`},
	{"role_skill_group_ref.skill_group_id -> skill", `SELECT count(*) FROM role_skill_group_ref g WHERE NOT EXISTS (SELECT 1 FROM skill s WHERE s.skill_group_id = g.skill_group_id)`},
	{"playable roleinfo -> role_skill_group_ref", `SELECT count(*) FROM roleinfo r WHERE r.role_type = 1 AND NOT EXISTS (SELECT 1 FROM role_skill_group_ref g WHERE g.role_id = r.id)`},
	{"buff_gameplay_cue_ids.owner_id -> buff", `SELECT count(*) FROM buff_gameplay_cue_ids b WHERE NOT EXISTS (SELECT 1 FROM buff x WHERE x.id = b.owner_id)`},
	{"buff_gameplay_cue_ids.value -> gameplaycue", `SELECT count(*) FROM buff_gameplay_cue_ids b WHERE NOT EXISTS (SELECT 1 FROM gameplaycue g WHERE g.id = CAST(CAST(b.value AS INTEGER) AS TEXT))`},
	{"dungeondetection.secondary -> secondaryguidedata", `SELECT count(*) FROM dungeondetection d WHERE d.secondary IS NOT NULL AND d.secondary <> '' AND NOT EXISTS (SELECT 1 FROM secondaryguidedata s WHERE s.id = d.secondary)`},
	{"silentareadetection.secondary -> secondaryguidedata", `SELECT count(*) FROM silentareadetection d WHERE d.secondary IS NOT NULL AND d.secondary <> '' AND NOT EXISTS (SELECT 1 FROM secondaryguidedata s WHERE s.id = d.secondary)`},
	{"instancedungeonentrance_instance_ref.instance_dungeon_id -> instancedungeon", `SELECT count(*) FROM instancedungeonentrance_instance_ref r WHERE NOT EXISTS (SELECT 1 FROM instancedungeon i WHERE i.id = r.instance_dungeon_id)`},
}

func (g *gate) relationChecks(c *Check, m *masterView) {
	held := 0
	for _, rule := range relationRules {
		broken, err := m.count(rule.query)
		switch {
		case err != nil:
			c.fail("%s cannot be checked: %v", rule.name, err)
		case broken != 0:
			c.fail("%s: %d rows do not resolve", rule.name, broken)
		default:
			held++
		}
	}
	c.Coverage = &Coverage{Passed: held, Total: len(relationRules), Denominator: "required lookup relations"}
}

func (g *gate) roleNameCheck(c *Check, m *masterView) {
	named := 0
	for _, role := range m.playable {
		complete := true
		for _, locale := range masterLocales {
			n, err := m.count("SELECT count(*) FROM roleinfo_locale WHERE id = ? AND locale = ? AND trim(name) <> ''", strconv.FormatUint(role, 10), locale)
			if err != nil || n == 0 {
				c.fail("role %d has no %s name", role, locale)
				complete = false
			}
		}
		if complete {
			named++
		}
	}
	c.Coverage = &Coverage{Passed: named, Total: len(m.playable), Denominator: "playable roles named in all five Master locales"}
}

func (g *gate) artCheck(c *Check, m *masterView) {
	if g.manifest == nil {
		c.unverified("the image manifest cannot be read")
		return
	}
	complete := 0
	for _, role := range m.playable {
		ok := true
		for _, kind := range []string{"avatar", "portrait"} {
			if !g.manifest.has(fmt.Sprintf("game/characters/%d/%s.webp", role, kind)) {
				c.failSubject(fmt.Sprintf("role %d %s", role, kind), "role %d has no %s in the image pack", role, kind)
				ok = false
			}
		}
		if ok {
			complete++
		}
	}
	c.Coverage = &Coverage{Passed: complete, Total: len(m.playable), Denominator: "playable roles (roleinfo.role_type 1) with avatar and portrait"}
}

func (g *gate) trialCheck(c *Check, m *masterView) {
	if m.product == nil {
		c.unverified("the product loader did not load the Master")
		return
	}
	rows, err := m.db.Query(`SELECT t.id, t.parent_role_id FROM trialroleinfo t JOIN roleinfo r ON r.id = t.parent_role_id WHERE r.role_type = 1 ORDER BY CAST(t.id AS INTEGER)`)
	if err != nil {
		c.fail("trial roles cannot be read: %v", err)
		return
	}
	type trialRow struct{ trial, parent uint64 }
	var trials []trialRow
	for rows.Next() {
		var trialRaw, parentRaw string
		if err := rows.Scan(&trialRaw, &parentRaw); err != nil {
			c.fail("trial roles cannot be read: %v", err)
			break
		}
		trialID, err1 := strconv.ParseUint(trialRaw, 10, 64)
		parentID, err2 := strconv.ParseUint(parentRaw, 10, 64)
		if err1 != nil || err2 != nil {
			c.fail("trial role %q has an unreadable id or parent", trialRaw)
			continue
		}
		trials = append(trials, trialRow{trialID, parentID})
	}
	rows.Close()
	resolver := m.product.DisplayResolver()
	ctx := context.Background()
	resolved := 0
	for _, t := range trials {
		if got := resolver.StaticRole(ctx, t.trial); got != t.parent {
			c.fail("trial role %d resolves to %d, its parent is %d", t.trial, got, t.parent)
			continue
		}
		if g.manifest != nil && !g.manifest.has(fmt.Sprintf("game/characters/%d/avatar.webp", t.parent)) {
			c.fail("trial role %d: its parent %d has no avatar", t.trial, t.parent)
			continue
		}
		resolved++
	}
	c.Coverage = &Coverage{Passed: resolved, Total: len(trials), Denominator: "trial roles of playable roles resolved to their parent through capture.DisplayResolver.StaticRole"}
}

func (g *gate) skillChecks(icons, names *Check, m *masterView) {
	total, withIcon := 0, 0
	roleSkills := map[uint64][]uint64{}
	for _, role := range m.playable {
		skills, err := m.ids(`SELECT s.id FROM skill s JOIN role_skill_group_ref g ON g.skill_group_id = s.skill_group_id WHERE g.role_id = ? ORDER BY CAST(s.id AS INTEGER)`, strconv.FormatUint(role, 10))
		if err != nil {
			icons.fail("role %d: UI skills cannot be read: %v", role, err)
			continue
		}
		roleSkills[role] = skills
		if len(skills) == 0 {
			icons.fail("role %d has no UI skill", role)
		}
		for _, skill := range skills {
			total++
			if g.manifest == nil {
				continue
			}
			if g.manifest.has(fmt.Sprintf("game/skills/by-config/%d/icon.webp", skill)) {
				withIcon++
			} else {
				icons.failSubject(fmt.Sprintf("skill %d", skill), "role %d UI skill %d has no icon (skills/by-config)", role, skill)
			}
		}
	}
	if g.manifest == nil {
		icons.unverified("the image manifest cannot be read")
	}
	icons.Coverage = &Coverage{Passed: withIcon, Total: total, Denominator: "UI skills (skill via role_skill_group_ref) of playable roles"}

	// A role passes when it names at least one of its UI skills in every locale: a new role whose text the client has not
	// filled in yet (as before a hot patch) names none.
	perLocale := map[string]int{}
	passedRoles := 0
	for _, role := range m.playable {
		ok := len(roleSkills[role]) > 0
		for _, locale := range masterLocales {
			named := 0
			for _, skill := range roleSkills[role] {
				if n, err := m.count("SELECT count(*) FROM skill_locale WHERE id = ? AND locale = ? AND trim(name) <> ''", strconv.FormatUint(skill, 10), locale); err == nil && n > 0 {
					named++
				}
			}
			perLocale[locale] += named
			if len(roleSkills[role]) > 0 && named == 0 {
				names.fail("role %d names none of its %d UI skills in %s", role, len(roleSkills[role]), locale)
				ok = false
			}
		}
		if ok {
			passedRoles++
		}
	}
	for _, locale := range masterLocales {
		names.Evidence = append(names.Evidence, fmt.Sprintf("%s: %d of %d UI skills named", locale, perLocale[locale], total))
	}
	names.Coverage = &Coverage{Passed: passedRoles, Total: len(m.playable), Denominator: "playable roles naming at least one UI skill in every Master locale"}
}

// hudBuffs lists the buffs whose definition carries a GameplayCue the client HUD draws (cue type 2, 14, 24 or 37 with a
// Path), with the direction parameter of the first such cue in list order ("" when the cue has none).
func (m *masterView) hudBuffs() (map[uint64]string, error) {
	types := make([]string, len(hudCueTypes))
	for i, t := range hudCueTypes {
		types[i] = strconv.Itoa(t)
	}
	rows, err := m.db.Query(`SELECT b.owner_id, b.ordinal, coalesce((SELECT p.value FROM gameplaycue_parameters p WHERE p.owner_id = g.id AND p.ordinal = 0), '')
		FROM buff_gameplay_cue_ids b JOIN gameplaycue g ON g.id = CAST(CAST(b.value AS INTEGER) AS TEXT)
		WHERE g.path_asset_id IS NOT NULL AND g.path_asset_id <> '' AND g.cue_type IN (` + strings.Join(types, ",") + `)
		ORDER BY b.owner_id, b.ordinal`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint64]string{}
	for rows.Next() {
		var raw, direction string
		var ordinal int
		if err := rows.Scan(&raw, &ordinal, &direction); err != nil {
			return nil, err
		}
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("buff id %q: %w", raw, err)
		}
		if _, seen := out[id]; !seen {
			out[id] = direction
		}
	}
	return out, rows.Err()
}

func (g *gate) hudChecks(class, icons *Check, m *masterView) {
	buffs, err := m.hudBuffs()
	if err != nil {
		class.fail("the HUD cue definitions cannot be read: %v", err)
		icons.unverified("the HUD cue definitions cannot be read")
		return
	}
	ids := make([]uint64, 0, len(buffs))
	for id := range buffs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if m.product == nil {
		class.unverified("the product loader did not load the Master")
	} else {
		byPolarity := map[capture.EffectPolarity]int{}
		classified := 0
		for _, id := range ids {
			definition := g.in.Classify(m.product, id)
			switch {
			case !definition.HUDIcon:
				class.failSubject(fmt.Sprintf("effect %d", id), "effect %d: the client HUD draws it (cue direction %q), the product says it has no HUD icon", id, buffs[id])
			case definition.Polarity != capture.EffectPolarityBuff && definition.Polarity != capture.EffectPolarityDebuff && definition.Polarity != capture.EffectPolarityNeutral:
				class.failSubject(fmt.Sprintf("effect %d", id), "effect %d: the client HUD draws it (cue direction %q), the product classifies it %q, which no view lists", id, buffs[id], definition.Polarity)
			default:
				want := capture.EffectPolarityNeutral
				switch buffs[id] {
				case "1":
					want = capture.EffectPolarityBuff
				case "2":
					want = capture.EffectPolarityDebuff
				}
				if definition.Polarity != want {
					class.failSubject(fmt.Sprintf("effect %d", id), "effect %d: the client draws it as %s (cue direction %q), the product as %s", id, want, buffs[id], definition.Polarity)
					continue
				}
				classified++
				byPolarity[definition.Polarity]++
			}
		}
		class.Coverage = &Coverage{Passed: classified, Total: len(ids), Denominator: "buffs with a HUD GameplayCue (type 2/14/24/37 with a Path)"}
		class.Evidence = append(class.Evidence, fmt.Sprintf("buff %d, debuff %d, neutral %d", byPolarity[capture.EffectPolarityBuff], byPolarity[capture.EffectPolarityDebuff], byPolarity[capture.EffectPolarityNeutral]))
	}
	if g.manifest == nil {
		icons.unverified("the image manifest cannot be read")
		return
	}
	present := 0
	for _, id := range ids {
		if g.manifest.has(fmt.Sprintf("game/buffs/%d/icon.webp", id)) {
			present++
		} else {
			icons.failSubject(fmt.Sprintf("effect %d", id), "effect %d is drawn by the HUD but has no icon in the image pack", id)
		}
	}
	icons.Coverage = &Coverage{Passed: present, Total: len(ids), Denominator: "buffs with a HUD GameplayCue"}
}

func (g *gate) contentChecks(categories, modes, unresolved *Check, m *masterView) {
	used, err := m.ids(`SELECT DISTINCT secondary FROM (SELECT secondary FROM dungeondetection UNION SELECT secondary FROM silentareadetection) WHERE secondary IS NOT NULL AND secondary <> '' ORDER BY CAST(secondary AS INTEGER)`)
	if err != nil {
		categories.fail("the Adventure Guide detection rows cannot be read: %v", err)
		modes.unverified("the Adventure Guide detection rows cannot be read")
		return
	}
	named := 0
	for _, category := range used {
		ok := true
		for _, locale := range masterLocales {
			n, err := m.count("SELECT count(*) FROM secondaryguidedata_locale WHERE id = ? AND locale = ? AND trim(text) <> ''", strconv.FormatUint(category, 10), locale)
			if err != nil || n == 0 {
				categories.fail("guide category %d has no %s name", category, locale)
				ok = false
			}
		}
		if ok {
			named++
		}
	}
	categories.Coverage = &Coverage{Passed: named, Total: len(used), Denominator: "guide categories used by DungeonDetection or SilentAreaDetection rows"}

	if m.product == nil {
		modes.unverified("the product loader did not load the Master")
		return
	}
	// Which categories the product reaches: every instance through the product's resolver, by category name.
	nameOf := map[string]uint64{}
	for _, category := range used {
		var text string
		if err := m.db.QueryRow("SELECT text FROM secondaryguidedata_locale WHERE id = ? AND locale = 'en-US'", strconv.FormatUint(category, 10)).Scan(&text); err == nil {
			nameOf[text] = category
		}
	}
	instances, err := m.ids("SELECT id FROM instancedungeon ORDER BY CAST(id AS INTEGER)")
	if err != nil {
		modes.fail("instances cannot be read: %v", err)
		return
	}
	resolver := m.product.DisplayResolver()
	ctx := context.Background()
	reached := map[uint64]int{}
	openWorld, unknown := 0, 0
	for _, id := range instances {
		mode, err := resolver.InstanceMode(ctx, id, "en-US")
		if err != nil {
			modes.fail("instance %d: %v", id, err)
			continue
		}
		switch mode.Kind {
		case capture.InstanceModeOpenWorld:
			openWorld++
		case capture.InstanceModeCategory:
			if category, ok := nameOf[mode.Name]; ok {
				reached[category]++
			}
		default:
			unknown++
		}
	}
	// The client relations of a category: the entrances its rows name (DungeonId, InstanceDungeonEntranceId) and the
	// instances (SubDungeonId, SilentAreaDetection.DungeonId), with the ShipTower stages for category 28. A relation another
	// category's rows also use cannot tell the two apart, so only an exclusive one obliges the resolver.
	withRelation, err := m.ids(`WITH ` + categoryRelations + ` SELECT DISTINCT cat FROM rel ORDER BY CAST(cat AS INTEGER)`)
	if err != nil {
		modes.fail("the detection rows' instance relations cannot be read: %v", err)
		return
	}
	resolvedCategories := 0
	for _, category := range withRelation {
		if reached[category] > 0 {
			resolvedCategories++
			continue
		}
		exclusive, err := m.strings(`WITH `+categoryRelations+` SELECT k FROM rel WHERE cat = ? AND k NOT IN (SELECT k FROM rel WHERE cat <> ?) ORDER BY k`, strconv.FormatUint(category, 10), strconv.FormatUint(category, 10))
		switch {
		case err != nil:
			modes.fail("guide category %d: its relations cannot be read: %v", category, err)
		case len(exclusive) > 0:
			modes.fail("guide category %d reaches instances only it names (%s), but the product's resolver gives none of them this mode", category, strings.Join(exclusive, ", "))
		default:
			shared, _ := m.strings(`WITH `+categoryRelations+` SELECT k FROM rel WHERE cat = ? ORDER BY k`, strconv.FormatUint(category, 10))
			unresolved.Status = StatusUnverified
			unresolved.Findings = append(unresolved.Findings, fmt.Sprintf("guide category %d: every relation (%s) is shared with another category, so the client data cannot place an instance in it", category, strings.Join(shared, ", ")))
			resolvedCategories++
		}
	}
	if unresolved.Status == StatusUnverified {
		unresolved.Reason = "content the client data names ambiguously stays UNKNOWN or takes the other category's CORRELATED mode (research/topics/output/combat-history.md section 17)"
	}
	modes.Coverage = &Coverage{Passed: resolvedCategories, Total: len(withRelation), Denominator: "guide categories whose detection rows reach an instance (resolved, or ambiguous in the client data)"}
	modes.Evidence = append(modes.Evidence, fmt.Sprintf("%d instances: %d open world, %d in a category, %d without a single category (ambiguous or not offered by any guide row)", len(instances), openWorld, sumValues(reached), unknown))
}

// categoryRelations is a WITH clause naming each guide category's client relations to entrances and instances that exist
// in the Master, as rel(cat, k).
const categoryRelations = `rel(cat, k) AS (
	SELECT secondary, 'entrance ' || dungeon_id FROM dungeondetection WHERE dungeon_id IN (SELECT entrance_id FROM instancedungeonentrance_instance_ref)
	UNION SELECT secondary, 'entrance ' || instance_dungeon_entrance_id FROM dungeondetection WHERE instance_dungeon_entrance_id IN (SELECT entrance_id FROM instancedungeonentrance_instance_ref)
	UNION SELECT secondary, 'instance ' || sub_dungeon_id FROM dungeondetection WHERE sub_dungeon_id IN (SELECT id FROM instancedungeon)
	UNION SELECT secondary, 'entrance ' || instance_dungeon_entrance_id FROM silentareadetection WHERE instance_dungeon_entrance_id IN (SELECT entrance_id FROM instancedungeonentrance_instance_ref)
	UNION SELECT secondary, 'instance ' || dungeon_id FROM silentareadetection WHERE dungeon_id IN (SELECT id FROM instancedungeon)
	UNION SELECT '28', 'ship tower stage ' || inst_id FROM slashtowerstageinfo WHERE inst_id IN (SELECT id FROM instancedungeon)
	UNION SELECT '28', 'ship tower stage ' || instance_dungeon_id FROM slashandtowercfg_instance_ref WHERE instance_dungeon_id IN (SELECT id FROM instancedungeon)
)`

func (m *masterView) strings(query string, args ...any) ([]string, error) {
	rows, err := m.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func sumValues(m map[uint64]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}
