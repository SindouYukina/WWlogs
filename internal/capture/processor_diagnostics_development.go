//go:build wwdev

package capture

import "io"

// processorDiagnostics is the Development state of a processor (processor.dev): the console and audit writers, the
// research switches and schema, the raw diagnostic and research sinks, the records the research outputs keep, and their
// error counters. Only Development code reads or writes it; the core reaches it through the Development hooks
// (hooks_public.go lists them with their Public no-op), and a Public build has none of it
// (processor_diagnostics_public.go).
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

	// Raw diagnostic and research sinks, each set by its output flag (run_development.go) or by the live session spool
	// (session_diagnostics_development.go).
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

	// Frame and event ordinals of the research records and event envelopes (event_envelope_development.go).
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

	// Error counters of the research outputs, and the failed diagnostic writes by sink (diagnostic_isolation_development.go).
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

// BeginProtocolSession forgets the timer flow time of the previous protocol session at a new handshake.
func (d *processorDiagnostics) BeginProtocolSession() {
	d.flowTime, d.hasFlowTime, d.flowTimeFrame = 0, false, 0
}
