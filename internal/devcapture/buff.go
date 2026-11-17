package devcapture

import (
	"fmt"
	"strings"
	"time"

	"wwlogs/internal/capture"
)

type buffSnapshotAuditRow struct {
	SchemaVersion     int                      `json:"schemaVersion"`
	EventType         string                   `json:"eventType"`
	SessionNumber     int                      `json:"sessionNumber"`
	MessageID         uint16                   `json:"messageId"`
	Timestamp         time.Time                `json:"timestamp"`
	ResponseCommandID uint16                   `json:"responseCommandId"`
	ResponseDirection string                   `json:"responseDirection"`
	RequestCommandID  uint16                   `json:"requestCommandId"`
	FrameToken        uint16                   `json:"frameToken"`
	EntityID          *uint64                  `json:"entityId,omitempty"`
	EntityIDStatus    capture.ResolutionStatus `json:"entityIdStatus"`
	ComponentPath     string                   `json:"componentPath"`
	// BuffIDStatus records the captured field mapping, not static-definition resolution.
	BuffIDStatus capture.ResolutionStatus `json:"buffIdStatus"`
	// BuffDefinitionStatus records an exact link from BuffId to the normalized static buff table.
	BuffDefinitionStatus   capture.ResolutionStatus `json:"buffDefinitionStatus"`
	BuffDefinitionTable    string                   `json:"buffDefinitionTable,omitempty"`
	BuffDefinitionID       *uint64                  `json:"buffDefinitionId,omitempty"`
	BuffDefinitionEvidence string                   `json:"buffDefinitionEvidence"`
	// BuffOwners names the features that list this BuffId in an explicit reference table; the Buff has no name of its own.
	BuffOwners *capture.BuffOwnersJSON `json:"buffOwners,omitempty"`
	// BuffSemanticStatus covers runtime application and effect semantics, not static row lookup.
	BuffSemanticStatus        capture.ResolutionStatus `json:"buffSemanticStatus"`
	BuffSemanticEvidence      string                   `json:"buffSemanticEvidence"`
	InstigatorSemantics       capture.ResolutionStatus `json:"instigatorSemantics"`
	InstigatorEvidence        string                   `json:"instigatorEvidence,omitempty"`
	ReferenceSchema           string                   `json:"referenceSchema"`
	WindowsSchemaVerification string                   `json:"windowsSchemaVerification"`
	Buff                      capture.RawBuffSnapshot  `json:"buff"`
}

const buffSnapshotSchemaVersion = 4

// fightBuffSnapshotPath is the wire path of the buff records an EntityActiveResponse carries.
var fightBuffSnapshotPath = fmt.Sprintf("EntityActiveResponse.field%d(ComponentPbs[])->EntityComponentPb.field%d(FightBuffComponent)->FightBuffComponentPb.field%d(FightBuffInfos[])->FightBuffInformation",
	capture.FieldEntityActiveResponseComponentPbs, capture.FieldEntityComponentPbFightBuffComponent, capture.FieldFightBuffComponentPbFightBuffInfos)

