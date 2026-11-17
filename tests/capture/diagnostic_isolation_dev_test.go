//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Failure injection for the Development diagnostic outputs (Phase 3A R01). A diagnostic row that cannot be encoded or
// written must leave the core result of the same input unchanged, must not be counted as a parse error, and must be
// counted on its own. Each test runs the same input through a processor without diagnostic outputs (the public build's
// situation: its sinks cannot be set) and one whose outputs fail, and compares the core results.

// failingDiagnosticWriter accepts failAfter writes and refuses every later one.
type failingDiagnosticWriter struct {
	failAfter, writes int
}

func (w *failingDiagnosticWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > w.failAfter {
		return 0, errors.New("injected diagnostic write failure")
	}
	return len(p), nil
}

// coreEffectState is the core effect result: the tracker snapshot and the core statistics, in canonical form.
func coreEffectState(p *Processor) string {
	return withoutAllocationIDs(canonicalValue(p.Effects.Snapshot()) + "|" + canonicalValue(p.Stats))
}

// coreDamageState is the core damage result of a processor in canonical form.
func coreDamageState(p *Processor) string {
	return withoutAllocationIDs(canonicalValue(p.DamageStatistics.Snapshot()) + "|" + canonicalValue(p.NotifyStats) + "|" + canonicalValue(p.Stats))
}

// withoutAllocationIDs removes the capture session id every tracker draws from a process-wide counter, and the
// encounter id prefix made from it: allocation order, not a result.
func withoutAllocationIDs(text string) string {
	text = regexp.MustCompile(`CaptureSessionID:\d+`).ReplaceAllString(text, "CaptureSessionID:*")
	return regexp.MustCompile(`EncounterID:\d+-`).ReplaceAllString(text, "EncounterID:*-")
}

// canonicalValue prints a value with pointers followed and floats in a stable text form (NaN equal to NaN), so equal
// results print equal however they are allocated.
func canonicalValue(value any) string {
	var out strings.Builder
	writeCanonical(&out, reflect.ValueOf(value), 0)
	return out.String()
}

var canonicalTimeType = reflect.TypeOf(time.Time{})

func writeCanonical(out *strings.Builder, v reflect.Value, depth int) {
	if !v.IsValid() || depth > 32 {
		out.WriteString("nil")
		return
	}
	if v.Type() == canonicalTimeType && v.CanInterface() {
		out.WriteString(v.Interface().(time.Time).UTC().Format(time.RFC3339Nano))
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			out.WriteString("nil")
			return
		}
		writeCanonical(out, v.Elem(), depth+1)
	case reflect.Struct:
		out.WriteString("{")
		for i := 0; i < v.NumField(); i++ {
			fmt.Fprintf(out, "%s:", v.Type().Field(i).Name)
			writeCanonical(out, v.Field(i), depth+1)
			out.WriteString(" ")
		}
		out.WriteString("}")
	case reflect.Slice, reflect.Array:
		out.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			writeCanonical(out, v.Index(i), depth+1)
			out.WriteString(" ")
		}
		out.WriteString("]")
	case reflect.Map:
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
		out.WriteString("map[")
		for _, key := range keys {
			fmt.Fprintf(out, "%v:", key)
			writeCanonical(out, v.MapIndex(key), depth+1)
			out.WriteString(" ")
		}
		out.WriteString("]")
	case reflect.Float32, reflect.Float64:
		out.WriteString(strconv.FormatFloat(v.Float(), 'g', -1, 64))
	default:
		fmt.Fprintf(out, "%v", v)
	}
}

