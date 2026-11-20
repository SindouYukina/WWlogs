package capture

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
)

// The content model of the Master, for display only. content_context lists the static contexts of an instance in a content
// system: one row for each context the game's data allows, with one column per dimension (activity, season, cycle, series,
// stage, difficulty, mode) naming an entry of the same system and dimension, and the trial character of a role trial.
// content_system names a system by its Adventure Guide category, or says the game names no system; content_entry says
// whether an entry has official names (content_entry_locale), is approved to have none (NO_OFFICIAL_NAME), or has a name
// source not decided yet (UNRESOLVED). A dimension a context leaves NULL is not modelled for that context; it never means
// the game has none.
//
// Several contexts are choices the static data cannot fix: a cycle, a season, a boss and a difficulty. The runtime has no
// evidence of which one an encounter was, so it chooses none: a dimension is named only when every context that models it
// names the same thing (one entry, or entries with the same official name in the locale). Otherwise it is
// RUNTIME_DEPENDENT, with the number of candidates. Every name is the locale's official text, else English, and never a
// name of another dimension or of the system.

// ContentLayerState says what the content model gives one dimension of an instance.
type ContentLayerState string

const (
	// ContentLayerOfficial: every context names the same thing, with an official name in the locale or English.
	ContentLayerOfficial ContentLayerState = "OFFICIAL"
	// ContentLayerNoOfficialName: approved: the game gives this element no name.
	ContentLayerNoOfficialName ContentLayerState = "NO_OFFICIAL_NAME"
	// ContentLayerUnresolved: the element's name source is not decided; it is not known to have no name.
	ContentLayerUnresolved ContentLayerState = "UNRESOLVED"
	// ContentLayerNameMissing: the element has official names, but none in the locale or English.
	ContentLayerNameMissing ContentLayerState = "NAME_MISSING"
	// ContentLayerRuntimeDependent: the contexts name different things and the runtime cannot tell which one applies.
	ContentLayerRuntimeDependent ContentLayerState = "RUNTIME_DEPENDENT"
)

// contentDimensions are the dimension columns of content_context in display order, then the trial character.
var contentDimensions = []string{"activity", "season", "cycle", "series", "stage", "difficulty", "mode"}

// contentRoleDimension is the layer of the trial character (content_context.role_id), named by the role's own name.
const contentRoleDimension = "role"

// ContentLayer is one dimension of an instance's content in a locale.
type ContentLayer struct {
	Dimension string            `json:"dimension"`
	State     ContentLayerState `json:"state"`
	Name      string            `json:"name,omitempty"`
	Locale    string            `json:"locale,omitempty"`
	// Candidates is the number of different entries (or characters) a RUNTIME_DEPENDENT layer leaves open.
	Candidates int `json:"candidates,omitempty"`
	// RoleID is the RoleInfo id of the role layer's character, when there is one.
	RoleID uint64 `json:"roleId,omitempty"`
}

// InstanceContent is what the content model says about one instance in a locale.
type InstanceContent struct {
	// System is the content system that lists the instance; empty when none or several do.
	System string `json:"system,omitempty"`
	// Status is VERIFIED (one system lists the instance), AMBIGUOUS (several do, and none is chosen) or UNKNOWN (none).
	Status string `json:"status"`
	// Systems are the systems of an AMBIGUOUS instance.
	Systems []string `json:"systems,omitempty"`
	// Contexts is the number of static contexts of the instance in its system.
	Contexts int `json:"contexts,omitempty"`
	// Layers are the modelled dimensions in display order (contentDimensions, then the role).
	Layers []ContentLayer `json:"layers,omitempty"`
}

var errContentModel = errors.New("Master content tables could not be read")

// contentSystemRow is one content_system row.
type contentSystemRow struct {
	id            string
	guideCategory string
	nameStatus    string
}

// contentSystemsLocked lists the content systems whose contexts name an instance, in id order.
func (r *DisplayResolver) contentSystemsLocked(ctx context.Context, id string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT DISTINCT system_id FROM content_context WHERE instance_id = ? ORDER BY system_id", id)
	if err != nil {
		return nil, errContentModel
	}
	defer rows.Close()
	var systems []string
	for rows.Next() {
		var system string
		if err := rows.Scan(&system); err != nil {
			return nil, errContentModel
		}
		systems = append(systems, system)
	}
	if rows.Err() != nil {
		return nil, errContentModel
	}
	return systems, nil
}

func (r *DisplayResolver) contentSystemLocked(ctx context.Context, system string) (contentSystemRow, error) {
	row := contentSystemRow{id: system}
	var category sql.NullString
	if err := r.db.QueryRowContext(ctx, "SELECT guide_category_id, name_status FROM content_system WHERE id = ?", system).Scan(&category, &row.nameStatus); err != nil {
		// content_context references content_system; a context without its system is a broken Master, not UNKNOWN content.
		return row, errContentModel
	}
	row.guideCategory = category.String
	return row, nil
}

// InstanceContent returns the content model's layers of an instance in a locale (English as the fallback name).
func (r *DisplayResolver) InstanceContent(ctx context.Context, instanceID uint64, locale string) (InstanceContent, error) {
	key := "content:" + strconv.FormatUint(instanceID, 10) + ":" + locale
	r.mu.Lock()
	defer r.mu.Unlock()
	if content, ok := r.contents.get(key); ok {
		return content, nil
	}
	content, err := r.instanceContentLocked(ctx, instanceID, locale)
	if err != nil {
		return InstanceContent{Status: "UNKNOWN"}, err
	}
	r.contents.put(key, content)
	return content, nil
}

