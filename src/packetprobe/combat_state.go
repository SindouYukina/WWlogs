package main

// combatStateSchemaVersion identifies the layout of combatStateRecord.
const combatStateSchemaVersion = 1

// combatStateAttribute names an attribute id carried by AttributeChangedNotify, RecoverPropChangedNotify or SquadPropChangedNotify.
type combatStateAttribute struct {
	Type         int64            `json:"type"`
	ProtocolName string           `json:"protocolName,omitempty"`
	PropertyKey  string           `json:"propertyKey,omitempty"`
	Name         string           `json:"name,omitempty"`
	Status       resolutionStatus `json:"status"`
	Evidence     string           `json:"evidence"`
	BaseValue    *int64           `json:"baseValue,omitempty"`
	CurrentValue *int64           `json:"currentValue,omitempty"`
	MaxValue     *int64           `json:"maxValue,omitempty"`
	Ratio        *int64           `json:"ratio,omitempty"`
}

// combatStateFsm carries a ChangeStateNotify with its state names where the master can give them.
type combatStateFsm struct {
	FsmID        int64                      `json:"fsmId"`
	FromState    int64                      `json:"fromState"`
	ToState      int64                      `json:"toState"`
	Status       resolutionStatus           `json:"status"`
	MachineCount int                        `json:"machineCount"`
	Machines     []string                   `json:"machines,omitempty"`
	FromName     string                     `json:"fromDevNameZhCn,omitempty"`
	ToName       string                     `json:"toDevNameZhCn,omitempty"`
	FromLabel    *combatEventLabelTextJSONL `json:"fromLabel,omitempty"`
	ToLabel      *combatEventLabelTextJSONL `json:"toLabel,omitempty"`
	Evidence     string                     `json:"evidence"`
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
}
