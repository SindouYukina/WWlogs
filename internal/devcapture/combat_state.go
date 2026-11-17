package devcapture

import (
	"fmt"
	"sort"
	"strings"

	"wwlogs/internal/capture"
)

// fsmLabel returns the display text of a developer node name: the master locale text, else the zh-CN original.
func fsmLabel(m *capture.GameMaster, devName string) *capture.CombatEventLabelTextJSONL {
	if m == nil || m.FSM == nil || devName == "" {
		return nil
	}
	byLocale := m.FSM.Labels[devName]
	chosen, ok := byLocale[m.Locale]
	if !ok {
		chosen, ok = byLocale["zh-CN"]
	}
	if !ok || chosen.Text == "" {
		return nil
	}
	return &capture.CombatEventLabelTextJSONL{Locale: chosen.Locale, Text: chosen.Text, Kind: chosen.Kind,
		Evidence: "aistatemachine_node_name_locale row for this node name; an internal developer node name, not an official in-game text"}
}

// fsmDescendants returns every node uuid reachable from a node by child links, following reference links into the
// machines that define the referenced uuid.
func fsmDescendants(f *capture.FSMMaster, machine string, uuid int64) map[int64]struct{} {
	out := make(map[int64]struct{})
	seen := make(map[capture.FSMNodeKey]struct{})
	stack := []capture.FSMNodeKey{{Machine: machine, UUID: uuid}}
	for len(stack) > 0 {
		key := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, done := seen[key]; done {
			continue
		}
		seen[key] = struct{}{}
		for _, child := range f.Children[key] {
			out[child] = struct{}{}
			stack = append(stack, capture.FSMNodeKey{Machine: key.Machine, UUID: child})
		}
		if reference, ok := f.References[key]; ok {
			for _, other := range f.MachinesByNode[reference] {
				out[reference] = struct{}{}
				stack = append(stack, capture.FSMNodeKey{Machine: other, UUID: reference})
			}
		}
	}
	return out
}

// fsmResolution is the outcome of naming one state transition.
type fsmResolution struct {
	Status       capture.ResolutionStatus
	MachineCount int
	Machines     []string
	FromName     string
	ToName       string
	Evidence     string
}

const maxFsmMachinesShown = 5

// resolveFsmTransition names the two states of a ChangeStateNotify. FsmId and the state ids are node uuids, which are
// only unique inside one state-machine config, so the candidate machines are those that define the FsmId node and
// contain both states beneath it (the states alone when no machine does). The developer names are given only when all
// candidates agree on them; with several candidates that disagree, nothing is named.
func resolveFsmTransition(m *capture.GameMaster, fsmID, fromState, toState int64, entityHash, commonHash *int64) *fsmResolution {
	if m == nil || m.FSM == nil {
		return nil
	}
	candidates := make([]string, 0, 4)
	basis := "state machines that define the FsmId node and hold both states beneath it"
	allowed := map[string]struct{}(nil)
	// The entity's own config version (HashCode) and the common machine's version (CommonHashCode) narrow the machines
	// to the ones the entity runs; either hash alone is used when the other was not observed.
	for _, hash := range []*int64{entityHash, commonHash} {
		if hash == nil {
			continue
		}
		for _, machine := range m.FSM.MachinesByVersion[*hash] {
			if allowed == nil {
				allowed = make(map[string]struct{})
				basis = "state machines whose config Version equals the entity's EntityFsmComponentPb HashCode or CommonHashCode and that hold both states beneath the FsmId node"
			}
			allowed[machine] = struct{}{}
		}
	}
	for _, machine := range m.FSM.MachinesByNode[fsmID] {
		if allowed != nil {
			if _, ok := allowed[machine]; !ok {
				continue
			}
		}
		below := fsmDescendants(m.FSM, machine, fsmID)
		_, hasFrom := below[fromState]
		_, hasTo := below[toState]
		if hasFrom && hasTo {
			candidates = append(candidates, machine)
		}
	}
	if len(candidates) == 0 {
		// The states may live in the entity's own machine while the FsmId names a node of the common machine.
		pool := m.FSM.MachinesByNode[fsmID]
		basis = "state machines that define the FsmId node and both state nodes"
		if allowed != nil {
			pool = pool[:0:0]
			for machine := range allowed {
				pool = append(pool, machine)
			}
			basis = "state machines whose config Version equals the entity's EntityFsmComponentPb HashCode or CommonHashCode and that define both state nodes"
		}
		for _, machine := range pool {
			if _, hasFrom := m.FSM.Names[capture.FSMNodeKey{Machine: machine, UUID: fromState}]; !hasFrom {
				continue
			}
			if _, hasTo := m.FSM.Names[capture.FSMNodeKey{Machine: machine, UUID: toState}]; hasTo {
				candidates = append(candidates, machine)
			}
		}
	}
	sort.Strings(candidates)
	result := &fsmResolution{Status: capture.ResolutionUnknown, MachineCount: len(candidates)}
	result.Machines = candidates
	if len(result.Machines) > maxFsmMachinesShown {
		result.Machines = result.Machines[:maxFsmMachinesShown]
	}
	if len(candidates) == 0 {
		result.Evidence = "No state-machine config defines this FsmId node together with both state nodes; raw ids kept"
		return result
	}
	fromNames, toNames := map[string]struct{}{}, map[string]struct{}{}
	for _, machine := range candidates {
		fromNames[m.FSM.Names[capture.FSMNodeKey{Machine: machine, UUID: fromState}]] = struct{}{}
		toNames[m.FSM.Names[capture.FSMNodeKey{Machine: machine, UUID: toState}]] = struct{}{}
	}
	if len(fromNames) != 1 || len(toNames) != 1 {
		result.Evidence = fmt.Sprintf("%d state-machine configs fit this transition and they name the states differently; the entity's own machine is not known, so no name is given", len(candidates))
		return result
	}
	for name := range fromNames {
		result.FromName = name
	}
	for name := range toNames {
		result.ToName = name
	}
	result.Status = capture.ResolutionCorrelated
	result.Evidence = fmt.Sprintf("%d %s; all agree on the developer node names (zh-CN developer labels, not localized text)", len(candidates), basis)
	return result
}