func TestFailedBuffLifecycleRowsDoNotSkipEffectUpdates(t *testing.T) {
	deliver := func(p *Processor) {
		// Inner notify of a CombatReceivePackNotify, a top-level ApplyGameplayEffectNotify, and a client effect push.
		deliverEffectMessage(t, p, 0, effectPackBody(200, FieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
		if err := p.HandleBuffLifecycleMessage(GameFrame{CommandID: CmdApplyGameplayEffectNotify, Direction: "in", Timestamp: effectAt(1)}, effectApplyPayload(2, 43, 201, 100, 1, 20, 20)); err != nil {
			t.Fatalf("top-level apply: %v", err)
		}
		deliverClientPush(t, p, 2, clientPushBody(testCombatCommon{entityID: 100}, FieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436729, 1505701002, 102, 1, 12)))
	}
	base := newClientEffectTestProcessor()
	deliver(base)
	failing := newClientEffectTestProcessor()
	var console bytes.Buffer
	failing.Diagnostic = &console
	failing.Dev.buffLifecycle = &failingDiagnosticWriter{}
	deliver(failing)

	if got, want := coreEffectState(failing), coreEffectState(base); got != want {
		t.Fatalf("core effect result changed by a failing diagnostic sink:\nfailing %s\nbase    %s", got, want)
	}
	// The top-level apply names no CombatCommon holder, so the core keeps only the other two as live effects.
	if len(base.Effects.Snapshot().Effects) != 2 || failing.Stats.ParseErrors != 0 {
		t.Fatalf("effects = %d, parse errors = %d; want 2 effects and no parse error", len(base.Effects.Snapshot().Effects), failing.Stats.ParseErrors)
	}
	if failing.Dev.diagnosticFailures["buff-events"] != 3 || strings.Count(console.String(), "diagnostic_output_error sink=buff-events") != 1 {
		t.Fatalf("failures = %v, console = %q; want 3 counted and one reported line", failing.Dev.diagnosticFailures, console.String())
	}
	if line := failing.diagnosticFailureLine(); line != "diagnostic_output_errors buff-events=3" {
		t.Fatalf("summary line = %q", line)
	}
	if base.diagnosticFailureLine() != "" {
		t.Fatalf("a run without diagnostic outputs reported failures: %q", base.diagnosticFailureLine())
	}
}

// encoding/json rejects NaN and infinite floats, which the core accepts. The row fails, the duration update does not.
func TestUnencodableDiagnosticRowDoesNotSkipTheCoreUpdate(t *testing.T) {
	nan := float32(math.NaN())
	deliver := func(p *Processor) {
		deliverEffectMessage(t, p, 0, effectPackBody(200, FieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
		deliverEffectMessage(t, p, 1, effectPackBody(200, FieldCombatNotifyDataBuffDurationNotify, effectDurationPayload(1, nan, nan)))
		if err := p.HandleBuffLifecycleMessage(GameFrame{CommandID: CmdBuffDurationNotify, Direction: "in", Timestamp: effectAt(2)}, effectDurationPayload(1, float32(math.Inf(1)), 10)); err != nil {
			t.Fatalf("top-level duration: %v", err)
		}
	}
	base := newClientEffectTestProcessor()
	deliver(base)
	written := newClientEffectTestProcessor()
	var lifecycle bytes.Buffer
	written.Dev.buffLifecycle = &lifecycle
	deliver(written)

	if got, want := coreEffectState(written), coreEffectState(base); got != want {
		t.Fatalf("core effect result changed by an unencodable diagnostic row:\nwith rows %s\nbase      %s", got, want)
	}
	if written.Stats.ParseErrors != 0 || written.Dev.diagnosticFailures["buff-events"] != 2 {
		t.Fatalf("parse errors = %d, failures = %v; want none and 2", written.Stats.ParseErrors, written.Dev.diagnosticFailures)
	}
	if rows := strings.Count(lifecycle.String(), "\n"); rows != 1 {
		t.Fatalf("lifecycle rows = %d, want only the encodable apply row", rows)
	}
}

func TestFailedCombatNotifyAuditDoesNotSkipTheRestOfTheFrame(t *testing.T) {
	items := [][]byte{
		notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 300, changeLife: 300}),
		notifyData(testNotify{attacker: 101, target: 201, damageID: notifyTestDamage, damage: 400, changeLife: 400}),
		notifyData(testNotify{attacker: 100, target: 201, damageID: notifyTestDamage, damage: 500, changeLife: 500}),
	}
	for name, failAfter := range map[string]int{"every audit row fails": 0, "the audit fails after its first row": 1} {
		t.Run(name, func(t *testing.T) {
			base := newNotifyTestProcessor()
			deliverNotifyFrame(t, base, 1, 41, items...)
			audited := newNotifyTestProcessor()
			audited.Dev.combatNotifyAudit = &failingDiagnosticWriter{failAfter: failAfter}
			deliverNotifyFrame(t, audited, 1, 41, items...)

			if got, want := coreDamageState(audited), coreDamageState(base); got != want {
				t.Fatalf("core damage result changed by a failing audit:\naudited %s\nbase    %s", got, want)
			}
			if want := base.DamageStatistics.Snapshot(); want.DamageDealt != 1200 || want.OutgoingEvents != 3 {
				t.Fatalf("base damage = %d in %d events, want 1200 in 3", want.DamageDealt, want.OutgoingEvents)
			}
			if audited.Dev.diagnosticFailures["combat-notify-audit"] != 3-failAfter {
				t.Fatalf("audit failures = %v, want %d", audited.Dev.diagnosticFailures, 3-failAfter)
			}
		})
	}
}

// An inner notify whose DamageContext the audit cannot decode: the audit row fails, the core keeps its own outcome.
func TestMalformedInnerNotifyAuditKeepsTheCoreOutcome(t *testing.T) {
	efn := appendVarintField(nil, FieldDamageExecuteNotifyAttackerEntityID, 100)
	efn = appendVarintField(efn, FieldDamageExecuteNotifyTargetEntityID, 200)
	efn = appendVarintField(efn, FieldDamageExecuteNotifyDamageID, notifyTestDamage)
	efn = appendVarintField(efn, FieldDamageExecuteNotifyDamage, 600)
	efn = appendVarintField(efn, FieldDamageExecuteNotifyChangeLife, 600)
	efn = appendBytesField(efn, FieldDamageExecuteNotifyDamageContext, []byte{0x08, 0x80}) // truncated varint
	notify := appendBytesField(nil, FieldCombatNotifyDataCombatCommon, appendVarintField(nil, FieldCombatCommonEntityID, 200))
	notify = appendBytesField(notify, FieldCombatNotifyDataDamageExecuteNotify, efn)
	malformed := appendBytesField(nil, FieldCombatReceiveDataCombatNotifyData, notify)
	following := notifyData(testNotify{attacker: 101, target: 201, damageID: notifyTestDamage, damage: 400, changeLife: 400})

	base := newNotifyTestProcessor()
	deliverNotifyFrame(t, base, 1, 41, malformed, following)
	audited := newNotifyTestProcessor()
	var audit bytes.Buffer
	audited.Dev.combatNotifyAudit = &audit
	deliverNotifyFrame(t, audited, 1, 41, malformed, following)

	if got, want := coreDamageState(audited), coreDamageState(base); got != want {
		t.Fatalf("core outcome changed by an audit decode failure:\naudited %s\nbase    %s", got, want)
	}
	if audited.Dev.diagnosticFailures["combat-notify-audit"] != 1 || !strings.Contains(audit.String(), `"innerFieldNumber"`) {
		t.Fatalf("audit failures = %v, audit = %q; want the malformed row counted and the following row written", audited.Dev.diagnosticFailures, audit.String())
	}
}

// A failed snapshot row used to return before the identity, FSM and part components of the same response.
func TestFailedBuffSnapshotRowKeepsTheResponseComponents(t *testing.T) {
	monster := appendVarintField(nil, FieldMonsterCaptureComponentPbTemplateID, 123456)
	monster = appendVarintField(monster, FieldMonsterCaptureComponentPbMonsterID, 456789)
	response := effectSnapshotResponse(7, 640012035, 100, 1, 30, 30)
	response = appendBytesField(response, FieldEntityActiveResponseComponentPbs, appendBytesField(nil, FieldEntityComponentPbMonsterCaptureComponent, monster))

	base := newClientEffectTestProcessor()
	deliverEffectSnapshot(t, base, 9, 8801, 0, response)
	failing := newClientEffectTestProcessor()
	failing.Dev.buffSnapshots = &failingDiagnosticWriter{}
	deliverEffectSnapshot(t, failing, 9, 8801, 0, response)

	if got, want := coreEffectState(failing), coreEffectState(base); got != want {
		t.Fatalf("core result changed by a failing snapshot sink:\nfailing %s\nbase    %s", got, want)
	}
	entity := failing.Entities.Entities[8801]
	if !entity.HasMonsterID || entity.MonsterID != 456789 || failing.Stats.ActiveResponseIdentityComponents != 1 {
		t.Fatalf("identity component of the response was skipped: entity=%+v stats=%+v", entity, failing.Stats)
	}
	if failing.Dev.diagnosticFailures["buff-snapshots"] != 1 || failing.Stats.ParseErrors != 0 {
		t.Fatalf("failures = %v, parse errors = %d", failing.Dev.diagnosticFailures, failing.Stats.ParseErrors)
	}
}

// TestDiagnosticFailureReplayCollector replays real captures through the Development pipeline twice, without diagnostic
// outputs and with every row-level diagnostic output failing (combat events, Buff events, Buff snapshots, CombatNotifyData
// audit), and requires the same statistics report for each capture. Skipped unless WWLOGS_DIAGNOSTIC_FAILURE_OUT is set.
// Environment: WWLOGS_DIAGNOSTIC_FAILURE_OUT (report file), WWLOGS_DIAGNOSTIC_FAILURE_PCAPS ("name|path" per line),
// WWLOGS_DIAGNOSTIC_FAILURE_MASTER_DB (empty: the embedded Master).
func TestDiagnosticFailureReplayCollector(t *testing.T) {
	out := os.Getenv("WWLOGS_DIAGNOSTIC_FAILURE_OUT")
	if out == "" {
		t.Skip("WWLOGS_DIAGNOSTIC_FAILURE_OUT is not set")
	}
	captures := loadRawCaptures(t, os.Getenv("WWLOGS_DIAGNOSTIC_FAILURE_PCAPS"))
	key, err := LoadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := LoadGameMasterDatabase(os.Getenv("WWLOGS_DIAGNOSTIC_FAILURE_MASTER_DB"), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	type captureResult struct {
		Capture     string         `json:"capture"`
		Equal       bool           `json:"equal"`
		Plain       string         `json:"plain"`
		Failing     string         `json:"failing"`
		Failures    map[string]int `json:"diagnosticFailures"`
		ParseErrors [2]int         `json:"parseErrors"`
	}
	volatile := regexp.MustCompile(`(duration|run_id)=\S+`)
	run := func(capture rawCapture, fail bool) (string, map[string]int, int) {
		p := NewProcessor(key, io.Discard, io.Discard, nil)
		p.Masters = masters
		if fail {
			p.Dev.combatEventsJSONL, p.Dev.buffLifecycle = &failingDiagnosticWriter{}, &failingDiagnosticWriter{}
			p.Dev.buffSnapshots, p.Dev.combatNotifyAudit = &failingDiagnosticWriter{}, &failingDiagnosticWriter{}
		}
		streams, stats := make(map[string]*Stream), CaptureStats{}
		replayRawCapture(p, capture, nil, 0, streams, &stats)
		FinishTransport(streams, p, &stats)
		p.FinishCapture()
		return volatile.ReplaceAllString(p.CurrentStatisticsReport().String(), "$1=*"), p.Dev.diagnosticFailures, p.Stats.ParseErrors
	}
	var results []captureResult
	for _, capture := range captures {
		plain, _, plainParse := run(capture, false)
		failing, failures, failingParse := run(capture, true)
		results = append(results, captureResult{Capture: capture.name, Equal: plain == failing, Plain: plain, Failing: failing,
			Failures: failures, ParseErrors: [2]int{plainParse, failingParse}})
		if plain != failing || plainParse != failingParse {
			t.Errorf("%s: core result changed by failing diagnostics:\nplain   %s\nfailing %s", capture.name, plain, failing)
		}
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The combat event stream reports its failures the same way: counted per sink, reported once, core unaffected.
func TestFailedCombatEventStreamIsCountedOnce(t *testing.T) {
	p := newNotifyTestProcessor()
	var console bytes.Buffer
	p.Diagnostic = &console
	p.Dev.combatEventsJSONL = &failingDiagnosticWriter{}
	deliverNotifyFrame(t, p, 1, 41,
		notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 300, changeLife: 300}),
		notifyData(testNotify{attacker: 101, target: 201, damageID: notifyTestDamage, damage: 400, changeLife: 400}))
	if got := p.DamageStatistics.Snapshot(); got.DamageDealt != 700 || got.OutgoingEvents != 2 {
		t.Fatalf("damage = %d in %d events, want 700 in 2", got.DamageDealt, got.OutgoingEvents)
	}
	if p.Dev.diagnosticFailures["combat-events"] < 2 || strings.Count(console.String(), "diagnostic_output_error sink=combat-events") != 1 {
		t.Fatalf("failures = %v, console = %q", p.Dev.diagnosticFailures, console.String())
	}
}

// flushFailingConsole accepts every write and refuses every flush, like a buffered console whose pipe has closed.
type flushFailingConsole struct{ bytes.Buffer }

func (*flushFailingConsole) Flush() error { return errors.New("injected console flush failure") }

// failingBufferedConsole refuses writes after failAfter and every flush.
type failingBufferedConsole struct{ failingDiagnosticWriter }

func (*failingBufferedConsole) Flush() error { return errors.New("injected console flush failure") }

// A console or event audit write that failed used to return before the counters of the damage result (printed results,
// their sum, player-related events). Each failed line is counted once per sink; the counters and the damage statistics
// stay those of the same input without outputs, and the research lines are written for every result.
func TestFailedConsoleAndAuditWritesKeepTheDamageCounters(t *testing.T) {
	items := [][]byte{
		notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 300, changeLife: 300}),
		notifyData(testNotify{attacker: 101, target: 201, damageID: notifyTestDamage, damage: 400, changeLife: 400}),
		notifyData(testNotify{attacker: 100, target: 201, damageID: notifyTestDamage, damage: 500, changeLife: 500}),
	}
	base := newNotifyTestProcessor()
	deliverNotifyFrame(t, base, 1, 41, items...)
	if base.Stats.Printed != 3 || base.Stats.MatchedCombatResultAbs != 1200 || base.Stats.PlayerRelatedEvents != 3 {
		t.Fatalf("base counters: printed=%d sum=%d related=%d, want 3, 1200, 3", base.Stats.Printed, base.Stats.MatchedCombatResultAbs, base.Stats.PlayerRelatedEvents)
	}
	for _, test := range []struct {
		name        string
		console     io.Writer
		wantConsole int
	}{
		{"every console line fails", &failingDiagnosticWriter{}, 3},
		{"the console fails after its first line", &failingDiagnosticWriter{failAfter: 1}, 2},
		{"every console flush fails", &flushFailingConsole{}, 3},
		{"a failed buffered line is not flushed again", &failingBufferedConsole{}, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			failing := newNotifyTestProcessor()
			var diagnostic bytes.Buffer
			failing.Dev.stdout, failing.Diagnostic = test.console, &diagnostic
			failing.Dev.audit, failing.Dev.research = &failingDiagnosticWriter{failAfter: 1}, true
			deliverNotifyFrame(t, failing, 1, 41, items...)

			if got, want := coreDamageState(failing), coreDamageState(base); got != want {
				t.Fatalf("core damage result changed by failing console and audit writes:\nfailing %s\nbase    %s", got, want)
			}
			if failing.Dev.diagnosticFailures["console"] != test.wantConsole || failing.Dev.diagnosticFailures["event-audit"] != 2 {
				t.Fatalf("failures = %v, want console=%d event-audit=2", failing.Dev.diagnosticFailures, test.wantConsole)
			}
			text := diagnostic.String()
			if strings.Count(text, "diagnostic_output_error sink=console") != 1 || strings.Count(text, "[DAMAGE] ") != 3 {
				t.Fatalf("diagnostic stream = %q, want one console failure line and three research lines", text)
			}
			if failing.Stats.ParseErrors != 0 {
				t.Fatalf("parse errors = %d, want 0", failing.Stats.ParseErrors)
			}
		})
	}
}

