package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"wwlogs/internal/capture"
	"wwlogs/internal/history"
	"wwlogs/internal/service"
)

// Combat History in the host: one Store for the run, a Recorder for each capture session that keeps history, and the
// Bridge queries of the History view. Queries read the Store on the caller's goroutine and never touch a running capture.

// historyQueryLimit bounds one History query, including its Master name lookups.
const historyQueryLimit = 5 * time.Second

// openHistory opens the run's history file: the explicit Development test file when one was given, otherwise the user's own
// under %LOCALAPPDATA%. A history that cannot be opened is reported and leaves the application running without one.
func openHistory(path string, masters *capture.MasterData) *history.Store {
	if path == "" {
		var err error
		if path, err = history.DefaultPath(); err != nil {
			fmt.Fprintf(os.Stderr, "WWlogs desktop: combat history is unavailable: %v\n", err)
		}
	}
	store, err := history.Open(history.Options{Path: path, MasterSHA256: masters.SHA256()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "WWlogs desktop: combat history: %v\n", err)
	}
	return store
}

// historyStarter begins a history session for each capture. Live captures are recorded. A replay is recorded only into an
// explicitly supplied Development history file, never into the user's own history, and the same file only once. A capture
// that starts with recording off still gets a recorder, without any session row, so switching recording on during it can
// start a span. recorders follows the running capture's recorder.
func historyStarter(store *history.Store, recordReplays bool, recorders *recorderTracker) service.HistoryStarter {
	return func(cfg capture.SessionConfig) (service.HistoryRecorder, string, error) {
		recorders.set(nil)
		start := history.SessionStart{Source: history.SourceLive, Level: history.Level(), Paused: cfg.HistoryPaused}
		if cfg.ReplayPath != "" {
			if !recordReplays {
				return nil, "", nil
			}
			digest, err := fileSHA256(cfg.ReplayPath)
			if err != nil {
				return nil, service.HistoryUnavailable, err
			}
			start.Source, start.SourceSHA256 = history.SourceReplay, digest
		}
		recorder, err := store.BeginSession(start)
		switch {
		case errors.Is(err, history.ErrDuplicateSource):
			return nil, service.HistoryDuplicate, err
		case err != nil:
			return nil, service.HistoryUnavailable, err
		}
		recorders.set(recorder)
		return recorder, "", nil
	}
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read replay file: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("read replay file: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// historyName is a Master name with its evidence for the History view. A monster or scene name comes through a versioned
// Master link from an observed ID, so it is at most CORRELATED; an ID without a name stays UNKNOWN.
type historyName struct {
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
}

func (n *nameProjection) linked(kind string, id uint64) historyName {
	if id == 0 {
		return historyName{Status: "UNKNOWN"}
	}
	name := n.resolve(kind, id)
	if name.Name == "" {
		return historyName{Status: "UNKNOWN"}
	}
	return historyName{Name: name.Name, Status: "CORRELATED"}
}

type historySummaryJSON struct {
	history.EncounterSummary
	SceneName     historyName `json:"sceneName"`
	MainEnemyName historyName `json:"mainEnemyName"`
}

func (n *nameProjection) summary(summary history.EncounterSummary) historySummaryJSON {
	out := historySummaryJSON{EncounterSummary: summary, SceneName: historyName{Status: "UNKNOWN"}, MainEnemyName: historyName{Status: "UNKNOWN"}}
	if summary.Scene.Known {
		out.SceneName = n.linked("instance", summary.Scene.InstanceID)
	}
	if enemy := summary.MainEnemy; enemy != nil && !enemy.IdentityConflict {
		out.MainEnemyName = n.enemy(enemy.MonsterID, enemy.TemplateID)
	}
	return out
}

// enemy names an enemy instance by its MonsterInfo link, otherwise by its TemplateConfig link.
func (n *nameProjection) enemy(monsterID, templateID uint64) historyName {
	if monsterID != 0 {
		if name := n.linked("monster", monsterID); name.Name != "" {
			return name
		}
	}
	return n.linked("template", templateID)
}

type historyEnemyJSON struct {
	Ordinal          int         `json:"ordinal"`
	MonsterID        uint64      `json:"monsterId,omitempty"`
	TemplateID       uint64      `json:"templateId,omitempty"`
	IdentityConflict bool        `json:"identityConflict,omitempty"`
	Name             historyName `json:"name"`
	DamageTaken      int64       `json:"damageTaken"`
	HitsTaken        uint64      `json:"hitsTaken"`
	DamageDealt      int64       `json:"damageDealt"`
	HitsDealt        uint64      `json:"hitsDealt"`
}

// historyQuery prepares the names of one History query in the UI locale.
func (b *Bridge) historyQuery() (*nameProjection, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), historyQueryLimit)
	locale := b.catalog.Languages[b.settings.current().Settings.Locale].MasterLocale
	return &nameProjection{ctx: ctx, resolver: b.projection.resolver, locale: locale}, cancel
}

