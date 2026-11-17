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
func coreEffectState(p *processor) string {
	return withoutAllocationIDs(canonicalValue(p.effects.Snapshot()) + "|" + canonicalValue(p.stats))
}

// coreDamageState is the core damage result of a processor in canonical form.
func coreDamageState(p *processor) string {
	return withoutAllocationIDs(canonicalValue(p.damageStatistics.Snapshot()) + "|" + canonicalValue(p.notifyStats) + "|" + canonicalValue(p.stats))
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
	deliver := func(p *processor) {
		// Inner notify of a CombatReceivePackNotify, a top-level ApplyGameplayEffectNotify, and a client effect push.
		deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
		if err := p.handleBuffLifecycleMessage(gameFrame{commandID: cmdApplyGameplayEffectNotify, direction: "in", timestamp: effectAt(1)}, effectApplyPayload(2, 43, 201, 100, 1, 20, 20)); err != nil {
			t.Fatalf("top-level apply: %v", err)
		}
		deliverClientPush(t, p, 2, clientPushBody(testCombatCommon{entityID: 100}, fieldCombatPushDataApplyGameplayEffectPush, clientApplyPayload(268436729, 1505701002, 102, 1, 12)))
	}
	base := newClientEffectTestProcessor()
	deliver(base)
	failing := newClientEffectTestProcessor()
	var console bytes.Buffer
	failing.diagnostic = &console
	failing.dev.buffLifecycle = &failingDiagnosticWriter{}
	deliver(failing)

	if got, want := coreEffectState(failing), coreEffectState(base); got != want {
		t.Fatalf("core effect result changed by a failing diagnostic sink:\nfailing %s\nbase    %s", got, want)
	}
	// The top-level apply names no CombatCommon holder, so the core keeps only the other two as live effects.
	if len(base.effects.Snapshot().Effects) != 2 || failing.stats.parseErrors != 0 {
		t.Fatalf("effects = %d, parse errors = %d; want 2 effects and no parse error", len(base.effects.Snapshot().Effects), failing.stats.parseErrors)
	}
	if failing.dev.diagnosticFailures["buff-events"] != 3 || strings.Count(console.String(), "diagnostic_output_error sink=buff-events") != 1 {
		t.Fatalf("failures = %v, console = %q; want 3 counted and one reported line", failing.dev.diagnosticFailures, console.String())
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
	deliver := func(p *processor) {
		deliverEffectMessage(t, p, 0, effectPackBody(200, fieldCombatNotifyDataApplyGameplayEffectNotify, effectApplyPayload(1, 42, 200, 100, 1, 30, 30)))
		deliverEffectMessage(t, p, 1, effectPackBody(200, fieldCombatNotifyDataBuffDurationNotify, effectDurationPayload(1, nan, nan)))
		if err := p.handleBuffLifecycleMessage(gameFrame{commandID: cmdBuffDurationNotify, direction: "in", timestamp: effectAt(2)}, effectDurationPayload(1, float32(math.Inf(1)), 10)); err != nil {
			t.Fatalf("top-level duration: %v", err)
		}
	}
	base := newClientEffectTestProcessor()
	deliver(base)
	written := newClientEffectTestProcessor()
	var lifecycle bytes.Buffer
	written.dev.buffLifecycle = &lifecycle
	deliver(written)

	if got, want := coreEffectState(written), coreEffectState(base); got != want {
		t.Fatalf("core effect result changed by an unencodable diagnostic row:\nwith rows %s\nbase      %s", got, want)
	}
	if written.stats.parseErrors != 0 || written.dev.diagnosticFailures["buff-events"] != 2 {
		t.Fatalf("parse errors = %d, failures = %v; want none and 2", written.stats.parseErrors, written.dev.diagnosticFailures)
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
			audited.dev.combatNotifyAudit = &failingDiagnosticWriter{failAfter: failAfter}
			deliverNotifyFrame(t, audited, 1, 41, items...)

			if got, want := coreDamageState(audited), coreDamageState(base); got != want {
				t.Fatalf("core damage result changed by a failing audit:\naudited %s\nbase    %s", got, want)
			}
			if want := base.damageStatistics.Snapshot(); want.DamageDealt != 1200 || want.OutgoingEvents != 3 {
				t.Fatalf("base damage = %d in %d events, want 1200 in 3", want.DamageDealt, want.OutgoingEvents)
			}
			if audited.dev.diagnosticFailures["combat-notify-audit"] != 3-failAfter {
				t.Fatalf("audit failures = %v, want %d", audited.dev.diagnosticFailures, 3-failAfter)
			}
		})
	}
}