// RecordBuffSnapshotDiagnostics writes the research row of one Buff record of an EntityActiveResponse (the Buff snapshot
// output, the event line and the encounter summary) after the core has read it (observeEffectSnapshot). The row exists
// only when one of those outputs does. A failed row must not skip the identity, FSM and part components of the same
// response, so it is counted (diagnostic_isolation.go) and the response handler continues.
func (p *processor) RecordBuffSnapshotDiagnostics(frame capture.GameFrame, record capture.RawBuffSnapshot, request capture.PendingActiveRequest, matched bool) {
	if p.dev.buffSnapshots == nil && p.dev.combatEventsJSONL == nil && p.dev.encounterSummary == nil {
		return
	}
	row := buffSnapshotAuditRow{
		SchemaVersion: buffSnapshotSchemaVersion, EventType: "BUFF_SNAPSHOT", SessionNumber: p.SessionNumber,
		MessageID: frame.CommandID, Timestamp: frame.Timestamp, ResponseCommandID: frame.CommandID,
		ResponseDirection: frame.Direction, RequestCommandID: capture.CmdEntityActiveRequest,
		FrameToken: frame.Token, EntityIDStatus: capture.ResolutionUnknown,
		ComponentPath:             fightBuffSnapshotPath,
		BuffIDStatus:              capture.ResolutionCorrelated,
		BuffDefinitionStatus:      capture.ResolutionUnknown,
		BuffDefinitionEvidence:    "No exact normalized buff.id row has been linked; the raw BuffId remains unchanged",
		BuffSemanticStatus:        capture.ResolutionUnknown,
		BuffSemanticEvidence:      "This status covers runtime application and effect semantics; static row lookup is reported separately, and snapshot presence does not establish application or uptime",
		ReferenceSchema:           capture.CurrentProtocol(),
		WindowsSchemaVerification: capture.ProtocolSchemaBasis() + "; client/master compatibility UNKNOWN",
		Buff:                      record,
	}
	if record.HasID && p.Masters != nil && p.Masters.HasBuffDefinition(record.ID) {
		definitionID := record.ID
		row.BuffDefinitionStatus = capture.ResolutionCorrelated
		row.BuffDefinitionTable = "buff"
		row.BuffDefinitionID = &definitionID
		row.BuffDefinitionEvidence = "FightBuffInformation.BuffId follows the client's BuffById lookup; exact normalized buff.id row found. This links static attributes only and does not establish runtime application or effect semantics"
		row.BuffOwners = capture.ResolveBuffOwners(p.Masters, record.ID)
	} else if record.HasID {
		// No static definition: only an observed-role owner can exist; the definition status stays UNKNOWN.
		row.BuffOwners = capture.ResolveBuffOwners(p.Masters, record.ID)
	}
	if record.HasInstigatorID {
		row.InstigatorSemantics = capture.ResolutionCorrelated
		row.InstigatorEvidence = "Client code (static research: CharacterBuffComponent, ActiveBuff) compares InstigatorId with CreatureDataId and resolves it through CreatureModel.GetEntity; client/master compatibility UNKNOWN"
	}
	if matched && request.HasEntityID {
		entityID := request.EntityID
		row.EntityID = &entityID
		row.EntityIDStatus = capture.ResolutionCorrelated
	}
	p.observeBuffSnapshot(row)
	if p.dev.buffSnapshots != nil {
		p.writeDiagnosticRow(p.dev.buffSnapshots, "buff-snapshots", row)
	}
	p.WriteStructuredEventJSONL(row)
}

func (p *processor) WriteBuffLifecycleConsole(row capture.BuffLifecycleAuditRow) {
	if p == nil || !p.dev.showEffectEvents || p.dev.stdout == nil {
		return
	}
	if !p.dev.showAllEffectEvents && !combatConsoleVisible(row) {
		return
	}
	// The verified character names are resolved at print time.
	var holderName, sourceName string
	if holder, ok := capture.LifecycleEntity(&row); ok {
		holderName = p.verifiedCharacterName(holder)
	}
	if row.InstigatorID != nil {
		sourceName = p.verifiedCharacterName(*row.InstigatorID)
	}
	line := formatBuffLifecycleConsole(row, holderName, sourceName)
	if fields := p.effectSemanticConsoleFields(row); fields != "" {
		line += " " + fields
	}
	// The effect lines share the console sink with the damage lines (writeDamageEventOutputs) and its failure count.
	if _, err := fmt.Fprintln(p.dev.stdout, line); err != nil {
		p.noteDiagnosticFailure("console", err)
		return
	}
	if flusher, ok := p.dev.stdout.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			p.noteDiagnosticFailure("console", err)
		}
	}
}

// verifiedCharacterName returns the character name of a runtime entity when both the character link
// and the name are verified, and an empty string otherwise.
func (p *processor) verifiedCharacterName(entityID uint64) string {
	if p == nil || p.Entities == nil || entityID == 0 {
		return ""
	}
	identity := capture.ResolveEntityIdentity(p.Entities, p.Masters, entityID)
	if !identity.HasCharacter || identity.CharacterStatus != capture.ResolutionVerified || identity.Character.NameStatus != string(capture.ResolutionVerified) {
		return ""
	}
	return identity.Character.Name
}