// combatStateSchemaVersion identifies the layout of combatStateRecord.
const combatStateSchemaVersion = 1

// combatStateAttribute names an attribute id carried by AttributeChangedNotify, RecoverPropChangedNotify or SquadPropChangedNotify.
type combatStateAttribute struct {
	Type         int64                    `json:"type"`
	ProtocolName string                   `json:"protocolName,omitempty"`
	PropertyKey  string                   `json:"propertyKey,omitempty"`
	Name         string                   `json:"name,omitempty"`
	Status       capture.ResolutionStatus `json:"status"`
	Evidence     string                   `json:"evidence"`
	BaseValue    *int64                   `json:"baseValue,omitempty"`
	CurrentValue *int64                   `json:"currentValue,omitempty"`
	MaxValue     *int64                   `json:"maxValue,omitempty"`
	Ratio        *int64                   `json:"ratio,omitempty"`
}

// combatStateFsm carries a ChangeStateNotify with its state names where the master can give them.
type combatStateFsm struct {
	FsmID int64 `json:"fsmId"`
	// EntityFsmHashCode is the entity's EntityFsmComponentPb.HashCode when the entity's FSM component was observed.
	EntityFsmHashCode       *int64                             `json:"entityFsmHashCode,omitempty"`
	EntityFsmCommonHashCode *int64                             `json:"entityFsmCommonHashCode,omitempty"`
	FromState               int64                              `json:"fromState"`
	ToState                 int64                              `json:"toState"`
	Status                  capture.ResolutionStatus           `json:"status"`
	MachineCount            int                                `json:"machineCount"`
	Machines                []string                           `json:"machines,omitempty"`
	FromName                string                             `json:"fromDevNameZhCn,omitempty"`
	ToName                  string                             `json:"toDevNameZhCn,omitempty"`
	FromLabel               *capture.CombatEventLabelTextJSONL `json:"fromLabel,omitempty"`
	ToLabel                 *capture.CombatEventLabelTextJSONL `json:"toLabel,omitempty"`
	Evidence                string                             `json:"evidence"`
}

// combatStateBuff carries an ActivateBuffNotify: a switch of a buff handle that the message names only by handle.
type combatStateBuff struct {
	Handle         int64                    `json:"handle"`
	On             *bool                    `json:"on,omitempty"`
	LinkedBuffID   *uint64                  `json:"linkedBuffId,omitempty"`
	Status         capture.ResolutionStatus `json:"status"`
	BuffDefinition string                   `json:"buffDefinitionTable,omitempty"`
	BuffOwners     *capture.BuffOwnersJSON  `json:"buffOwners,omitempty"`
	Evidence       string                   `json:"evidence"`
}

