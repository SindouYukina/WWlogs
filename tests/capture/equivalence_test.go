package capture

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
)

// This file is the test-only collector for the three-way core equivalence acceptance (Golden Baseline,
// Development profile, Public profile). It replays archived PCAP files through the same processor that
// the executable uses and writes a canonical, profile-independent record of the core state. It is skipped
// unless WWLOGS_EQUIVALENCE_OUT is set, adds no flag or export to any executable, and reads only paths the
// caller supplies through the environment. The embedded key payload must be present in the test binary
// (scripts/embed-key.go -test-binary); without it the collector fails loudly instead of skipping.
//
// Environment:
//
//	WWLOGS_EQUIVALENCE_OUT        output directory (required to run)
//	WWLOGS_EQUIVALENCE_PCAPS      text file with one "name|absolute path" per line
//	WWLOGS_EQUIVALENCE_MASTER_DB  Master SQLite database
//	WWLOGS_EQUIVALENCE_LOCALE     Master locale (default zh-TW)
//	WWLOGS_EQUIVALENCE_LOCAL_IP   local IPv4 used to decide packet direction (default 192.168.1.2)

const equivalenceFilter = "udp and portrange 13100-13120"

type equivalenceInputs struct {
	out      string
	pcaps    []equivalencePCAP
	master   string
	locale   string
	localIP  string
	sessions int
}

type equivalencePCAP struct {
	name string
	path string
}

