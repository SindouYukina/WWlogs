package capture

func pushWrapper(child decodedField) *decodedMessage {
	push := decodedField{Number: 2, Name: "Push", Kind: "class", Message: &decodedMessage{Fields: []decodedField{child}}}
	return &decodedMessage{Type: "CombatSendPackRequest", Fields: []decodedField{
		{Number: 14, Name: "Data", Kind: "repeated", Message: &decodedMessage{Fields: []decodedField{push}}},
	}}
}