// combatConsoleVisible is the default console policy: a lifecycle change of a combat effect that
// involves the local team. Everything else stays in the tracker and the JSONL and is printed only with
// -show-all-effect-events: unknown and non-combat effects, effects between non-player entities,
// instant effects (no lifecycle to show), server buff requests and the client's answers (the apply or
// remove they cause is printed instead), duration refreshes and stack messages that change nothing.
func combatConsoleVisible(row capture.BuffLifecycleAuditRow) bool {
	switch row.EventKind {
	case "GAMEPLAY_EFFECT_APPLY", "CLIENT_EFFECT_APPLY_REPORT":
		if row.HandleID == nil || !capture.ValidEffectHandle(*row.HandleID) {
			return false
		}
	case "GAMEPLAY_EFFECT_REMOVE", "CLIENT_EFFECT_REMOVE_REPORT":
	case "BUFF_STACK_COUNT_CHANGE", "CLIENT_EFFECT_STACK_REPORT":
		if row.ConsoleSuppressed {
			return false
		}
	default:
		return false
	}
	return row.CombatClass == capture.EffectCombatClassCombat && row.PlayerRelated
}

func formatBuffLifecycleConsole(row capture.BuffLifecycleAuditRow, holderName, sourceName string) string {
	parts := []string{"BuffEvent"}
	if !row.Timestamp.IsZero() {
		parts = append(parts, row.Timestamp.Local().Format("15:04:05.000"))
	}
	if row.EventKind != "" {
		parts = append(parts, "kind="+row.EventKind)
	}
	switch row.EventKind {
	case "BUFF_APPLY_REQUEST_NOTIFY", "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY":
		parts = append(parts, "effectState=UNCONFIRMED")
	}
	if row.EvidenceSource == capture.EffectEvidenceClientReport {
		// The local client's own report: authoritative for its team, never acknowledged by the server.
		parts = append(parts, "evidence=CLIENT_REPORTED")
	}
	if row.CombatClass != "" {
		parts = append(parts, "combatClass="+strings.ToUpper(string(row.CombatClass)), "combatBasis="+row.CombatClassBasis,
			fmt.Sprintf("localTeam=%t", row.PlayerRelated))
	}
	appendID := func(label string, value *uint64) {
		if value != nil {
			parts = append(parts, fmt.Sprintf("%s=%d", label, *value))
		}
	}
	appendID("buffId", row.BuffID)
	appendID("id", row.ID)
	appendID("handle", row.HandleID)
	appendID("handleLinkedId", row.HandleLinkedID)
	switch row.EventKind {
	case "CLIENT_EFFECT_APPLY_REPORT", "CLIENT_EFFECT_STACK_REPORT", "CLIENT_EFFECT_REMOVE_REPORT":
		if holder, ok := capture.ClientEffectHolder(row); ok {
			parts = append(parts, fmt.Sprintf("holder=%d", holder), "holderStatus="+string(capture.ResolutionCorrelated))
			if holderName != "" {
				parts = append(parts, "holderName="+holderName)
			}
		} else {
			appendID("holderCandidate", row.CombatCommonEntityID)
			parts = append(parts, "holderStatus="+string(capture.ResolutionUnknown))
		}
		appendID("instigator", row.InstigatorID)
		if row.InstigatorID != nil && sourceName != "" {
			parts = append(parts, "instigatorName="+sourceName)
		}
		if row.HandleLinkedStatus != "" {
			parts = append(parts, "handleLinkedStatus="+string(row.HandleLinkedStatus))
		}
		if row.EventKind == "CLIENT_EFFECT_REMOVE_REPORT" {
			removal := "EXPIRED"
			if row.IsPrematureRemoval != nil && *row.IsPrematureRemoval {
				removal = "PREMATURE"
			}
			parts = append(parts, "removal="+removal)
		}
		appendID("stack", row.StackCount)
		if row.DurationSeconds != nil {
			parts = append(parts, fmt.Sprintf("duration_s=%.3f", *row.DurationSeconds))
		}
		if row.GenericIDDefinitionStatus != "" {
			parts = append(parts, "idReferenceStatus="+string(row.GenericIDDefinitionStatus))
		}
		return strings.Join(parts, " ")
	case "BUFF_APPLY_REQUEST_RESPONSE", "BUFF_REMOVE_BY_ID_REQUEST_RESPONSE":
		appendID("commonEntity", row.CombatCommonEntityID)
		appendID("requestId", row.RequestLinkedID)
		parts = append(parts, "requestLinkStatus="+string(row.RequestLinkStatus))
		// ErrorCode 0 is Success in the protocol enum and is omitted on the wire.
		if row.ErrorCode == nil || *row.ErrorCode == 0 {
			parts = append(parts, "result=ACCEPTED")
		} else {
			parts = append(parts, "result=ERROR")
			appendID("errorCode", row.ErrorCode)
		}
		if row.IsActive != nil {
			parts = append(parts, fmt.Sprintf("active=%t", *row.IsActive))
		}
		return strings.Join(parts, " ")
	}
	appendID("target", row.TargetEntityID)
	if row.TargetEntityID != nil && holderName != "" {
		parts = append(parts, "targetName="+holderName)
	}
	appendID("instigator", row.InstigatorID)
	if row.InstigatorID != nil && sourceName != "" {
		parts = append(parts, "instigatorName="+sourceName)
	}
	if row.TargetEntityID != nil {
		parts = append(parts, "targetStatus="+string(row.TargetStatus))
	} else if row.CombatCommonEntityID != nil {
		switch row.EventKind {
		case "BUFF_DURATION_CHANGE", "BUFF_STACK_COUNT_CHANGE":
			label, status := "holderCandidate", capture.ResolutionUnknown
			if row.HandleLinkedStatus == capture.ResolutionCorrelated {
				label, status = "holder", capture.ResolutionCorrelated
			}
			parts = append(parts,
				fmt.Sprintf("%s=%d", label, *row.CombatCommonEntityID),
				"holderStatus="+string(status),
			)
		default:
			appendID("commonEntity", row.CombatCommonEntityID)
		}
	}
	if row.HandleLinkedStatus != "" {
		parts = append(parts, "handleLinkedStatus="+string(row.HandleLinkedStatus))
	}
	if row.StackCount != nil && !(row.EventKind == "BUFF_REMOVE_BY_ID_REQUEST_NOTIFY" && *row.StackCount == ^uint64(0)) {
		appendID("stack", row.StackCount)
	}
	if row.DurationSeconds != nil {
		parts = append(parts, fmt.Sprintf("duration_s=%.3f", *row.DurationSeconds))
	}
	if row.RemainingDuration != nil {
		parts = append(parts, fmt.Sprintf("remaining_s=%.3f", *row.RemainingDuration))
	}
	if row.BuffDefinitionStatus != "" {
		parts = append(parts, "buffIdLookup="+string(row.BuffDefinitionStatus))
	}
	if row.GenericIDDefinitionStatus != "" {
		parts = append(parts, "idReferenceStatus="+string(row.GenericIDDefinitionStatus))
	}
	return strings.Join(parts, " ")
}

// withMessagePath is a lifecycle row with the wire path of its inner message, for the research outputs. The core
// records only the raw facts: the outer command (CommandID) and the field of the inner message
// (InnerMessageFieldNumber). A client report reaches the core inside CombatSendPackRequest pushes; every other inner
// message, in CombatReceivePackNotify or nested in CombatSendPackResponse, has the notify path, as before the path
// left the core.
func withMessagePath(row capture.BuffLifecycleAuditRow) capture.BuffLifecycleAuditRow {
	if row.InnerMessageFieldNumber == nil {
		return row
	}
	fieldNumber := *row.InnerMessageFieldNumber
	if row.CommandID == capture.CmdCombatSendPackRequest {
		row.MessagePath = fmt.Sprintf("CombatSendPackRequest.field%d(CombatSendData[]).field%d(CombatPushData).field%d", capture.FieldCombatSendPackRequestData, capture.FieldCombatSendDataPush, fieldNumber)
	} else {
		row.MessagePath = fmt.Sprintf("CombatReceivePackNotify.field%d(CombatReceiveData[]).field%d(CombatNotifyData).field%d", capture.FieldCombatReceivePackNotifyData, capture.FieldCombatReceiveDataCombatNotifyData, fieldNumber)
	}
	return row
}
