//go:build wwdev

package capture

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// This development-profile, test-only collector explains the swap_cross_direction case of
// TestTransportPerturbationCollector at the level of single damage records. It replays a capture unmodified and with the
// same cross-direction swaps, compares every damage record by its protocol key (session and RPC id), and isolates by
// bisection the individual swaps after which a record is classified differently. For each such swap it reports the
// frames the two datagrams carried and the records it changed. The archived captures are only read.
//
// Environment:
//
//	WWLOGS_REORDER_OUT             output directory (required to run)
//	WWLOGS_PERTURBATION_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_PERTURBATION_MASTER_DB  Master SQLite database

type reorderFrame struct {
	Direction string `json:"direction"`
	CommandID uint16 `json:"commandId"`
	Status    string `json:"decodeStatus"`
	Timestamp string `json:"timestamp"`
}

type reorderRecord struct {
	Flow         string `json:"flow"`
	Source       uint64 `json:"sourceEntityId"`
	SourceRel    string `json:"sourceRelation"`
	Target       uint64 `json:"targetEntityId"`
	TargetRel    string `json:"targetRelation"`
	SourceRoleID uint64 `json:"sourceRoleId,omitempty"`
	Downgrade    string `json:"contextDowngrade,omitempty"`
	Amount       int64  `json:"amount"`
	SkillID      uint64 `json:"skillId,omitempty"`
	ObservedAt   string `json:"observedAt"`
}

type reorderChange struct {
	Key    string         `json:"key"`
	Before *reorderRecord `json:"before"`
	After  *reorderRecord `json:"after"`
}

type reorderSwap struct {
	// Index is the position of the first datagram of the swapped pair in the unmodified capture.
	Index     int             `json:"index"`
	Joint     []int           `json:"jointWith,omitempty"`
	First     []reorderFrame  `json:"firstDatagramFrames"`
	Second    []reorderFrame  `json:"secondDatagramFrames"`
	FirstDir  string          `json:"firstDirection"`
	SecondDir string          `json:"secondDirection"`
	GapMicros int64           `json:"gapMicroseconds"`
	Changes   []reorderChange `json:"changes"`
}

type reorderReport struct {
	Capture         string         `json:"capture"`
	Datagrams       int            `json:"datagrams"`
	Swaps           int            `json:"swaps"`
	Records         int            `json:"records"`
	ChangedRecords  int            `json:"changedRecords"`
	ChangedAmount   int64          `json:"changedAmount"`
	FlowTransitions map[string]int `json:"flowTransitions"`
	Replays         int            `json:"replays"`
	CausalSwaps     []reorderSwap  `json:"causalSwaps"`
	Unexplained     []string       `json:"unexplainedKeys,omitempty"`
}

type reorderRun struct {
	records map[string]reorderRecord
	frames  [][]reorderFrame
}

