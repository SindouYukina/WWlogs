package desktop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"wwlogs/internal/capture"
	"wwlogs/internal/service"
)

// Contract tests between the Go host and the frontend (Phase 3A R09). The frontend calls the Bridge by name and reads
// the snapshot as plain JSON, so neither compiler sees the other side. These tests read the tracked frontend sources and
// compare them with the Go types: the Bridge methods host.ts calls with the number of arguments it passes, and the fields
// of the snapshot and settings types the frontend declares with the JSON fields Go sends. Both profiles run them.

func readFrontendSource(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "lib", name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

type frontendBridgeCall struct {
	method string
	args   int
}

var bridgeCallName = regexp.MustCompile(`^\("(\w+)"`)

// frontendBridgeCalls reads every call<T>("Method", ...) of host.ts with the number of arguments after the method name.
func frontendBridgeCalls(source string) []frontendBridgeCall {
	var calls []frontendBridgeCall
	for offset := 0; ; {
		start := strings.Index(source[offset:], "call<")
		if start < 0 {
			return calls
		}
		i := offset + start + len("call<")
		for depth := 1; i < len(source) && depth > 0; i++ { // the type argument
			switch source[i] {
			case '<':
				depth++
			case '>':
				depth--
			}
		}
		offset = i
		name := bridgeCallName.FindStringSubmatch(source[i:])
		if name == nil { // the generic helper itself: call<T>(method: string, ...)
			continue
		}
		args, depth := 0, 0
	scan:
		for j := i + len(name[0]); j < len(source); j++ {
			switch c := source[j]; c {
			case '"', '\'', '`':
				if end := strings.IndexByte(source[j+1:], c); end >= 0 {
					j += end + 1
				}
			case '(', '{', '[':
				depth++
			case ')', '}', ']':
				if depth == 0 {
					break scan
				}
				depth--
			case ',':
				if depth == 0 {
					args++
				}
			}
		}
		calls = append(calls, frontendBridgeCall{method: name[1], args: args})
	}
}

// Every Bridge method host.ts calls exists with the number of arguments it passes (a leading context.Context is the
// call context Wails supplies), and every exported Bridge method is called by host.ts or is a Wails service lifecycle
// method.
func TestFrontendBridgeCallsMatchTheGoBridge(t *testing.T) {
	calls := frontendBridgeCalls(readFrontendSource(t, "host.ts"))
	if len(calls) < 30 {
		t.Fatalf("read %d Bridge calls from host.ts; the reader no longer finds the host object's calls", len(calls))
	}
	bridge := reflect.TypeOf(&Bridge{})
	callContext := reflect.TypeOf((*context.Context)(nil)).Elem()
	called := make(map[string]bool)
	for _, call := range calls {
		if called[call.method] {
			t.Errorf("host.ts calls Bridge.%s from two wrappers", call.method)
		}
		called[call.method] = true
		method, ok := bridge.MethodByName(call.method)
		if !ok {
			t.Errorf("host.ts calls Bridge.%s, which the Go Bridge does not have", call.method)
			continue
		}
		parameters := method.Type.NumIn() - 1 // the receiver
		if parameters > 0 && method.Type.In(1) == callContext {
			parameters--
		}
		if parameters != call.args {
			t.Errorf("host.ts calls Bridge.%s with %d arguments; the Go method takes %d", call.method, call.args, parameters)
		}
	}
	lifecycle := map[string]bool{"ServiceName": true, "ServiceStartup": true, "ServiceShutdown": true}
	for i := 0; i < bridge.NumMethod(); i++ {
		if name := bridge.Method(i).Name; !called[name] && !lifecycle[name] {
			t.Errorf("Bridge.%s is exported to the frontend, but host.ts never calls it", name)
		}
	}
}

// The reader counts arguments at the top level of a call only.
func TestFrontendBridgeCallReaderCountsTopLevelArguments(t *testing.T) {
	source := `function call<T>(method: string, ...args: unknown[]): Promise<T> {}
a: () => call<Snapshot>("CurrentSnapshot"),
b: (x: number) => call<Map<string, number>>("One", x),
c: (r: string) => call<void>("Object", { localIPv4: r, other: [1, 2] }),
d: (a: number, q: Q, e: string) => call<C>("Three", a, f(q, 1), "x, y"),`
	got := frontendBridgeCalls(source)
	want := []frontendBridgeCall{{"CurrentSnapshot", 0}, {"One", 1}, {"Object", 1}, {"Three", 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %+v, want %+v", got, want)
	}
}

var tsProperty = regexp.MustCompile(`^\s*(?:readonly\s+)?(\w+)(\?)?\s*:`)

// tsInterfaceFields returns the properties of one exported interface and whether each is optional. Only the
// interface's own properties count; inline object types and comments inside it are skipped.
func tsInterfaceFields(t *testing.T, source, name string) map[string]bool {
	t.Helper()
	header := regexp.MustCompile(`(?m)^export interface ` + regexp.QuoteMeta(name) + ` \{`).FindStringIndex(source)
	if header == nil {
		t.Fatalf("the frontend declares no interface %s", name)
	}
	fields := make(map[string]bool)
	var statement strings.Builder
	flush := func() {
		if match := tsProperty.FindStringSubmatch(statement.String()); match != nil {
			fields[match[1]] = match[2] == "?"
		}
		statement.Reset()
	}
	depth := 1
	for i := header[1]; i < len(source); i++ {
		if strings.HasPrefix(source[i:], "/*") {
			end := strings.Index(source[i+2:], "*/")
			if end < 0 {
				break
			}
			i += end + 3
			continue
		}
		if strings.HasPrefix(source[i:], "//") {
			end := strings.IndexByte(source[i:], '\n')
			if end < 0 {
				break
			}
			i += end - 1
			continue
		}
		c := source[i]
		switch {
		case c == '{':
			if depth == 1 {
				flush()
			}
			depth++
		case c == '}':
			if depth == 1 {
				flush()
			}
			if depth--; depth == 0 {
				return fields
			}
		case depth != 1: // inside an inline object type
		case c == '\n' || c == ';':
			flush()
		default:
			statement.WriteByte(c)
		}
	}
	t.Fatalf("interface %s is not closed", name)
	return nil
}

// jsonFields returns the JSON object fields of a Go struct type and whether encoding/json omits each one when empty.
func jsonFields(goType reflect.Type) map[string]bool {
	goType = indirectType(goType)
	fields := make(map[string]bool)
	for i := 0; i < goType.NumField(); i++ {
		field := goType.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, options, _ := strings.Cut(tag, ",")
		if field.Anonymous && name == "" {
			for embedded, omitted := range jsonFields(field.Type) {
				fields[embedded] = omitted
			}
			continue
		}
		if !field.IsExported() {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = strings.Contains(","+options+",", ",omitempty,") || strings.Contains(","+options+",", ",omitzero,")
	}
	return fields
}

func indirectType(goType reflect.Type) reflect.Type {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	return goType
}

// goTypeAt follows a JSON path from a Go type: "statistics.encounterDps", with "[]" for the elements of a list or map.
func goTypeAt(t *testing.T, root reflect.Type, path string) reflect.Type {
	t.Helper()
	current := root
	for _, step := range strings.Split(path, ".") {
		elements := strings.HasSuffix(step, "[]")
		step = strings.TrimSuffix(step, "[]")
		structType := indirectType(current)
		found := false
		for i := 0; i < structType.NumField(); i++ {
			field := structType.Field(i)
			if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); name == step {
				current, found = field.Type, true
				break
			}
		}
		if !found {
			t.Fatalf("Go type %s has no JSON field %q (path %s)", structType, step, path)
		}
		if elements {
			if current = indirectType(current); current.Kind() != reflect.Slice && current.Kind() != reflect.Array && current.Kind() != reflect.Map {
				t.Fatalf("JSON field %q of path %s is not a list", step, path)
			}
			current = current.Elem()
		}
	}
	return indirectType(current)
}

// compareContract reports a frontend field Go never sends, a required frontend field Go omits when empty, and a Go field
// the frontend does not declare unless goOnly names it with the reason.
func compareContract(t *testing.T, label string, declared, sent map[string]bool, goOnly map[string]string) {
	t.Helper()
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		omitted, ok := sent[name]
		switch {
		case !ok:
			t.Errorf("%s.%s is declared by the frontend, but Go never sends it", label, name)
		case omitted && !declared[name]:
			t.Errorf("%s.%s is required in the frontend type, but Go omits it when empty", label, name)
		}
	}
	for name := range sent {
		if _, ok := declared[name]; !ok && goOnly[name] == "" {
			t.Errorf("Go sends %s.%s, which the frontend type does not declare", label, name)
		}
	}
	for name := range goOnly {
		if _, ok := declared[name]; ok {
			t.Errorf("%s.%s is listed as Go-only but the frontend declares it now", label, name)
		} else if _, ok := sent[name]; !ok {
			t.Errorf("%s.%s is listed as Go-only but Go no longer sends it", label, name)
		}
	}
}

// The snapshot types the frontend declares (lib/snapshot.ts, build-identity.ts, history.ts) and the settings answer
// (host.ts) name the JSON fields Go sends, with the same optionality where Go may omit a field.
func TestFrontendSnapshotTypesMatchTheGoPayload(t *testing.T) {
	status := reflect.TypeOf(service.Status{})
	view := reflect.TypeOf(capture.ViewSnapshot{})
	for _, contract := range []struct {
		file, name string
		goType     reflect.Type
		goOnly     map[string]string
	}{
		{"snapshot.ts", "ServiceStatus", status, map[string]string{
			"history": "the host's Combat History indicator (history_status.go) reads it; windows receive the indicator"}},
		{"snapshot.ts", "SessionInfo", goTypeAt(t, status, "session"), nil},
		{"snapshot.ts", "SessionResult", goTypeAt(t, status, "lastResult"), map[string]string{
			"historyIncomplete": "set when the session's Combat History was not stored completely; no window reads it yet"}},
		{"build-identity.ts", "AppIdentity", goTypeAt(t, status, "app"), nil},
		{"snapshot.ts", "ListeningInfo", reflect.TypeOf(service.ListeningStatus{}), nil},
		{"snapshot.ts", "OverlayState", reflect.TypeOf(OverlayState{}), nil},
		{"history.ts", "HistoryIndicator", reflect.TypeOf(historyIndicator{}), nil},
		{"snapshot.ts", "ViewSnapshot", view, nil},
		{"snapshot.ts", "DataIntegrity", goTypeAt(t, view, "statistics.integrity"), map[string]string{
			"firstLossAt": "Combat History stores it from the same core type (history/write.go); no window shows it"}},
		{"snapshot.ts", "DPS", goTypeAt(t, view, "statistics.encounterDps"), nil},
		{"snapshot.ts", "Healing", goTypeAt(t, view, "statistics.healing"), nil},
		{"snapshot.ts", "Encounter", goTypeAt(t, view, "encounters.recent[]"), map[string]string{
			"startBasis": "the evidence that opened the encounter; Combat History stores it from the core encounter; no window shows it",
			"endBasis":   "the evidence that closed the encounter; Combat History stores it from the core encounter; no window shows it",
		}},
		{"snapshot.ts", "Role", goTypeAt(t, view, "roles[]"), nil},
		{"snapshot.ts", "Skill", goTypeAt(t, view, "roles[].skills[]"), map[string]string{
			"sourceId": "the host projection resolves the derived name from it (projection.go) before a window receives the row"}},
		{"snapshot.ts", "UnassignedSkill", goTypeAt(t, view, "roles[].unassignedSkill"), map[string]string{
			"sourceId":            "the host projection resolves the derived name from it (projection.go) before a window receives the row",
			"damageIds":           "protocol facts of the embedded statistics totals (UnassignedSkillTotals); no window reads them",
			"damageIdsIncomplete": "protocol facts of the embedded statistics totals (UnassignedSkillTotals); no window reads them",
		}},
		{"snapshot.ts", "Status", goTypeAt(t, view, "roles[].statuses[]"), nil},
		{"snapshot.ts", "Vehicle", goTypeAt(t, view, "vehicle"), nil},
		{"snapshot.ts", "UnassignedRole", goTypeAt(t, view, "unassignedRole"), nil},
		{"snapshot.ts", "DamageCompleteness", goTypeAt(t, view, "completeness"), nil},
		{"snapshot.ts", "EffectRow", goTypeAt(t, view, "effects.buffs"), nil},
		{"snapshot.ts", "Effect", goTypeAt(t, view, "effects.recentEnded[]"), nil},
		{"snapshot.ts", "EffectCounts", goTypeAt(t, view, "effects.counts"), nil},
		{"snapshot.ts", "Timeline", goTypeAt(t, view, "timeline"), nil},
		{"snapshot.ts", "TimelinePoint", goTypeAt(t, view, "timeline.points[]"), nil},
		{"snapshot.ts", "TimelineContributor", goTypeAt(t, view, "timeline.contributors[]"), nil},
		{"host.ts", "SettingsState", reflect.TypeOf(SettingsState{}), nil},
		{"host.ts", "Settings", reflect.TypeOf(Settings{}), map[string]string{
			"languagePreference":   "windows read it from the snapshot (Snapshot.languagePreference)",
			"overlays":             "windows read the Overlay states from the snapshot (Snapshot.overlays)",
			"desktopWindow":        "the host's own Desktop window placement",
			"showOverlayOnStartup": "read from v0.5 settings files only, to migrate it; never written",
		}},
	} {
		t.Run(contract.name, func(t *testing.T) {
			declared := tsInterfaceFields(t, readFrontendSource(t, contract.file), contract.name)
			compareContract(t, contract.name, declared, jsonFields(contract.goType), contract.goOnly)
		})
	}
}

// The top-level fields of an encoded snapshot are the fields of the frontend Snapshot type.
func TestFrontendSnapshotTypeMatchesTheEncodedSnapshot(t *testing.T) {
	// Every omitempty field of the shell carries a value, so the encoding shows all of them.
	state := SettingsState{Settings: Settings{Locale: "en", LanguagePreference: languagePreferenceAuto}, Warning: "settings warning", LocaleRevision: 1}
	raw, err := (&displayProjection{}).encode(&service.Snapshot{Sequence: 1}, state, testLocaleCatalog(), service.ListeningStatus{}, OverlayStates{}, historyIndicator{})
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	// The anonymous payload struct of encode has no type to reflect on, so this compares names; the encoding above shows
	// every field, and the per-type test checks what Go may omit inside them.
	sent := make(map[string]bool, len(encoded))
	for name := range encoded {
		sent[name] = false
	}
	compareContract(t, "Snapshot", tsInterfaceFields(t, readFrontendSource(t, "snapshot.ts"), "Snapshot"), sent, nil)
}

// The interface reader takes an interface's own properties with their optionality, past comments and inline types.
func TestTypeScriptInterfaceReaderSkipsInlineTypesAndComments(t *testing.T) {
	source := `export interface Other { wrong: number }
export interface Sample {
  /** Doc comment with a colon: and braces { } */
  plain: number
  optional?: string // trailing: comment
  nested: { inner: number; deeper?: { x: string } }; after: boolean
  union:
    | "a"
    | "b"
  readonly fixed: number
}
export interface Next { other: number }`
	got := tsInterfaceFields(t, source, "Sample")
	want := map[string]bool{"plain": false, "optional": true, "nested": false, "after": false, "union": false, "fixed": false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
}