// An inner notify whose DamageContext the audit cannot decode: the audit row fails, the core keeps its own outcome.
func TestMalformedInnerNotifyAuditKeepsTheCoreOutcome(t *testing.T) {
	efn := appendVarintField(nil, fieldDamageExecuteNotifyAttackerEntityID, 100)
	efn = appendVarintField(efn, fieldDamageExecuteNotifyTargetEntityID, 200)
	efn = appendVarintField(efn, fieldDamageExecuteNotifyDamageID, notifyTestDamage)
	efn = appendVarintField(efn, fieldDamageExecuteNotifyDamage, 600)
	efn = appendVarintField(efn, fieldDamageExecuteNotifyChangeLife, 600)
	efn = appendBytesField(efn, fieldDamageExecuteNotifyDamageContext, []byte{0x08, 0x80}) // truncated varint
	notify := appendBytesField(nil, fieldCombatNotifyDataCombatCommon, appendVarintField(nil, fieldCombatCommonEntityID, 200))
	notify = appendBytesField(notify, fieldCombatNotifyDataDamageExecuteNotify, efn)
	malformed := appendBytesField(nil, fieldCombatReceiveDataCombatNotifyData, notify)
	following := notifyData(testNotify{attacker: 101, target: 201, damageID: notifyTestDamage, damage: 400, changeLife: 400})

	base := newNotifyTestProcessor()
	deliverNotifyFrame(t, base, 1, 41, malformed, following)
	audited := newNotifyTestProcessor()
	var audit bytes.Buffer
	audited.dev.combatNotifyAudit = &audit
	deliverNotifyFrame(t, audited, 1, 41, malformed, following)

	if got, want := coreDamageState(audited), coreDamageState(base); got != want {
		t.Fatalf("core outcome changed by an audit decode failure:\naudited %s\nbase    %s", got, want)
	}
	if audited.dev.diagnosticFailures["combat-notify-audit"] != 1 || !strings.Contains(audit.String(), `"innerFieldNumber"`) {
		t.Fatalf("audit failures = %v, audit = %q; want the malformed row counted and the following row written", audited.dev.diagnosticFailures, audit.String())
	}
}

// A failed snapshot row used to return before the identity, FSM and part components of the same response.
func TestFailedBuffSnapshotRowKeepsTheResponseComponents(t *testing.T) {
	monster := appendVarintField(nil, fieldMonsterCaptureComponentPbTemplateID, 123456)
	monster = appendVarintField(monster, fieldMonsterCaptureComponentPbMonsterID, 456789)
	response := effectSnapshotResponse(7, 640012035, 100, 1, 30, 30)
	response = appendBytesField(response, fieldEntityActiveResponseComponentPbs, appendBytesField(nil, fieldEntityComponentPbMonsterCaptureComponent, monster))

	base := newClientEffectTestProcessor()
	deliverEffectSnapshot(t, base, 9, 8801, 0, response)
	failing := newClientEffectTestProcessor()
	failing.dev.buffSnapshots = &failingDiagnosticWriter{}
	deliverEffectSnapshot(t, failing, 9, 8801, 0, response)

	if got, want := coreEffectState(failing), coreEffectState(base); got != want {
		t.Fatalf("core result changed by a failing snapshot sink:\nfailing %s\nbase    %s", got, want)
	}
	entity := failing.entities.entities[8801]
	if !entity.hasMonsterID || entity.monsterID != 456789 || failing.stats.activeResponseIdentityComponents != 1 {
		t.Fatalf("identity component of the response was skipped: entity=%+v stats=%+v", entity, failing.stats)
	}
	if failing.dev.diagnosticFailures["buff-snapshots"] != 1 || failing.stats.parseErrors != 0 {
		t.Fatalf("failures = %v, parse errors = %d", failing.dev.diagnosticFailures, failing.stats.parseErrors)
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
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is required: %v", err)
	}
	masters, err := loadGameMasterDatabase(os.Getenv("WWLOGS_DIAGNOSTIC_FAILURE_MASTER_DB"), "zh-TW")
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
		p := newProcessor(key, io.Discard, io.Discard, nil)
		p.masters = masters
		if fail {
			p.dev.combatEventsJSONL, p.dev.buffLifecycle = &failingDiagnosticWriter{}, &failingDiagnosticWriter{}
			p.dev.buffSnapshots, p.dev.combatNotifyAudit = &failingDiagnosticWriter{}, &failingDiagnosticWriter{}
		}
		streams, stats := make(map[string]*stream), captureStats{}
		replayRawCapture(p, capture, nil, 0, streams, &stats)
		finishTransport(streams, p, &stats)
		p.finishCapture()
		return volatile.ReplaceAllString(p.currentStatisticsReport().String(), "$1=*"), p.dev.diagnosticFailures, p.stats.parseErrors
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
	p.diagnostic = &console
	p.dev.combatEventsJSONL = &failingDiagnosticWriter{}
	deliverNotifyFrame(t, p, 1, 41,
		notifyData(testNotify{attacker: 100, target: 200, damageID: notifyTestDamage, damage: 300, changeLife: 300}),
		notifyData(testNotify{attacker: 101, target: 201, damageID: notifyTestDamage, damage: 400, changeLife: 400}))
	if got := p.damageStatistics.Snapshot(); got.DamageDealt != 700 || got.OutgoingEvents != 2 {
		t.Fatalf("damage = %d in %d events, want 700 in 2", got.DamageDealt, got.OutgoingEvents)
	}
	if p.dev.diagnosticFailures["combat-events"] < 2 || strings.Count(console.String(), "diagnostic_output_error sink=combat-events") != 1 {
		t.Fatalf("failures = %v, console = %q", p.dev.diagnosticFailures, console.String())
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
	if base.stats.printed != 3 || base.stats.matchedCombatResultAbs != 1200 || base.stats.playerRelatedEvents != 3 {
		t.Fatalf("base counters: printed=%d sum=%d related=%d, want 3, 1200, 3", base.stats.printed, base.stats.matchedCombatResultAbs, base.stats.playerRelatedEvents)
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
			failing.dev.stdout, failing.diagnostic = test.console, &diagnostic
			failing.dev.audit, failing.dev.research = &failingDiagnosticWriter{failAfter: 1}, true
			deliverNotifyFrame(t, failing, 1, 41, items...)

			if got, want := coreDamageState(failing), coreDamageState(base); got != want {
				t.Fatalf("core damage result changed by failing console and audit writes:\nfailing %s\nbase    %s", got, want)
			}
			if failing.dev.diagnosticFailures["console"] != test.wantConsole || failing.dev.diagnosticFailures["event-audit"] != 2 {
				t.Fatalf("failures = %v, want console=%d event-audit=2", failing.dev.diagnosticFailures, test.wantConsole)
			}
			text := diagnostic.String()
			if strings.Count(text, "diagnostic_output_error sink=console") != 1 || strings.Count(text, "[DAMAGE] ") != 3 {
				t.Fatalf("diagnostic stream = %q, want one console failure line and three research lines", text)
			}
			if failing.stats.parseErrors != 0 {
				t.Fatalf("parse errors = %d, want 0", failing.stats.parseErrors)
			}
		})
	}
}

