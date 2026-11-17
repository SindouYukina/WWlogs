//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// The display audit explains, for Development diagnostics, why the Effects Overlay shows what it shows: for each cell why
// it is listed, which instances it stands for, and where its countdown, stack and icon come from; for everything else the
// reason it stays out. It reads the same decisions the view uses and changes nothing.

const effectDisplayAuditInterval = time.Second

type effectAuditMember struct {
	Holder           uint64   `json:"holderEntity"`
	HolderGeneration uint64   `json:"holderGeneration"`
	Handle           uint64   `json:"handle"`
	HolderRelation   string   `json:"holderRelation"`
	Source           *uint64  `json:"sourceEntity,omitempty"`
	SourceRelation   string   `json:"sourceRelation"`
	RemainingSeconds *float64 `json:"remainingSeconds,omitempty"`
	Stack            *uint64  `json:"stack,omitempty"`
}

type effectAuditCell struct {
	Key        string  `json:"key"`
	EffectID   *uint64 `json:"effectId,omitempty"`
	Instances  int     `json:"instances"`
	Shown      string  `json:"shownBecause"`
	ShownBasis string  `json:"shownBasis"`
	// Evidence is never stronger than CORRELATED: a kind is a classification with a named basis, not a verified game rule.
	Evidence string              `json:"evidence"`
	Merge    string              `json:"merge"`
	Target   string              `json:"target"`
	Members  []effectAuditMember `json:"members"`

	Countdown        string     `json:"countdown"`
	CountdownHandle  uint64     `json:"countdownFromHandle"`
	RemainingSeconds *float64   `json:"remainingSeconds,omitempty"`
	DurationSeconds  *float64   `json:"durationSeconds,omitempty"`
	ExpectedExpiry   *time.Time `json:"expectedExpiry,omitempty"`
	Stack            *uint64    `json:"stack,omitempty"`
	StackFrom        string     `json:"stackFrom"`
	Icon             string     `json:"icon"`
	CueTypes         []int32    `json:"cueTypes,omitempty"`
	Leaves           string     `json:"leavesWhen"`
	HiddenAfter      *time.Time `json:"hiddenAfter,omitempty"`
}

type effectAuditExcluded struct {
	Reason   string `json:"reason"`
	EffectID uint64 `json:"effectId"`
	Count    int    `json:"instances"`
	Kind     string `json:"kind"`
	Basis    string `json:"basis"`
	// DisplayBasis names why the instance is non-HUD or unresolved.
	DisplayBasis string `json:"displayBasis,omitempty"`
}

type effectAuditRow struct {
	At           time.Time             `json:"at"`
	ClockLead    float64               `json:"clockLeadSeconds"`
	ClockStalled bool                  `json:"clockStalled"`
	Status       string                `json:"status"`
	Counts       ViewEffectCounts      `json:"counts"`
	Cells        []effectAuditCell     `json:"cells"`
	Excluded     []effectAuditExcluded `json:"excluded,omitempty"`
	// ExcludedUnchanged says Excluded is the same as in the previous row and was left out to bound the file.
	ExcludedUnchanged bool `json:"excludedUnchanged,omitempty"`
}

func effectAuditMergeKind(cell EffectDisplayCell) string {
	if cell.Instances() > 1 {
		return "same_effect_on_several_holders"
	}
	return "single_instance"
}

func effectAuditCountdown(cell EffectDisplayCell) string {
	return string(cell.Earliest.RemainingStatus)
}

func effectAuditLeaves(cell EffectDisplayCell) (string, *time.Time) {
	if cell.Earliest.ExpectedExpiry == nil {
		return "server_remove", nil
	}
	latest := *cell.Earliest.ExpectedExpiry
	for _, member := range cell.Members {
		if member.ExpectedExpiry != nil && member.ExpectedExpiry.After(latest) {
			latest = *member.ExpectedExpiry
		}
	}
	hidden := latest.Add(EffectDisplayExpiryGrace)
	return "server_remove_or_expiry_plus_grace", &hidden
}

