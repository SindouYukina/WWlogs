package main

func attributeMessage(attributeType, baseValue int64, withCurrent bool) decodedField {
	fields := []decodedField{
		{Number: 3, Name: "AttributeType", Kind: "int32", Value: attributeType},
		{Number: 1, Name: "BaseValue", Kind: "int32", Value: baseValue},
	}
	if withCurrent {
		fields = append(fields, decodedField{Number: 2, Name: "CurrentValue", Kind: "int32", Value: baseValue})
	}
	return decodedField{Number: 8, Name: "Attributes", Kind: "class", Message: &decodedMessage{Type: "GameplayAttributeData", Fields: fields}}
}

func notifyWithEntity(entity uint64, attributes ...decodedField) decodedField {
	common := decodedField{Number: 1, Name: "CombatCommon", Kind: "class", Message: &decodedMessage{Type: "CombatCommon", Fields: []decodedField{
		{Number: 2, Name: "EntityId", Kind: "int64", Value: int64(entity)},
	}}}
	change := decodedField{Number: 22, Name: "AttributeChangedNotify", Kind: "class", Message: &decodedMessage{Type: "AttributeChangedNotify", Fields: attributes}}
	return decodedField{Number: 2, Name: "CombatNotifyData", Kind: "class", Message: &decodedMessage{Type: "CombatNotifyData", Fields: []decodedField{common, change}}}
}

func wrapData(notifies ...decodedField) *decodedMessage {
	var fields []decodedField
	for _, notify := range notifies {
		fields = append(fields, decodedField{Number: 5, Name: "Data", Kind: "repeated", Message: &decodedMessage{Type: "CombatReceiveData", Fields: []decodedField{notify}}})
	}
	return &decodedMessage{Type: "CombatReceivePackNotify", Fields: fields}
}

func hpTestSchema() *protocolSchema {
	return &protocolSchema{Enums: map[string]schemaEnum{
		"EAttributeType": {Members: map[string]int64{"LifeMax": 2, "Life": 3, "Energy": 60}},
	}}
}
