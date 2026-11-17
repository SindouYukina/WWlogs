package devcapture

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"wwlogs/internal/capture"
)

// effectSemanticConsoleFields describes the static definition of the effect a lifecycle row
// is about: the id from an APPLY, or the id inherited through the handle link. Only an exact
// buff row is described, the official name is always reported unresolved, and developer tags
// are labelled internal because they are unofficial simplified-Chinese text.
func (p *processor) effectSemanticConsoleFields(row capture.BuffLifecycleAuditRow) string {
	var effectID uint64
	switch {
	case (row.EventKind == "GAMEPLAY_EFFECT_APPLY" || row.EventKind == "CLIENT_EFFECT_APPLY_REPORT") && row.ID != nil:
		effectID = *row.ID
	case row.HandleLinkedID != nil && row.HandleLinkedStatus == capture.ResolutionCorrelated:
		effectID = *row.HandleLinkedID
	default:
		return ""
	}
	if p == nil || !p.Masters.HasBuffDefinition(effectID) {
		return fmt.Sprintf("effectId=%d semantic=%s", effectID, capture.EffectSemanticDefinitionUnresolved)
	}
	info := p.EffectStaticInfo(effectID)
	if info == nil {
		info = &capture.EffectStaticInfo{}
	}
	fields := formatEffectSemanticFields(effectID, info)
	if owner := featureOwner(capture.ResolveBuffOwners(p.Masters, effectID)); owner != nil {
		fields += fmt.Sprintf(" owner=%q ownerKind=%s", owner.Name, owner.Kind)
	}
	return fields
}

// featureOwner returns the first named game feature that lists the effect id. A role observation is
// skipped: it names only the role that carried the effect, not the feature that grants it.
func featureOwner(owners *capture.BuffOwnersJSON) *capture.BuffOwnerText {
	if owners == nil {
		return nil
	}
	for i := range owners.Owners {
		if owners.Owners[i].Kind != "role_observation" {
			return &owners.Owners[i]
		}
	}
	return nil
}

// formatEffectSemanticFields renders the static definition of a linked effect id.
func formatEffectSemanticFields(effectID uint64, info *capture.EffectStaticInfo) string {
	semantic := capture.EffectSemanticNameUnresolved
	if info.Term != nil {
		semantic = capture.EffectSemanticNameCorrelated
	}
	parts := []string{fmt.Sprintf("effectId=%d", effectID), "semantic=" + string(semantic)}
	if info.Term != nil {
		parts = append(parts, fmt.Sprintf("name=%q nameStatus=%s nameSource=Term%d", info.Term.Name, info.Term.Status, info.Term.TermID))
	}
	if info.StaticDurationSeconds != nil {
		parts = append(parts, fmt.Sprintf("staticDuration_s=%.3f", *info.StaticDurationSeconds))
	}
	if info.StackLimit != nil {
		parts = append(parts, fmt.Sprintf("staticStackLimit=%d", *info.StackLimit))
	}
	if readable := readableDeveloperTags(info.DeveloperTags); len(readable) > 0 {
		parts = append(parts, "devTag[internal]="+strings.Join(readable, "|"))
	} else if len(info.DeveloperTags) > 0 {
		parts = append(parts, fmt.Sprintf("devTag[internal]=unreadable(%d)", len(info.DeveloperTags)))
	}
	return strings.Join(parts, " ")
}

// readableDeveloperTags keeps tag paths that have no hashed segment. A segment of eight
// lowercase hexadecimal digits is a hashed name that no table can translate.
func readableDeveloperTags(tags []string) []string {
	var readable []string
	for _, tag := range tags {
		hashed := false
		for _, segment := range strings.Split(tag, ".") {
			if len(segment) == 8 && strings.Trim(segment, "0123456789abcdef") == "" {
				hashed = true
				break
			}
		}
		if !hashed {
			readable = append(readable, tag)
		}
	}
	return readable
}

// writeEffectState writes the final tracker snapshot as one JSON line.
func (p *processor) writeEffectState(w io.Writer) error {
	snapshot := p.EffectSnapshotAtLastFrame()
	line, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode effect state: %w", err)
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write effect state: %w", err)
	}
	return nil
}