// historyZoneJSON says which zone a History answer laid its dates out in: the IANA zone the view asked for, or Local when
// that zone could not be loaded (fallback), with the zone's UTC offset now.
type historyZoneJSON struct {
	ID            string `json:"id"`
	Fallback      bool   `json:"fallback,omitempty"`
	OffsetSeconds int    `json:"offsetSeconds"`
}

func zoneJSON(zone history.ViewZone) historyZoneJSON {
	_, offset := time.Now().In(zone.Location).Zone()
	return historyZoneJSON{ID: zone.ID, Fallback: zone.Fallback, OffsetSeconds: offset}
}

// HistoryYear returns the encounter count of every date of year in the view's time zone (an IANA name; year 0 is the
// current year there), with the history's status.
func (b *Bridge) HistoryYear(year int, zoneName string) (json.RawMessage, error) {
	names, cancel := b.historyQuery()
	defer cancel()
	zone := history.ResolveViewZone(zoneName)
	view, err := b.history.Year(names.ctx, year, zone.Location)
	if err != nil && !errors.Is(err, history.ErrUnavailable) {
		return nil, err
	}
	return json.Marshal(struct {
		Status history.Status  `json:"status"`
		Zone   historyZoneJSON `json:"zone"`
		history.YearView
	}{b.history.Status(), zoneJSON(zone), view})
}

// HistoryDay returns one page of the encounters of a date in the view's time zone, named in the UI locale.
func (b *Bridge) HistoryDay(date string, offset int, zoneName string) (json.RawMessage, error) {
	names, cancel := b.historyQuery()
	defer cancel()
	zone := history.ResolveViewZone(zoneName)
	view, err := b.history.Day(names.ctx, date, offset, history.MaxDayPage, zone.Location)
	if err != nil && !errors.Is(err, history.ErrUnavailable) {
		return nil, err
	}
	encounters := make([]historySummaryJSON, 0, len(view.Encounters))
	for _, summary := range view.Encounters {
		encounters = append(encounters, names.summary(summary))
	}
	if names.err != nil {
		return nil, names.err
	}
	return json.Marshal(struct {
		Status     history.Status       `json:"status"`
		Zone       historyZoneJSON      `json:"zone"`
		Date       string               `json:"date"`
		Total      int                  `json:"total"`
		Offset     int                  `json:"offset"`
		Encounters []historySummaryJSON `json:"encounters"`
	}{b.history.Status(), zoneJSON(zone), view.Date, view.Total, view.Offset, encounters})
}

// HistoryEncounter returns one stored encounter: its summary with its times in the view's time zone, the capture session
// that recorded it and, for a finished encounter, the same encounter view and DPS timeline the Dashboard showed when it
// finished, named in the UI locale.
func (b *Bridge) HistoryEncounter(id int64, zoneName string) (json.RawMessage, error) {
	names, cancel := b.historyQuery()
	defer cancel()
	zone := history.ResolveViewZone(zoneName)
	detail, err := b.history.Encounter(names.ctx, id, zone.Location)
	if err != nil {
		return nil, err
	}
	out := struct {
		Status        history.Status                     `json:"status"`
		Zone          historyZoneJSON                    `json:"zone"`
		Summary       historySummaryJSON                 `json:"summary"`
		Session       history.SessionInfo                `json:"session"`
		Encounter     *capture.ViewEncounter             `json:"encounter"`
		Timeline      *capture.EncounterTimelineSnapshot `json:"timeline"`
		TimelineValid bool                               `json:"timelineValid"`
		Enemies       []historyEnemyJSON                 `json:"enemies"`
		Effects       history.EffectWindow               `json:"effects"`
	}{Status: b.history.Status(), Zone: zoneJSON(zone), Summary: names.summary(detail.Summary), Session: detail.Session, Enemies: []historyEnemyJSON{}, Effects: detail.Effects}
	if detail.Snapshot != nil {
		view := b.masters.ProjectEncounter(*detail.Snapshot)
		names.encounter(&view)
		out.Encounter = &view
		if detail.Timeline != nil {
			// An unfinished encounter's stored results describe its checkpoint, so its timeline ends there.
			end := detail.Snapshot.EndedAt
			if end.IsZero() && detail.Summary.CheckpointAt != nil {
				end = time.Unix(0, detail.Summary.CheckpointAt.UnixNano())
			}
			timeline, valid := detail.Timeline.RenderForView(view.EncounterID, detail.Snapshot.StartedAt, end)
			out.Timeline, out.TimelineValid = &timeline, valid
		}
	}
	for _, enemy := range detail.Enemies {
		named := historyName{Status: "UNKNOWN"}
		if !enemy.IdentityConflict {
			named = names.enemy(enemy.MonsterID, enemy.TemplateID)
		}
		out.Enemies = append(out.Enemies, historyEnemyJSON{
			Ordinal: enemy.Ordinal, MonsterID: enemy.MonsterID, TemplateID: enemy.TemplateID, IdentityConflict: enemy.IdentityConflict, Name: named,
			DamageTaken: enemy.DamageTaken, HitsTaken: enemy.HitsTaken, DamageDealt: enemy.DamageDealt, HitsDealt: enemy.HitsDealt,
		})
	}
	if names.err != nil {
		return nil, names.err
	}
	return json.Marshal(out)
}