// combatStateRecord is one attribute change, recovery value or state-machine transition of one entity.
type combatStateRecord struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	Kind            string                `json:"kind"`
	FrameOrdinal    uint64                `json:"frameOrdinal"`
	SessionNumber   int                   `json:"sessionNumber"`
	Timestamp       string                `json:"timestamp"`
	EntityID        uint64                `json:"entityId"`
	EventOrderInMsg int                   `json:"orderInMessage"`
	Attribute       *combatStateAttribute `json:"attribute,omitempty"`
	Fsm             *combatStateFsm       `json:"fsm,omitempty"`
	Buff            *combatStateBuff      `json:"buff,omitempty"`
}

func optionalScalar(message *decodedMessage, name string) *int64 {
	value, ok := scalarValue(message, name)
	if !ok {
		return nil
	}
	return &value
}

// resolveCombatStateAttribute names an attribute id. The protocol enum name and the PropertyIndex key must agree (ignoring case) for the id to be named.
func resolveCombatStateAttribute(m *capture.GameMaster, schema *protocolSchema, attributeType int64, enum string) combatStateAttribute {
	result := combatStateAttribute{Type: attributeType, Status: capture.ResolutionUnknown, Evidence: "No PropertyIndex row for this attribute id; raw id kept"}
	if schema != nil {
		if name, ok := schema.enumName(enum, attributeType); ok {
			result.ProtocolName = name
		}
	}
	if m == nil {
		return result
	}
	entry, ok := m.PropertyNames[attributeType]
	if !ok {
		return result
	}
	result.PropertyKey = entry.Key
	if result.ProtocolName != "" && !strings.EqualFold(result.ProtocolName, entry.Key) {
		result.Evidence = "PropertyIndex key differs from the protocol enum name for this id; no display name given"
		return result
	}
	if entry.Name == "" {
		result.Evidence = "PropertyIndex row has no localized name for the master locale; key kept"
		return result
	}
	result.Name = entry.Name
	result.Status = capture.ResolutionCorrelated
	result.Evidence = "AttributeType id has a PropertyIndex row whose key equals the protocol enum member name, ignoring case (client build observed, master compatibility unverified)"
	return result
}