// contentEntry is one content_entry with its display name in the asked locale or English.
type contentEntry struct {
	status string
	name   string
	locale string
}

func (r *DisplayResolver) instanceContentLocked(ctx context.Context, instanceID uint64, locale string) (InstanceContent, error) {
	unknown := InstanceContent{Status: "UNKNOWN"}
	if instanceID == 0 {
		return unknown, nil
	}
	id := strconv.FormatUint(instanceID, 10)
	systems, err := r.contentSystemsLocked(ctx, id)
	if err != nil {
		return unknown, err
	}
	switch len(systems) {
	case 0:
		return unknown, nil
	case 1:
	default:
		return InstanceContent{Status: "AMBIGUOUS", Systems: systems}, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT activity, season, cycle, series, stage, difficulty, mode, role_id FROM content_context
		WHERE instance_id = ? AND system_id = ? ORDER BY ordinal`, id, systems[0])
	if err != nil {
		return unknown, errContentModel
	}
	values := make([]map[string]bool, len(contentDimensions)+1)
	for i := range values {
		values[i] = map[string]bool{}
	}
	contexts := 0
	for rows.Next() {
		columns := make([]sql.NullString, len(values))
		targets := make([]any, len(columns))
		for i := range columns {
			targets[i] = &columns[i]
		}
		if err := rows.Scan(targets...); err != nil {
			rows.Close()
			return unknown, errContentModel
		}
		contexts++
		for i, column := range columns {
			if column.Valid && column.String != "" {
				values[i][column.String] = true
			}
		}
	}
	if err := rows.Close(); err != nil {
		return unknown, errContentModel
	}
	content := InstanceContent{System: systems[0], Status: "VERIFIED", Contexts: contexts}
	for i, dimension := range contentDimensions {
		if len(values[i]) == 0 {
			continue
		}
		entries := make([]contentEntry, 0, len(values[i]))
		for _, entryID := range sortedKeys(values[i]) {
			entry, err := r.contentEntryLocked(ctx, entryID, locale)
			if err != nil {
				return unknown, err
			}
			entries = append(entries, entry)
		}
		content.Layers = append(content.Layers, contentLayer(dimension, entries))
	}
	if roles := values[len(contentDimensions)]; len(roles) > 0 {
		layer, err := r.contentRoleLayerLocked(ctx, sortedKeys(roles), locale)
		if err != nil {
			return unknown, err
		}
		content.Layers = append(content.Layers, layer)
	}
	return content, nil
}

// contentEntryLocked reads an entry's name state and, for an official one, its name in the locale or English.
func (r *DisplayResolver) contentEntryLocked(ctx context.Context, entryID, locale string) (contentEntry, error) {
	var entry contentEntry
	if err := r.db.QueryRowContext(ctx, "SELECT name_status FROM content_entry WHERE id = ?", entryID).Scan(&entry.status); err != nil {
		// content_context references content_entry; an entry that cannot be read is a broken Master.
		return entry, errContentModel
	}
	if entry.status != string(ContentLayerOfficial) {
		return entry, nil
	}
	for _, candidate := range []string{locale, "en-US"} {
		var name string
		err := r.db.QueryRowContext(ctx, "SELECT name FROM content_entry_locale WHERE id = ? AND locale = ?", entryID, candidate).Scan(&name)
		if err != nil && err != sql.ErrNoRows {
			return entry, errContentModel
		}
		if err == nil && masterOfficialText(name) {
			if plain := masterPlainText(name); plain != "" {
				entry.name, entry.locale = plain, candidate
				break
			}
		}
	}
	return entry, nil
}

// contentLayer combines the entries the contexts name for one dimension (see the package comment above).
func contentLayer(dimension string, entries []contentEntry) ContentLayer {
	layer := ContentLayer{Dimension: dimension}
	first := entries[0]
	same := true
	for _, entry := range entries[1:] {
		same = same && entry == first
	}
	switch {
	case !same:
		layer.State, layer.Candidates = ContentLayerRuntimeDependent, len(entries)
	case first.status == string(ContentLayerOfficial) && first.name == "":
		layer.State = ContentLayerNameMissing
	case first.status == string(ContentLayerOfficial):
		layer.State, layer.Name, layer.Locale = ContentLayerOfficial, first.name, first.locale
	case first.status == string(ContentLayerNoOfficialName), first.status == string(ContentLayerUnresolved):
		layer.State = ContentLayerState(first.status)
	default:
		// A state this runtime does not know is not taken for a name.
		layer.State = ContentLayerUnresolved
	}
	return layer
}

// contentRoleLayerLocked names the trial character of the contexts by its own role name (Resolve "role").
func (r *DisplayResolver) contentRoleLayerLocked(ctx context.Context, roles []string, locale string) (ContentLayer, error) {
	entries := make([]contentEntry, 0, len(roles))
	var roleID uint64
	for _, raw := range roles {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return ContentLayer{}, errContentModel
		}
		roleID = id
		name, err := r.resolveLocked(ctx, "role", id, locale)
		if err != nil {
			return ContentLayer{}, err
		}
		entries = append(entries, contentEntry{status: string(ContentLayerOfficial), name: name.Name, locale: name.Locale})
	}
	layer := contentLayer(contentRoleDimension, entries)
	if len(roles) == 1 {
		layer.RoleID = roleID
	}
	return layer, nil
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
