package devcapture

import (
	"io"

	"wwlogs/internal/capture"
)

// processor is the Development view of a core processor: the core, read through its exported formal data, and the
// Development state the core never reads. It is the core's observer (capture.Observer): the core hands it every
// observation point, and it writes the console lines, the research and diagnostic outputs and their records.
type processor struct {
	*capture.Processor
	dev processorDiagnostics
}

var _ capture.Observer = (*processor)(nil)

// newProcessor is a core processor observed by its Development diagnostics: stdout receives the console lines,
// diagnostic the operational diagnostic stream and audit the event audit rows.
func newProcessor(rsaKey *capture.StaticRSA, stdout, diagnostic, audit io.Writer) *processor {
	return observeProcessor(capture.NewProcessor(rsaKey, diagnostic), stdout, audit)
}

// observeProcessor attaches Development diagnostics to a core processor that another part of the product opened, such
// as the processor of a Desktop live session.
func observeProcessor(core *capture.Processor, stdout, audit io.Writer) *processor {
	p := &processor{Processor: core, dev: newProcessorDiagnostics(stdout, audit)}
	core.Observe(p)
	return p
}

// BeginProtocolSession is the handshake of a new protocol session.
func (p *processor) BeginProtocolSession() {
	p.dev.beginProtocolSession()
}

// processorDiagnostics is the Development state of a processor (processor.dev): the console and audit writers, the
// research switches and schema, the raw diagnostic and research sinks, the records the research outputs keep, and their
// error counters. Only Development code reads or writes it; the core reaches the Development code only through its
// observation points (capture.Observer).
type processorDiagnostics struct {
	// stdout receives the console lines (damage results, combat effects) and audit the event audit rows (-audit-out).
	stdout io.Writer
	audit  io.Writer
	// research writes per-event evidence lines to the diagnostic stream (-research); the effect switches select the
	// console effect lines (-show-effect-events, -show-all-effect-events).
	research            bool
	showEffectEvents    bool
	showAllEffectEvents bool
	// protocolSchema is the optional research schema (-protocol-schema) of the research decoders.
	protocolSchema *protocolSchema

	// Raw diagnostic and research sinks, each set by its output flag (run.go) or by the live session spool
	// (session_diagnostics.go).
	encounterSummary  io.Writer
	combatEventsJSONL io.Writer
	buffSnapshots     io.Writer
	buffLifecycle     io.Writer
	combatNotifyAudit io.Writer
	frameArchive      io.Writer
	schemaDecodeOut   io.Writer
	partRefOut        io.Writer
	roleConfigOut     io.Writer
	hpStateOut        io.Writer
	damagePushOut     io.Writer
	clientPushOut     io.Writer
	bulletRefOut      io.Writer
	shieldRefOut      io.Writer
	skillRefOut       io.Writer
	summonOwnerOut    io.Writer
	combatStateOut    io.Writer
	encounterOut      io.Writer

	// Frame and event ordinals of the research records and event envelopes (event_envelope.go).
	frameOrdinal    uint64
	eventOrderFrame uint64
	auxEventOrders  map[string]*auxEventOrder
	eventOrder      int

	// Research records: bullet handles, outbound skill requests, timer flow time, the reported role, Buff intervals and
	// damage overlap, and the encounter summaries with their pending credits.
	bulletHandles         map[bulletHandleKey]bulletHandleEntry
	bulletHandleOrder     []bulletHandleKey
	combatActionRequests  map[combatActionRequestKey]combatActionRequest
	combatActionOrder     []combatActionRequestKey
	flowTime              int64
	hasFlowTime           bool
	flowTimeFrame         uint64
	timerObservations     []timerObservation
	activeRoleID          int64
	hasActiveRole         bool
	buffIntervals         []*buffInterval
	buffIntervalOpen      map[overlapKey]*buffInterval
	buffDuplicateApplies  int
	buffUnmatchedRemoves  int
	buffIntervalOverflow  int
	buffOverlapSkipped    int
	damageOverlap         []damageOverlapRecord
	damageOverlapOverflow int
	encounterSummaries    map[encounterSummaryKey]*encounterSummary
	// encounterTransitions holds the encounter transitions until the next encounter output point (-encounter-out).
	encounterTransitions      *encounterTransitionLog
	pendingSummaryCredits     map[uint64][]pendingSummaryCredit
	pendingSummaryCreditCount int

	// messagePaths caches the research wire path of each inner Buff lifecycle message (buff.go withMessagePath),
	// bounded by maxMessagePaths.
	messagePaths map[messagePathKey]string

	// Error counters of the research outputs, and the failed diagnostic writes by sink (diagnostic_isolation.go).
	frameArchiveErrors  int
	schemaDecodeErrors  int
	schemaDecodeSkipped int
	partRefErrors       int
	roleConfigErrors    int
	bulletRefErrors     int
	shieldRefErrors     int
	skillRefErrors      int
	summonOwnerErrors   int
	clientPushErrors    int
	damagePushErrors    int
	hpStateErrors       int
	combatStateErrors   int
	diagnosticFailures  map[string]int
}

// newProcessorDiagnostics is the Development state of a new processor: stdout receives the console lines and audit the
// event audit rows; the research record of outbound skill requests starts empty.
func newProcessorDiagnostics(stdout, audit io.Writer) processorDiagnostics {
	return processorDiagnostics{stdout: stdout, audit: audit, combatActionRequests: make(map[combatActionRequestKey]combatActionRequest)}
}

// beginProtocolSession forgets the timer flow time of the previous protocol session at a new handshake.
func (d *processorDiagnostics) beginProtocolSession() {
	d.flowTime, d.hasFlowTime, d.flowTimeFrame = 0, false, 0
}
