//go:build wwdev

package capture

func attributeMessage(attributeType, baseValue int64, withCurrent bool) decodedField {
	fields := []decodedField{
		{Number: FieldGameplayAttributeDataAttributeType, Name: "AttributeType", Kind: "int32", Value: attributeType},
		{Number: FieldGameplayAttributeDataBaseValue, Name: "BaseValue", Kind: "int32", Value: baseValue},
	}
	if withCurrent {
		fields = append(fields, decodedField{Number: FieldGameplayAttributeDataCurrentValue, Name: "CurrentValue", Kind: "int32", Value: baseValue})
	}
	return decodedField{Number: FieldAttributeChangedNotifyAttributes, Name: "Attributes", Kind: "class", Message: &decodedMessage{Type: "GameplayAttributeData", Fields: fields}}
}

func notifyWithEntity(entity uint64, attributes ...decodedField) decodedField {
	common := decodedField{Number: FieldCombatNotifyDataCombatCommon, Name: "CombatCommon", Kind: "class", Message: &decodedMessage{Type: "CombatCommon", Fields: []decodedField{
		{Number: FieldCombatCommonEntityID, Name: "EntityId", Kind: "int64", Value: int64(entity)},
	}}}
	change := decodedField{Number: FieldCombatNotifyDataAttributeChangedNotify, Name: "AttributeChangedNotify", Kind: "class", Message: &decodedMessage{Type: "AttributeChangedNotify", Fields: attributes}}
	return decodedField{Number: FieldCombatReceiveDataCombatNotifyData, Name: "CombatNotifyData", Kind: "class", Message: &decodedMessage{Type: "CombatNotifyData", Fields: []decodedField{common, change}}}
}

func wrapData(notifies ...decodedField) *decodedMessage {
	var fields []decodedField
	for _, notify := range notifies {
		fields = append(fields, decodedField{Number: FieldCombatReceivePackNotifyData, Name: "Data", Kind: "repeated", Message: &decodedMessage{Type: "CombatReceiveData", Fields: []decodedField{notify}}})
	}
	return &decodedMessage{Type: "CombatReceivePackNotify", Fields: fields}
}

func hpTestSchema() *protocolSchema {
	return &protocolSchema{Enums: map[string]schemaEnum{
		"EAttributeType": {Members: map[string]int64{"LifeMax": 2, "Life": 3, "Energy": 60}},
	}}
}
