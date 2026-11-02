//go:build wwdev

package capture

import (
	"fmt"
	"strings"
)

func (p *processor) writeBuffLifecycleConsole(row buffLifecycleAuditRow) {
	if p == nil || !p.showEffectEvents || p.stdout == nil {
		return
	}
	if _, err := fmt.Fprintln(p.stdout, formatBuffLifecycleConsole(row)); err != nil {
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
	appendID := func(label string, value *uint64) {
		if value != nil {
			parts = append(parts, fmt.Sprintf("%s=%d", label, *value))
		}
	}
	appendID("buffId", row.BuffID)
	appendID("id", row.ID)
	appendID("handle", row.HandleID)
	appendID("handleLinkedId", row.HandleLinkedID)
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
	appendID("stack", row.StackCount)
	if row.DurationSeconds != nil {
		parts = append(parts, fmt.Sprintf("duration_s=%.3f", *row.DurationSeconds))
	}
	if row.RemainingDuration != nil {
		parts = append(parts, fmt.Sprintf("remaining_s=%.3f", *row.RemainingDuration))
	}
	if row.BuffDefinitionStatus != "" {
		parts = append(parts, "definitionStatus="+string(row.BuffDefinitionStatus))
	}
	if row.GenericIDDefinitionStatus != "" {
		parts = append(parts, "idReferenceStatus="+string(row.GenericIDDefinitionStatus))
	}
	return strings.Join(parts, " ")
}