func TestCrossDirectionReorderAudit(t *testing.T) {
	out := os.Getenv("WWLOGS_REORDER_OUT")
	if out == "" {
		t.Skip("WWLOGS_REORDER_OUT is not set")
	}
	master := os.Getenv("WWLOGS_PERTURBATION_MASTER_DB")
	listPath := os.Getenv("WWLOGS_PERTURBATION_PCAPS")
	if master == "" || listPath == "" {
		t.Fatal("WWLOGS_PERTURBATION_MASTER_DB and WWLOGS_PERTURBATION_PCAPS are required")
	}
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := loadGameMasterDatabase(master, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, capture := range readReplayPCAPList(t, listPath) {
		t.Run(capture.name, func(t *testing.T) {
			packets := readPerturbationPackets(t, capture.path)
			report := auditCrossDirection(t, masters, key, capture.name, packets)
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, capture.name+".reorder-audit.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// crossDirectionSwaps returns the pair positions swapped by the swap_cross_direction case.
func crossDirectionSwaps(packets []perturbationPacket) []int {
	var swaps []int
	for i := 0; i+1 < len(packets); i += 4 {
		if packets[i].direction != packets[i+1].direction {
			swaps = append(swaps, i)
		}
	}
	return swaps
}

func applySwaps(packets []perturbationPacket, swaps []int) ([]perturbationPacket, []int) {
	result := append([]perturbationPacket(nil), packets...)
	origin := make([]int, len(packets))
	for i := range origin {
		origin[i] = i
	}
	for _, i := range swaps {
		result[i], result[i+1] = result[i+1], result[i]
		origin[i], origin[i+1] = origin[i+1], origin[i]
	}
	return result, origin
}

// replayReorder replays packets with the record and frame diagnostics enabled. Frames are attributed to the original
// datagram index whose delivery produced them.
func replayReorder(t *testing.T, masters *gameMaster, key *staticRSA, packets []perturbationPacket, origin []int) reorderRun {
	t.Helper()
	var records, frames bytes.Buffer
	p := newProcessor(key, io.Discard, io.Discard, nil)
	p.masters = masters
	p.encounterOut = &records
	p.frameArchive = &frames
	streams := make(map[string]*stream)
	stats := captureStats{}
	run := reorderRun{records: make(map[string]reorderRecord), frames: make([][]reorderFrame, len(packets))}
	for i, packet := range packets {
		stats.udpPackets++
		before := frames.Len()
		consumeDatagram(packet.payload, packet.direction, packet.at, streams, p, &stats)
		run.frames[origin[i]] = parseReorderFrames(t, frames.Bytes()[before:])
	}
	finishTransport(streams, p, &stats)
	p.finishCapture()
	scanner := bufio.NewScanner(&records)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		var line struct {
			Type string `json:"type"`
			CombatEventRecord
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Type != "COMBAT_EVENT_RECORD" {
			continue
		}
		r := line.CombatEventRecord
		record := reorderRecord{
			Flow: string(r.Flow), Source: r.Source.EntityID, SourceRel: r.Source.Relation, Target: r.Target.EntityID, TargetRel: r.Target.Relation,
			Downgrade: r.ContextDowngrade, Amount: r.Amount, SkillID: r.SkillID, ObservedAt: r.ObservedAt.Format(time.RFC3339Nano),
		}
		if r.SourceRole != nil {
			record.SourceRoleID = r.SourceRole.RoleID
		}
		run.records[fmt.Sprintf("%d/%d", r.Key.ProtocolSession, r.Key.RPCID)] = record
	}
	return run
}

func parseReorderFrames(t *testing.T, data []byte) []reorderFrame {
	t.Helper()
	var frames []reorderFrame
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var frame reorderFrame
		if err := json.Unmarshal(line, &frame); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	return frames
}

// classification is what the attribution audit compares: the flow, the endpoint relations and the role.
func (r reorderRecord) classification() string {
	return fmt.Sprintf("%s|%s|%s|%d|%s", r.Flow, r.SourceRel, r.TargetRel, r.SourceRoleID, r.Downgrade)
}

func reorderDiff(base, run reorderRun) []reorderChange {
	var changes []reorderChange
	for key, before := range base.records {
		after, ok := run.records[key]
		switch {
		case !ok:
			b := before
			changes = append(changes, reorderChange{Key: key, Before: &b})
		case after.classification() != before.classification() || after.Amount != before.Amount:
			b, a := before, after
			changes = append(changes, reorderChange{Key: key, Before: &b, After: &a})
		}
	}
	for key, after := range run.records {
		if _, ok := base.records[key]; !ok {
			a := after
			changes = append(changes, reorderChange{Key: key, After: &a})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Key < changes[j].Key })
	return changes
}

func auditCrossDirection(t *testing.T, masters *gameMaster, key *staticRSA, name string, packets []perturbationPacket) reorderReport {
	t.Helper()
	identity := make([]int, len(packets))
	for i := range identity {
		identity[i] = i
	}
	base := replayReorder(t, masters, key, packets, identity)
	all := crossDirectionSwaps(packets)
	report := reorderReport{Capture: name, Datagrams: len(packets), Swaps: len(all), Records: len(base.records), FlowTransitions: map[string]int{}}
	changedBy := func(swaps []int) []reorderChange {
		report.Replays++
		swapped, origin := applySwaps(packets, swaps)
		return reorderDiff(base, replayReorder(t, masters, key, swapped, origin))
	}
	full := changedBy(all)
	report.ChangedRecords = len(full)
	for _, change := range full {
		before, after := "absent", "absent"
		if change.Before != nil {
			before = change.Before.Flow
			report.ChangedAmount += change.Before.Amount
		}
		if change.After != nil {
			after = change.After.Flow
		}
		report.FlowTransitions[before+" -> "+after]++
	}
	if len(full) == 0 {
		return report
	}
	// Bisection: a half that changes nothing on its own is dropped; when neither half changes anything but both together
	// do, the set is reported as a joint cause.
	var causal [][]int
	var find func(swaps []int, changed bool)
	find = func(swaps []int, changed bool) {
		if !changed {
			return
		}
		if len(swaps) == 1 {
			causal = append(causal, swaps)
			return
		}
		left, right := swaps[:len(swaps)/2], swaps[len(swaps)/2:]
		leftChanged, rightChanged := len(changedBy(left)) > 0, len(changedBy(right)) > 0
		if !leftChanged && !rightChanged {
			causal = append(causal, swaps)
			return
		}
		find(left, leftChanged)
		find(right, rightChanged)
	}
	find(all, true)
	explained := map[string]bool{}
	for _, set := range causal {
		i := set[0]
		swap := reorderSwap{
			Index: i, First: base.frames[i], Second: base.frames[i+1], FirstDir: packets[i].direction, SecondDir: packets[i+1].direction,
			GapMicros: packets[i+1].at.Sub(packets[i].at).Microseconds(), Changes: changedBy(set),
		}
		if len(set) > 1 {
			swap.Joint = set[1:]
		}
		for _, change := range swap.Changes {
			explained[change.Key] = true
		}
		report.CausalSwaps = append(report.CausalSwaps, swap)
	}
	for _, change := range full {
		if !explained[change.Key] {
			report.Unexplained = append(report.Unexplained, change.Key)
		}
	}
	t.Logf("%s: %d swaps, %d records, %d changed (%d), %d causal swap sets, %d unexplained, %d replays: %s", name, len(all), len(base.records),
		len(full), report.ChangedAmount, len(causal), len(report.Unexplained), report.Replays, strings.TrimSpace(fmt.Sprint(report.FlowTransitions)))
	return report
}