// Outbound skill requests are a research record (Development, -protocol-schema). The link of a damage result to one is
// made by the Development event line only: it counts the link on the record and appears in the line, and the core event
// and result are the same with and without the record.
func TestSkillRequestLinkIsOnlyAResearchRecord(t *testing.T) {
	run := func(withRequest bool) (*Processor, CombatEvent, string) {
		p := newPairingTestProcessor()
		var events strings.Builder
		p.Dev.combatEventsJSONL = &events
		if withRequest {
			key := combatActionRequestKey{sessionNumber: p.SessionNumber, messageID: 9001}
			p.Dev.combatActionRequests[key] = combatActionRequest{SessionNumber: p.SessionNumber, MessageID: 9001, EntityID: 100, SkillID: 1409001, RequestType: "UseSkillRequest"}
		}
		request := PendingDamageRequest{AttackerEntityID: 100, HasAttackerEntityID: true, TargetEntityID: 200, HasTargetEntityID: true,
			DamageID: notifyTestDamage, HasDamageID: true, SkillID: 1409001, SkillMessageID: 9001}
		event := p.BuildDamageEvent(7, request, 250, 250, true, 250)
		p.WriteDamageEvent(event)
		return p, event, events.String()
	}
	plain, plainEvent, plainLine := run(false)
	linked, linkedEvent, linkedLine := run(true)
	if strings.Contains(plainLine, `"request":`) || !strings.Contains(linkedLine, `"request":{`) || !strings.Contains(linkedLine, `"linkedDamageEvents":1`) {
		t.Fatalf("event lines: without record %s\nwith record %s", plainLine, linkedLine)
	}
	if got := linked.Dev.combatActionRequests[combatActionRequestKey{sessionNumber: linked.SessionNumber, messageID: 9001}].LinkedDamageEvents; got != 1 {
		t.Fatalf("linked damage events on the record = %d, want 1", got)
	}
	if got, want := withoutAllocationIDs(canonicalValue(linkedEvent)), withoutAllocationIDs(canonicalValue(plainEvent)); got != want {
		t.Fatalf("damage event changed by the research link:\nlinked %s\nplain  %s", got, want)
	}
	if got, want := coreDamageState(linked), coreDamageState(plain); got != want {
		t.Fatalf("core damage result changed by the research link:\nlinked %s\nplain  %s", got, want)
	}
}

