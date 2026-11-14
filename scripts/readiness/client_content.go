package readiness

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
)

// clientContent is the research input clientContent (PROCEDURE.md S5): what the current client defines, read from its own
// ConfigDB files (source) and its decoded snapshot (extraction), independently of the Master
// (work/version-update/tools/client_content_inventory.py). The gate's other Master checks walk the Master's own rows; this
// one is the only place a row the Master build dropped is counted.
type clientContent struct {
	Client string                        `json:"client"`
	Tables map[string]clientContentTable `json:"tables"`
	// PlayableRoles are RoleInfo rows with RoleType 1 in the decoded snapshot; HUDBuffs the buffs whose GameplayCueIds
	// name a GameplayCue the HUD draws (CueType 2, 14, 24 or 37 with a Path).
	PlayableRoles []string `json:"playableRoles"`
	HUDBuffs      []string `json:"hudBuffs"`
	Exclusions    []struct {
		Table  string `json:"table"`
		ID     string `json:"id"`
		Reason string `json:"reason"`
	} `json:"exclusions"`
}

type clientContentTable struct {
	SourceRows int `json:"sourceRows"`
	// DecodedRows is nil for a table the decoded snapshot does not hold.
	DecodedRows *int     `json:"decodedRows"`
	IDs         []string `json:"ids"`
}

// clientContentTables are the client tables the Master keeps whole; each must hold every client row.
var clientContentTables = []string{"roleinfo", "trialroleinfo", "buff", "gameplaycue", "skill", "secondaryguidedata", "dungeondetection", "silentareadetection", "instancedungeon"}

func (g *gate) clientContentCheck(c *Check, m *masterView) {
	if g.in.ClientContent == "" {
		c.unverified("no client content inventory was supplied (research input clientContent; PROCEDURE.md S5)")
		return
	}
	data, err := os.ReadFile(g.in.ClientContent)
	if err != nil {
		c.unverified(fmt.Sprintf("the client content inventory cannot be read: %v", err))
		return
	}
	var inventory clientContent
	if err := json.Unmarshal(data, &inventory); err != nil {
		c.fail("the client content inventory cannot be parsed: %v", err)
		return
	}
	switch {
	case g.source == nil || g.source.protocolClient == "":
		c.unverified("the decoded protocol client is unknown, so the inventory's client cannot be compared")
	case inventory.Client != g.source.protocolClient:
		c.fail("the inventory describes client %q, the decoder reads %q: extract the inventory and the Master from the same client (PROCEDURE.md S5)", inventory.Client, g.source.protocolClient)
	}
	excluded := map[string]map[string]bool{}
	for _, exclusion := range inventory.Exclusions {
		if exclusion.Reason == "" {
			c.fail("the exclusion of %s %s states no reason", exclusion.Table, exclusion.ID)
			continue
		}
		if excluded[exclusion.Table] == nil {
			excluded[exclusion.Table] = map[string]bool{}
		}
		excluded[exclusion.Table][exclusion.ID] = true
	}
	matched, total := 0, 0
	for _, table := range clientContentTables {
		entry, ok := inventory.Tables[table]
		if !ok {
			c.fail("the inventory has no %s table", table)
			continue
		}
		if len(entry.IDs) != entry.SourceRows {
			c.fail("%s: the inventory lists %d ids for %d source rows", table, len(entry.IDs), entry.SourceRows)
		}
		if entry.DecodedRows != nil && *entry.DecodedRows != entry.SourceRows {
			c.fail("%s: %d of the client's %d rows decoded in the extraction snapshot", table, *entry.DecodedRows, entry.SourceRows)
		}
		master, err := m.stringSet("SELECT CAST(id AS TEXT) FROM " + table)
		if err != nil {
			c.fail("%s cannot be read from the Master: %v", table, err)
			continue
		}
		var missing []string
		for _, id := range entry.IDs {
			total++
			if master[id] || excluded[table][id] {
				matched++
				continue
			}
			missing = append(missing, id)
		}
		reportIDs(c, missing, "%s %s is in the client but not in the Master", table)
	}
	playable, err := m.stringSet("SELECT CAST(id AS TEXT) FROM roleinfo WHERE role_type = 1")
	if err != nil {
		c.fail("the Master's playable roles cannot be read: %v", err)
	} else {
		compareSets(c, inventory.PlayableRoles, playable, "playable role %s of the client is not playable in the Master", "role %s is playable in the Master but not in the client")
	}
	hud, err := m.hudBuffs()
	if err != nil {
		c.fail("the Master's HUD cue relations cannot be read: %v", err)
	} else {
		masterHUD := make(map[string]bool, len(hud))
		for id := range hud {
			masterHUD[strconv.FormatUint(id, 10)] = true
		}
		compareSets(c, inventory.HUDBuffs, masterHUD, "the client HUD draws buff %s but the Master gives it no HUD cue", "the Master gives buff %s a HUD cue the client does not draw")
	}
	c.Coverage = &Coverage{Passed: matched, Total: total, Denominator: "client ConfigDB rows of the tables the Master keeps whole"}
	c.Evidence = append(c.Evidence, fmt.Sprintf("inventory %s: %d client rows in %d tables, %d playable roles, %d HUD buffs, %d explicit exclusions",
		inventory.Client, total, len(clientContentTables), len(inventory.PlayableRoles), len(inventory.HUDBuffs), len(inventory.Exclusions)))
}

// compareSets fails on every client id the Master lacks and every Master id the client lacks.
func compareSets(c *Check, client []string, master map[string]bool, missingFormat, extraFormat string) {
	clientSet := make(map[string]bool, len(client))
	var missing []string
	for _, id := range client {
		clientSet[id] = true
		if !master[id] {
			missing = append(missing, id)
		}
	}
	var extra []string
	for id := range master {
		if !clientSet[id] {
			extra = append(extra, id)
		}
	}
	reportIDs(c, missing, missingFormat)
	reportIDs(c, extra, extraFormat)
}

// reportIDs fails once per id, in numeric order, naming at most the first maxReportedIDs and then the count.
func reportIDs(c *Check, ids []string, format string, prefix ...any) {
	const maxReportedIDs = 20
	sort.Slice(ids, func(i, j int) bool {
		a, errA := strconv.ParseUint(ids[i], 10, 64)
		b, errB := strconv.ParseUint(ids[j], 10, 64)
		if errA == nil && errB == nil {
			return a < b
		}
		return ids[i] < ids[j]
	})
	for i, id := range ids {
		if i == maxReportedIDs {
			c.fail("and %d more", len(ids)-maxReportedIDs)
			return
		}
		c.fail(format, append(append([]any{}, prefix...), id)...)
	}
}

// stringSet runs a one-column query and returns its values.
func (m *masterView) stringSet(query string, args ...any) (map[string]bool, error) {
	values, err := m.strings(query, args...)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out, nil
}
