package capture

import (
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
)

// Observer receives the one-way observation points of a processor: every frame and its decrypted body, the research
// decoders' inputs, the combat results, effect rows and encounter signals the core produced. The Development
// diagnostics (wwlogs/internal/devcapture) implement it; a Public build composes none, so a processor has no observer
// and skips every point. Nothing flows back into a core decision: no method returns a value, an observer counts its
// own failures, and the values it receives are the core's (it changes only its own copies).
type Observer interface {
	// BeginProtocolSession is the handshake of a new protocol session.
	BeginProtocolSession()
	WriteStructuredEventJSONL(event any)
	WriteDamageEventOutputs(event CombatEvent, visible bool, sourceRelation, targetRelation PlayerRelation)
	RecordBuffSnapshotDiagnostics(frame GameFrame, record RawBuffSnapshot, request PendingActiveRequest, matched bool)
	ArchiveClientPushResearch(frame GameFrame, plain []byte)
	WriteBattleStateSignal(at time.Time, container string, signal PlayerBattleStateSignal, anchor string)
	WriteCombatEventRecord(record CombatEventRecord, assignment string)
	FlushEncounterTransitions()
	WriteBuffLifecycleConsole(row BuffLifecycleAuditRow)
	WriteBuffLifecycleRow(row BuffLifecycleAuditRow)
	AuditCombatNotify(frame GameFrame, notification ProtoField, commonEntityID, commonOriginator, commonMessageID *uint64)
	ObserveBuffInterval(row BuffLifecycleAuditRow)
	ArchiveFrame(frame GameFrame)
	ArchiveUndecodedSchema(frame GameFrame)
	ArchiveSchemaDecode(frame GameFrame, plain []byte)
	ArchiveDamageRPCSchema(frame GameFrame, plain []byte)
	ArchiveHPStateFrame(frame GameFrame, plain []byte)
	ArchiveCombatStateFrame(frame GameFrame, plain []byte)
	ArchiveDamagePushFrame(frame GameFrame, plain []byte)
	ArchiveBulletRefReceiveFrame(frame GameFrame, plain []byte)
	ArchiveSummonOwnerFrame(frame GameFrame, plain []byte)
	BackfillAvailableSummaryCredits()
	ResetPendingSummaryCredits()
	BackfillEncounterSummary(sourceID, roleID uint64)
	RemovePendingSummaryCredits(sourceID uint64)
	WritePartReference(frame GameFrame, rpcID uint64, request PendingDamageRequest)
	WriteNoAmountEvent(rpcID uint64, request PendingDamageRequest, hasAmount bool, errorCode, immuneType int32, hasChangeLife bool, changeLife int32)
	ObserveEncounterSummary(event CombatEvent)
}

// Observe attaches the observer of the processor's observation points (nil detaches it). An application composes it
// before the first packet; the processor never decides anything from it.
func (p *Processor) Observe(observer Observer) {
	p.observer = observer
}

// SessionDiagnostics records a live session for Development diagnostics (wwlogs/internal/devcapture): it attaches its
// observer to the session's processor, receives every captured packet for its PCAP, observes the effects view, and
// reports its outcome when the session ends. A Public build composes none.
type SessionDiagnostics interface {
	Attach(p *Processor)
	Packet(info gopacket.CaptureInfo, data []byte)
	ObserveEffectDisplay(now time.Time)
	Finish(stopReason string, kernelDropped CaptureCounter) string
}

// SessionDiagnosticsOpener opens the Development diagnostics of a live session for the link type of its packets. A
// failure wrapped in DiagnosticSetupError leaves the session running without diagnostics.
type SessionDiagnosticsOpener func(link layers.LinkType) (SessionDiagnostics, error)