// Every research output the run wrote reports its failed records at the end of the run, also the outputs whose counters
// were not printed before (bullet, skill, client push, summon owner, damage push, HP state, combat state); an output
// that was not requested reports nothing.
func TestResearchOutputErrorsAreReported(t *testing.T) {
	p := NewProcessor(nil, io.Discard, io.Discard, nil)
	if lines := p.researchOutputErrorLines(); len(lines) != 0 {
		t.Fatalf("lines without research outputs = %q", lines)
	}
	var sink strings.Builder
	p.Dev.roleConfigOut, p.Dev.bulletRefOut, p.Dev.combatStateOut = &sink, &sink, &sink
	p.Dev.roleConfigErrors, p.Dev.bulletRefErrors, p.Dev.combatStateErrors, p.Dev.hpStateErrors = 1, 2, 3, 4
	want := []string{"role_config errors=1", "bullet_reference errors=2", "combat_state errors=3"}
	if lines := p.researchOutputErrorLines(); !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %q, want %q (hp_state has no output and must not report)", lines, want)
	}
	// A record that cannot be encoded is counted and reported by its output.
	if writeResearchRecord(&sink, map[string]float64{"x": math.NaN()}, &p.Dev.bulletRefErrors) {
		t.Fatal("an unencodable record was reported as encoded")
	}
	if lines := p.researchOutputErrorLines(); lines[1] != "bullet_reference errors=3" {
		t.Fatalf("lines after a failed record = %q", lines)
	}
}