// Outbound skill requests are a research record (Development, -protocol-schema). The link of a damage result to one is
// made by the Development event line only: it counts the link on the record and appears in the line, and the core event
// and result are the same with and without the record.
func TestSkillRequestLinkIsOnlyAResearchRecord(t *testing.T) {
	run := func(withRequest bool) (*processor, combatEvent, string) {
		p := newPairingTestProcessor()
		var events strings.Builder
		p.dev.combatEventsJSONL = &events
		if withRequest {
			key := combatActionRequestKey{sessionNumber: p.sessionNumber, messageID: 9001}
			p.dev.combatActionRequests[key] = combatActionRequest{SessionNumber: p.sessionNumber, MessageID: 9001, EntityID: 100, SkillID: 1409001, RequestType: "UseSkillRequest"}
		}
		request := pendingDamageRequest{attackerEntityID: 100, hasAttackerEntityID: true, targetEntityID: 200, hasTargetEntityID: true,
			damageID: notifyTestDamage, hasDamageID: true, skillID: 1409001, skillMessageID: 9001}
		event := p.buildDamageEvent(7, request, 250, 250, true, 250)
		p.writeDamageEvent(event)
		return p, event, events.String()
	}
	plain, plainEvent, plainLine := run(false)
	linked, linkedEvent, linkedLine := run(true)
	if strings.Contains(plainLine, `"request":`) || !strings.Contains(linkedLine, `"request":{`) || !strings.Contains(linkedLine, `"linkedDamageEvents":1`) {
		t.Fatalf("event lines: without record %s\nwith record %s", plainLine, linkedLine)
	}
	if got := linked.dev.combatActionRequests[combatActionRequestKey{sessionNumber: linked.sessionNumber, messageID: 9001}].LinkedDamageEvents; got != 1 {
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
	p := newProcessor(nil, io.Discard, io.Discard, nil)
	if lines := p.researchOutputErrorLines(); len(lines) != 0 {
		t.Fatalf("lines without research outputs = %q", lines)
	}
	var sink strings.Builder
	p.dev.roleConfigOut, p.dev.bulletRefOut, p.dev.combatStateOut = &sink, &sink, &sink
	p.dev.roleConfigErrors, p.dev.bulletRefErrors, p.dev.combatStateErrors, p.dev.hpStateErrors = 1, 2, 3, 4
	want := []string{"role_config errors=1", "bullet_reference errors=2", "combat_state errors=3"}
	if lines := p.researchOutputErrorLines(); !reflect.DeepEqual(lines, want) {
		t.Fatalf("lines = %q, want %q (hp_state has no output and must not report)", lines, want)
	}
	// A record that cannot be encoded is counted and reported by its output.
	if writeResearchRecord(&sink, map[string]float64{"x": math.NaN()}, &p.dev.bulletRefErrors) {
		t.Fatal("an unencodable record was reported as encoded")
	}
	if lines := p.researchOutputErrorLines(); lines[1] != "bullet_reference errors=3" {
		t.Fatalf("lines after a failed record = %q", lines)
	}
}