// archiveCombatState writes every attribute change, recovery value and state transition of one decoded CombatReceivePackNotify.
func (p *processor) archiveCombatState(frame capture.GameFrame, decoded *decodedMessage) {
	if p.dev.combatStateOut == nil || decoded == nil {
		return
	}
	order := 0
	emit := func(record combatStateRecord) {
		record.SchemaVersion = combatStateSchemaVersion
		record.FrameOrdinal = p.dev.frameOrdinal - 1
		record.SessionNumber = p.SessionNumber
		record.Timestamp = formatTimestamp(frame.Timestamp)
		record.EventOrderInMsg = order
		order++
		// A record that cannot be encoded ends the message; a failed write is only counted.
		if !writeResearchRecord(p.dev.combatStateOut, record, &p.dev.combatStateErrors) {
			return
		}
	}
	for _, data := range childMessages(decoded, "Data") {
		for _, notify := range childMessages(data, "CombatNotifyData") {
			entityID, hasEntity := combatCommonEntityID(notify)
			if !hasEntity {
				continue
			}
			for _, change := range childMessages(notify, "AttributeChangedNotify") {
				for _, attribute := range childMessages(change, "Attributes") {
					typeValue, ok := scalarValue(attribute, "AttributeType")
					if !ok {
						continue
					}
					resolved := resolveCombatStateAttribute(p.Masters, p.dev.protocolSchema, typeValue, "EAttributeType")
					resolved.BaseValue, resolved.CurrentValue = optionalScalar(attribute, "BaseValue"), optionalScalar(attribute, "CurrentValue")
					emit(combatStateRecord{Kind: "ATTRIBUTE_CHANGED", EntityID: entityID, Attribute: &resolved})
				}
			}
			for _, recoverNotify := range childMessages(notify, "RecoverPropChangedNotify") {
				for _, attribute := range childMessages(recoverNotify, "Attributes") {
					typeValue, ok := scalarValue(attribute, "AttrId")
					if !ok {
						continue
					}
					resolved := resolveCombatStateAttribute(p.Masters, p.dev.protocolSchema, typeValue, "EAttributeType")
					resolved.CurrentValue, resolved.MaxValue, resolved.Ratio = optionalScalar(attribute, "CurrentValue"), optionalScalar(attribute, "MaxValue"), optionalScalar(attribute, "Ratio")
					emit(combatStateRecord{Kind: "RECOVER_PROP_CHANGED", EntityID: entityID, Attribute: &resolved})
				}
			}
			for _, squad := range childMessages(notify, "SquadPropChangedNotify") {
				for _, attribute := range childMessages(squad, "Attributes") {
					typeValue, ok := scalarValue(attribute, "AttributeType")
					if !ok {
						continue
					}
					resolved := combatStateAttribute{Type: typeValue, Status: capture.ResolutionUnknown, Evidence: "Squad attribute ids are a separate enum (ESquadAttributeType) with no PropertyIndex name; protocol name only"}
					if p.dev.protocolSchema != nil {
						if name, known := p.dev.protocolSchema.enumName("ESquadAttributeType", typeValue); known {
							resolved.ProtocolName = name
						}
					}
					resolved.BaseValue, resolved.CurrentValue = optionalScalar(attribute, "BaseValue"), optionalScalar(attribute, "CurrentValue")
					emit(combatStateRecord{Kind: "SQUAD_PROP_CHANGED", EntityID: entityID, Attribute: &resolved})
				}
			}
			for _, activate := range childMessages(notify, "ActivateBuffNotify") {
				handle, okHandle := scalarValue(activate, "Handle")
				if !okHandle {
					continue
				}
				record := combatStateBuff{Handle: handle, Status: capture.ResolutionUnknown,
					Evidence: "No earlier ApplyGameplayEffectNotify with this (session, entity, handle) was observed; the message names the buff only by handle"}
				if on, ok := scalarValue(activate, "On"); ok {
					value := on != 0
					record.On = &value
				}
				if entry, found := p.BuffHandles[capture.BuffHandleKey{Session: p.SessionNumber, EntityID: entityID, HandleID: uint64(handle)}]; found && entry.ID != 0 {
					linked := entry.ID
					record.LinkedBuffID = &linked
					record.Evidence = "Same (session, entity, handle) as an earlier ApplyGameplayEffectNotify; the Id is inherited from that apply message"
					if entry.Definition {
						record.Status = capture.ResolutionCorrelated
						record.BuffDefinition = "buff"
						record.BuffOwners = capture.ResolveBuffOwners(p.Masters, linked)
					} else {
						record.Evidence += "; that Id has no exact buff.id row"
					}
				}
				emit(combatStateRecord{Kind: "BUFF_ACTIVATED", EntityID: entityID, Buff: &record})
			}
			for _, change := range childMessages(notify, "ChangeStateNotify") {
				fsmID, okID := scalarValue(change, "FsmId")
				if !okID {
					continue
				}
				from, _ := scalarValue(change, "FromState")
				to, _ := scalarValue(change, "ToState")
				record := combatStateFsm{FsmID: fsmID, FromState: from, ToState: to, Status: capture.ResolutionUnknown,
					Evidence: "State-machine node identities are not loaded in the master; raw ids kept"}
				var entityHash, commonHash *int64
				if entity, ok := p.Entities.Entities[entityID]; ok {
					if entity.HasFsmHashCode {
						hash := entity.FSMHashCode
						entityHash = &hash
						record.EntityFsmHashCode = entityHash
					}
					if entity.HasFsmCommonHashCode {
						hash := entity.FSMCommonHashCode
						commonHash = &hash
						record.EntityFsmCommonHashCode = commonHash
					}
				}
				if resolution := resolveFsmTransition(p.Masters, fsmID, from, to, entityHash, commonHash); resolution != nil {
					record.Status, record.MachineCount, record.Machines = resolution.Status, resolution.MachineCount, resolution.Machines
					record.FromName, record.ToName, record.Evidence = resolution.FromName, resolution.ToName, resolution.Evidence
					record.FromLabel, record.ToLabel = fsmLabel(p.Masters, resolution.FromName), fsmLabel(p.Masters, resolution.ToName)
				}
				emit(combatStateRecord{Kind: "FSM_STATE_CHANGED", EntityID: entityID, Fsm: &record})
			}
		}
	}
}

// ArchiveCombatStateFrame decodes a combat frame for the combat-state output when that output is enabled.
func (p *processor) ArchiveCombatStateFrame(frame capture.GameFrame, plain []byte) {
	if p.dev.combatStateOut == nil || p.dev.protocolSchema == nil || (frame.CommandID != capture.CmdCombatReceivePackNotify && frame.CommandID != capture.CmdCombatSendPackResponse) {
		return
	}
	payloads, err := capture.ReceivePackNotifyBodies(frame.CommandID, plain)
	if err != nil {
		p.dev.combatStateErrors++
		return
	}
	for _, payload := range payloads {
		decoded, err := decodeSchemaMessage(p.dev.protocolSchema, "CombatReceivePackNotify", payload, 0)
		if err != nil {
			p.dev.combatStateErrors++
			continue
		}
		p.archiveCombatState(frame, decoded)
	}
}