func newEffectAuditCell(cell EffectDisplayCell) effectAuditCell {
	representative := cell.Representative
	out := effectAuditCell{
		Key: EffectViewKey(representative), EffectID: CloneUint64(representative.EffectID), Instances: cell.Instances(),
		Shown: string(representative.Kind), ShownBasis: representative.KindBasis, Evidence: "CORRELATED", Merge: effectAuditMergeKind(cell),
		Target: string(EffectDisplayFor(representative).Target), Countdown: effectAuditCountdown(cell), CountdownHandle: cell.Earliest.HandleID,
		RemainingSeconds: CloneFloat64(cell.Earliest.RemainingSeconds), DurationSeconds: CloneFloat64(representative.ServerDurationSeconds),
		Stack: CloneUint64(representative.StackCount), StackFrom: "every_member_equal", Icon: "none",
	}
	if cell.Instances() == 1 {
		out.StackFrom = "single_instance"
	}
	if cell.Earliest.ExpectedExpiry != nil {
		expiry := *cell.Earliest.ExpectedExpiry
		out.ExpectedExpiry = &expiry
	}
	if static := representative.Static; static != nil && len(static.HUDCueTypes) > 0 {
		out.Icon, out.CueTypes = "hud_cue", static.HUDCueTypes
	}
	out.Leaves, out.HiddenAfter = effectAuditLeaves(cell)
	for _, member := range cell.Members {
		row := effectAuditMember{Holder: member.HolderEntityID, HolderGeneration: member.HolderGeneration, Handle: member.HandleID, HolderRelation: string(member.HolderRelation), Source: CloneUint64(member.SourceEntityID), SourceRelation: string(member.SourceRelation), RemainingSeconds: CloneFloat64(member.RemainingSeconds), Stack: CloneUint64(member.StackCount)}
		out.Members = append(out.Members, row)
	}
	sort.Slice(out.Members, func(i, j int) bool { return out.Members[i].Handle < out.Members[j].Handle })
	return out
}

// effectDisplayAudit explains the current view. The two signatures change only when the set of cells or of excluded
// effects changes, never because time passed.
func (p *Processor) effectDisplayAudit() (row effectAuditRow, cellSignature, excludedSignature string) {
	snapshot := p.EffectSnapshotAfterLastFrame(p.ViewClockLead)
	row = effectAuditRow{At: p.CurrentFrameTime, ClockLead: p.ViewClockLead.Seconds(), ClockStalled: p.ViewClockStalled, Status: string(snapshot.Status)}
	var eligible []EffectInstanceSnapshot
	type excludedKey struct {
		reason string
		id     uint64
		kind   string
		basis  string
		// display names why a non-HUD or unresolved decision was made.
		display string
	}
	excluded := map[excludedKey]int{}
	for _, effect := range snapshot.Effects {
		display := EffectDisplayFor(effect)
		row.Counts.Add(display.Eligibility)
		if display.Eligibility == EffectDisplayEligible {
			eligible = append(eligible, effect)
			continue
		}
		key := excludedKey{reason: string(display.Eligibility), kind: string(effect.Kind), basis: effect.KindBasis, display: display.Basis}
		if effect.EffectID != nil {
			key.id = *effect.EffectID
		}
		excluded[key]++
	}
	cells := EffectDisplayCells(eligible)
	row.Counts.Cells, row.Counts.ListedCells, row.Counts.Listed = len(cells), len(cells), len(eligible)
	var signature strings.Builder
	for _, cell := range cells {
		audit := newEffectAuditCell(cell)
		row.Cells = append(row.Cells, audit)
	}
	sort.Slice(row.Cells, func(i, j int) bool { return row.Cells[i].Key < row.Cells[j].Key })
	for _, cell := range row.Cells {
		fmt.Fprintf(&signature, "%s:%d:%s;", cell.Key, cell.Instances, cell.Merge)
	}
	for key, count := range excluded {
		row.Excluded = append(row.Excluded, effectAuditExcluded{Reason: key.reason, EffectID: key.id, Count: count, Kind: key.kind, Basis: key.basis, DisplayBasis: key.display})
	}
	sort.Slice(row.Excluded, func(i, j int) bool {
		a, b := row.Excluded[i], row.Excluded[j]
		if a.Reason != b.Reason {
			return a.Reason < b.Reason
		}
		return a.EffectID < b.EffectID
	})
	var other strings.Builder
	for _, entry := range row.Excluded {
		fmt.Fprintf(&other, "%s:%d:%d;", entry.Reason, entry.EffectID, entry.Count)
	}
	return row, signature.String(), other.String()
}

// effectDisplayAuditWriter writes a row when the display set changed, at most once per effectDisplayAuditInterval. It runs
// on the capture goroutine, but the writer it feeds is the asynchronous bounded diagnostic sink, so it never blocks.
type effectDisplayAuditWriter struct {
	target   io.Writer
	lastAt   time.Time
	cells    string
	excluded string
}

func (w *effectDisplayAuditWriter) observe(p *Processor, now time.Time) {
	if w == nil || w.target == nil || p.Effects == nil {
		return
	}
	if !w.lastAt.IsZero() && now.Sub(w.lastAt) < effectDisplayAuditInterval {
		return
	}
	row, cells, excluded := p.effectDisplayAudit()
	if cells == w.cells && excluded == w.excluded {
		return
	}
	if excluded == w.excluded {
		row.Excluded, row.ExcludedUnchanged = nil, true
	}
	w.lastAt, w.cells, w.excluded = now, cells, excluded
	_ = json.NewEncoder(w.target).Encode(row)
}