func equivalenceEnvironment(t *testing.T) (equivalenceInputs, bool) {
	t.Helper()
	inputs := equivalenceInputs{
		out:     os.Getenv("WWLOGS_EQUIVALENCE_OUT"),
		master:  os.Getenv("WWLOGS_EQUIVALENCE_MASTER_DB"),
		locale:  os.Getenv("WWLOGS_EQUIVALENCE_LOCALE"),
		localIP: os.Getenv("WWLOGS_EQUIVALENCE_LOCAL_IP"),
	}
	if inputs.out == "" {
		return inputs, false
	}
	if inputs.locale == "" {
		inputs.locale = "zh-TW"
	}
	if inputs.localIP == "" {
		inputs.localIP = "192.168.1.2"
	}
	if inputs.master == "" {
		t.Fatal("WWLOGS_EQUIVALENCE_MASTER_DB is required")
	}
	listPath := os.Getenv("WWLOGS_EQUIVALENCE_PCAPS")
	if listPath == "" {
		t.Fatal("WWLOGS_EQUIVALENCE_PCAPS is required")
	}
	file, err := os.Open(listPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, path, found := strings.Cut(line, "|")
		if !found {
			t.Fatalf("malformed PCAP list line %q", line)
		}
		inputs.pcaps = append(inputs.pcaps, equivalencePCAP{name: name, path: path})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(inputs.pcaps) == 0 {
		t.Fatal("PCAP list is empty")
	}
	return inputs, true
}

// equivalenceReplay replays one PCAP exactly like the executables' offline loop: same BPF filter, same
// direction rule, same datagram entry point. after is called with the 1-based UDP datagram index.
func equivalenceReplay(t *testing.T, inputs equivalenceInputs, masters *gameMaster, key *staticRSA, path string, configure func(*processor), after func(index int, p *processor)) (*processor, captureStats) {
	t.Helper()
	p := newProcessor(key, io.Discard, io.Discard, nil)
	p.masters = masters
	if configure != nil {
		configure(p)
	}
	handle, err := pcap.OpenOffline(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer handle.Close()
	if err := handle.SetBPFFilter(equivalenceFilter); err != nil {
		t.Fatal(err)
	}
	source := gopacket.NewPacketSource(handle, handle.LinkType())
	streams := make(map[string]*stream)
	stats := captureStats{}
	for {
		packet, err := source.NextPacket()
		if err != nil {
			if err == io.EOF {
				break
			}
			// A damaged or truncated capture ends the replay the same way in every profile.
			break
		}
		udpLayer := packet.Layer(layers.LayerTypeUDP)
		if udpLayer == nil {
			continue
		}
		udp := udpLayer.(*layers.UDP)
		stats.udpPackets++
		direction := packetDirection(packet, inputs.localIP, udp, 13100, 13120)
		consumeDatagram(udp.Payload, direction, packet.Metadata().CaptureInfo.Timestamp, streams, p, &stats)
		if after != nil {
			after(stats.udpPackets, p)
		}
	}
	// The executables flush open KCP gaps when a capture ends; replays do the same.
	finishTransport(streams, p, &stats)
	return p, stats
}

type equivalenceState struct {
	Damage  DamageStatisticsSnapshot `json:"damage"`
	Effects EffectSnapshot           `json:"effects"`
}

// equivalenceNormalizer removes the process-local capture session sequence so that two processes (or two
// PCAPs replayed in one process) compare equal. Everything else is kept exactly as produced.
type equivalenceNormalizer struct{ base uint64 }

func newEquivalenceNormalizer(p *processor) equivalenceNormalizer {
	return equivalenceNormalizer{base: p.damageStatistics.Snapshot().CaptureSessionID - 1}
}

func (n equivalenceNormalizer) damage(s DamageStatisticsSnapshot) DamageStatisticsSnapshot {
	s.CaptureSessionID -= n.base
	return s
}

func (n equivalenceNormalizer) effects(s EffectSnapshot) EffectSnapshot {
	s.CaptureSessionID -= n.base
	return s
}

// stateAtFrame reads the statistics exactly like the live report: unqualified totals first, then the
// capture-clock query at the last observed frame time.
func (n equivalenceNormalizer) stateAtFrame(p *processor) equivalenceState {
	damage := p.damageStatisticsSnapshot()
	if observedAt := p.currentFrameTime; !observedAt.IsZero() {
		damage = p.damageStatisticsSnapshotAt(CaptureQueryTime{CaptureSessionID: damage.CaptureSessionID, Timestamp: observedAt})
	}
	return equivalenceState{Damage: n.damage(damage), Effects: n.effects(p.effectSnapshotAtLastFrame())}
}

func equivalenceMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.MarshalIndent(value, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func equivalenceDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// equivalenceInts dumps every integer field of a struct by name, so a counter added or removed in one
// source tree shows up as a field-set difference instead of being skipped.
func equivalenceInts(value any) map[string]int64 {
	out := map[string]int64{}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		switch field.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			out[v.Type().Field(i).Name] = field.Int()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			out[v.Type().Field(i).Name] = int64(field.Uint())
		}
	}
	return out
}

// equivalenceContainerSizes records the size of the bounded request, handle, and pairing tables.
func equivalenceContainerSizes(p *processor) map[string]int {
	out := map[string]int{}
	v := reflect.ValueOf(p).Elem()
	for _, name := range []string{"pending", "pendingOrder", "consumed", "consumedOrder", "activeRequests", "combatActionRequests", "combatActionOrder", "bulletHandles", "buffHandles", "buffHandleOrder", "bulletHandleOrder"} {
		field := v.FieldByName(name)
		if field.IsValid() {
			out[name] = field.Len()
		}
	}
	return out
}

func equivalenceScalars(p *processor) map[string]string {
	out := map[string]string{}
	v := reflect.ValueOf(p).Elem()
	for _, name := range []string{"activeRoleID", "hasActiveRole", "sessionNumber", "sessionGeneration", "flowTime", "hasFlowTime"} {
		field := v.FieldByName(name)
		if field.IsValid() {
			out[name] = fmt.Sprint(field)
		}
	}
	out["currentFrameTime"] = p.currentFrameTime.Format(time.RFC3339Nano)
	return out
}

// equivalenceDiagnosticScalars holds counters that exist only to label diagnostic records. frameOrdinal is
// advanced by the frame archive path, which the public profile does not contain, so it is compared between
// the profiles that have it and reported (not compared) for the public profile.
func equivalenceDiagnosticScalars(p *processor) map[string]string {
	out := map[string]string{}
	v := reflect.ValueOf(p).Elem()
	for _, name := range []string{"frameOrdinal", "eventOrderFrame", "eventOrder"} {
		field := v.FieldByName(name)
		if field.IsValid() {
			out[name] = fmt.Sprint(field)
		}
	}
	return out
}

type equivalenceTimelinePoint struct {
	Datagram    int       `json:"datagram"`
	FrameTime   time.Time `json:"frameTime"`
	Digest      string    `json:"digest"`
	DamageDealt int64     `json:"damageDealt"`
	DamageTaken int64     `json:"damageTaken"`
	Outgoing    uint64    `json:"outgoingEvents"`
	Incoming    uint64    `json:"incomingEvents"`
	Unattrib    uint64    `json:"unattributedEvents"`
	Players     int       `json:"players"`
	Rolling5s   float64   `json:"rolling5s"`
	Rolling10s  float64   `json:"rolling10s"`
	Active      int       `json:"activeEffects"`
	Applies     uint64    `json:"effectApplies"`
	Removes     uint64    `json:"effectRemoves"`
}

type equivalenceRecord struct {
	Name string `json:"name"`
	// Final is the end-of-replay state read once, as the executables do when a replay ends.
	Final equivalenceState `json:"final"`
	// FinalDamageDigest and FinalEffectsDigest are SHA-256 over the canonical JSON of the sections above.
	Capture          map[string]int64           `json:"capture"`
	Processor        map[string]int64           `json:"processorStats"`
	Containers       map[string]int             `json:"containers"`
	Scalars          map[string]string          `json:"scalars"`
	TimelineChain    string                     `json:"timelineChain"`
	TimelineDatagram int                        `json:"timelineDatagrams"`
	Timeline         []equivalenceTimelinePoint `json:"timeline"`
	// Reset records a mid-replay Reset: both interval snapshots, then the continued final state.
	Reset equivalenceReset `json:"reset"`
}

type equivalenceReset struct {
	AtDatagram    int                      `json:"atDatagram"`
	Before        DamageStatisticsSnapshot `json:"before"`
	EffectsBefore EffectSnapshot           `json:"effectsBefore"`
	AfterReset    equivalenceState         `json:"afterReset"`
	Final         equivalenceState         `json:"final"`
}

func TestCoreEquivalenceCollector(t *testing.T) {
	inputs, enabled := equivalenceEnvironment(t)
	if !enabled {
		t.Skip("WWLOGS_EQUIVALENCE_OUT is not set")
	}
	key, err := loadEmbeddedPrivateKey()
	if err != nil {
		t.Fatalf("embedded key payload is unavailable (build the test binary with scripts/embed-key.go -test-binary): %v", err)
	}
	masters, err := loadGameMasterDatabase(inputs.master, inputs.locale)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range equivalenceModes() {
		directory := filepath.Join(inputs.out, mode.name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, item := range inputs.pcaps {
			record, extra := collectEquivalence(t, inputs, masters, key, item, mode)
			if err := os.WriteFile(filepath.Join(directory, item.name+".core.json"), equivalenceMarshal(t, record), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, item.name+".diag.json"), equivalenceMarshal(t, extra), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func collectEquivalence(t *testing.T, inputs equivalenceInputs, masters *gameMaster, key *staticRSA, item equivalencePCAP, mode equivalenceMode) (equivalenceRecord, map[string]any) {
	t.Helper()
	var sinks *equivalenceSinkSet
	configure := func(p *processor) {
		if mode.sinks {
			sinks = attachEquivalenceSinks(p)
		}
	}

	// Pass 1: read only once at the end, like an executable replay.
	var normalizer equivalenceNormalizer
	finalProcessor, stats := equivalenceReplay(t, inputs, masters, key, item.path, func(p *processor) {
		normalizer = newEquivalenceNormalizer(p)
		configure(p)
	}, nil)
	record := equivalenceRecord{
		Name:       item.name,
		Final:      normalizer.stateAtFrame(finalProcessor),
		Capture:    equivalenceInts(&stats),
		Processor:  equivalenceInts(&finalProcessor.stats),
		Containers: equivalenceContainerSizes(finalProcessor),
		Scalars:    equivalenceScalars(finalProcessor),
	}
	extra := map[string]any{"diagnosticScalars": equivalenceDiagnosticScalars(finalProcessor)}
	if sinks != nil {
		extra["sinks"] = sinks.summary()
	}
	total := stats.udpPackets

	// Pass 2: query after every datagram, which is how a live display polls. The digest chain proves that
	// the state evolved identically, not just that it ended identically.
	chain := sha256.New()
	previous := ""
	var normalizer2 equivalenceNormalizer
	equivalenceReplay(t, inputs, masters, key, item.path, func(p *processor) {
		normalizer2 = newEquivalenceNormalizer(p)
		if mode.sinks {
			attachEquivalenceSinks(p)
		}
	}, func(index int, p *processor) {
		state := normalizer2.stateAtFrame(p)
		digest := equivalenceDigest(equivalenceMarshal(t, state))
		chain.Write([]byte(digest))
		record.TimelineDatagram = index
		if digest == previous {
			return
		}
		previous = digest
		record.Timeline = append(record.Timeline, equivalenceTimelinePoint{
			Datagram:    index,
			FrameTime:   p.currentFrameTime,
			Digest:      digest,
			DamageDealt: state.Damage.DamageDealt,
			DamageTaken: state.Damage.DamageTaken,
			Outgoing:    state.Damage.OutgoingEvents,
			Incoming:    state.Damage.IncomingEvents,
			Unattrib:    state.Damage.UnattributedOutgoingEvents + state.Damage.UnattributedIncomingEvents,
			Players:     len(state.Damage.ByPlayerEntity),
			Rolling5s:   state.Damage.Rolling5sDPS.Value,
			Rolling10s:  state.Damage.Rolling10sDPS.Value,
			Active:      len(state.Effects.Effects),
			Applies:     state.Effects.Counters.Applies,
			Removes:     state.Effects.Counters.Removes,
		})
	})
	record.TimelineChain = hex.EncodeToString(chain.Sum(nil))

	// Pass 3: Reset (snapshot-and-reset on both trackers) at the midpoint, then keep replaying.
	if total > 0 {
		resetAt := total / 2
		var normalizer3 equivalenceNormalizer
		reset := equivalenceReset{AtDatagram: resetAt}
		done := false
		resetProcessor, _ := equivalenceReplay(t, inputs, masters, key, item.path, func(p *processor) {
			normalizer3 = newEquivalenceNormalizer(p)
			if mode.sinks {
				attachEquivalenceSinks(p)
			}
		}, func(index int, p *processor) {
			if done || index != resetAt {
				return
			}
			done = true
			damage := p.damageStatisticsSnapshot()
			query := CaptureQueryTime{CaptureSessionID: damage.CaptureSessionID, Timestamp: p.currentFrameTime}
			if !p.currentFrameTime.IsZero() {
				reset.Before = normalizer3.damage(p.snapshotAndResetDamageStatisticsAt(query))
				reset.EffectsBefore = normalizer3.effects(p.effects.SnapshotAndResetAt(query))
			} else {
				reset.Before = normalizer3.damage(p.snapshotAndResetDamageStatistics())
				p.effects.Reset()
			}
			reset.AfterReset = normalizer3.stateAtFrame(p)
		})
		reset.Final = normalizer3.stateAtFrame(resetProcessor)
		record.Reset = reset
	}
	return record, extra
}

type equivalenceMode struct {
	name  string
	sinks bool
}

// equivalenceModes lists the collector modes this build can run. "bare" attaches no diagnostic sink and
// exists in every profile. "sinks" attaches discarding hash writers to the diagnostic sinks and exists
// only where those sinks exist (Development and the Golden Baseline).
func equivalenceModes() []equivalenceMode {
	modes := []equivalenceMode{{name: "bare"}}
	if equivalenceSinksSupported {
		modes = append(modes, equivalenceMode{name: "sinks", sinks: true})
	}
	return modes
}

type equivalenceHashSink struct {
	hash interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
	bytes int64
	lines int64
}

func newEquivalenceHashSink() *equivalenceHashSink {
	return &equivalenceHashSink{hash: sha256.New()}
}

func (s *equivalenceHashSink) Write(data []byte) (int, error) {
	s.bytes += int64(len(data))
	for _, b := range data {
		if b == '\n' {
			s.lines++
		}
	}
	return s.hash.Write(data)
}

type equivalenceSinkSet struct {
	sinks map[string]*equivalenceHashSink
}

func (set *equivalenceSinkSet) summary() map[string]any {
	names := make([]string, 0, len(set.sinks))
	for name := range set.sinks {
		names = append(names, name)
	}
	sort.Strings(names)
	out := map[string]any{}
	for _, name := range names {
		sink := set.sinks[name]
		out[name] = map[string]any{"bytes": sink.bytes, "lines": sink.lines, "sha256": hex.EncodeToString(sink.hash.Sum(nil))}
	}
	return out
}

// TestMasterEquivalenceDigest writes a digest of every Master lookup table the processor loaded, so the ID
// conversion data can be compared between source trees without printing any name.
func TestMasterEquivalenceDigest(t *testing.T) {
	inputs, enabled := equivalenceEnvironment(t)
	if !enabled {
		t.Skip("WWLOGS_EQUIVALENCE_OUT is not set")
	}
	masters, err := loadGameMasterDatabase(inputs.master, inputs.locale)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	v := reflect.ValueOf(masters).Elem()
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		field := v.Field(i)
		if name == "databasePath" {
			continue
		}
		switch field.Kind() {
		case reflect.Map, reflect.Slice, reflect.Struct, reflect.String:
			var builder strings.Builder
			equivalenceCanonical(field, &builder, 0)
			entry := map[string]any{"sha256": equivalenceDigest([]byte(builder.String()))}
			if field.Kind() == reflect.Map || field.Kind() == reflect.Slice {
				entry["len"] = field.Len()
			}
			out[name] = entry
		}
	}
	directory := filepath.Join(inputs.out, "master")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "master-digest.json"), equivalenceMarshal(t, out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// equivalenceCanonical renders a value deterministically: maps are sorted by their rendered key and
// pointers are followed, so two processes produce the same text for the same data.
func equivalenceCanonical(v reflect.Value, b *strings.Builder, depth int) {
	if depth > 12 {
		b.WriteString("<depth>")
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		equivalenceCanonical(v.Elem(), b, depth+1)
	case reflect.Map:
		type pair struct{ key, value string }
		pairs := make([]pair, 0, v.Len())
		for _, key := range v.MapKeys() {
			var kb, vb strings.Builder
			equivalenceCanonical(key, &kb, depth+1)
			equivalenceCanonical(v.MapIndex(key), &vb, depth+1)
			pairs = append(pairs, pair{kb.String(), vb.String()})
		}
		sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
		b.WriteString("{")
		for _, p := range pairs {
			b.WriteString(p.key + ":" + p.value + ",")
		}
		b.WriteString("}")
	case reflect.Slice, reflect.Array:
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			equivalenceCanonical(v.Index(i), b, depth+1)
			b.WriteString(",")
		}
		b.WriteString("]")
	case reflect.Struct:
		b.WriteString("(")
		for i := 0; i < v.NumField(); i++ {
			b.WriteString(v.Type().Field(i).Name + "=")
			equivalenceCanonical(v.Field(i), b, depth+1)
			b.WriteString(";")
		}
		b.WriteString(")")
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		b.WriteString("<" + v.Kind().String() + ">")
	default:
		b.WriteString(fmt.Sprint(v))
	}
}
