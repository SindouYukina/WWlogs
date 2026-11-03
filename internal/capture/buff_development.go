//go:build wwdev

package capture

import (
	"fmt"
	"strings"
)

func (p *processor) writeBuffLifecycleConsole(row buffLifecycleAuditRow) {
	if p == nil || !p.showEffectEvents || p.stdout == nil || row.consoleSuppressed {
		return
	}
	line := formatBuffLifecycleConsole(row)
	if fields := p.effectSemanticConsoleFields(row); fields != "" {
		line += " " + fields
	}
	if _, err := fmt.Fprintln(p.stdout, line); err != nil {
		if p.diagnostic != nil {
			fmt.Fprintf(p.diagnostic, "effect_event_stdout_error=%v\n", err)
		}
		return
	}
	if flusher, ok := p.stdout.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil && p.diagnostic != nil {
			fmt.Fprintf(p.diagnostic, "effect_event_stdout_flush_error=%v\n", err)
		}
	}
}

func formatBuffLifecycleConsole(row buffLifecycleAuditRow) string {
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
	if row.EvidenceSource == EffectEvidenceClientReport {
		// The local client's own report: authoritative for its team, never acknowledged by the server.
		parts = append(parts, "evidence=CLIENT_REPORTED")
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
		if holder, ok := clientEffectHolder(row); ok {
			parts = append(parts, fmt.Sprintf("holder=%d", holder), "holderStatus="+string(resolutionCorrelated))
		} else {
			appendID("holderCandidate", row.CombatCommonEntityID)
			parts = append(parts, "holderStatus="+string(resolutionUnknown))
		}
		appendID("instigator", row.InstigatorID)
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
	appendID("instigator", row.InstigatorID)
	if row.TargetEntityID != nil {
		parts = append(parts, "targetStatus="+string(row.TargetStatus))
	} else if row.CombatCommonEntityID != nil {
		switch row.EventKind {
		case "BUFF_DURATION_CHANGE", "BUFF_STACK_COUNT_CHANGE":
			label, status := "holderCandidate", resolutionUnknown
			if row.HandleLinkedStatus == resolutionCorrelated {
				label, status = "holder", resolutionCorrelated
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
