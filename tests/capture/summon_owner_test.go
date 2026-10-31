package capture

func componentWrapper(component decodedField) decodedField {
	return decodedField{Number: 11, Name: "ComponentPbs", Kind: "class", Message: &decodedMessage{Type: "EntityComponentPb", Fields: []decodedField{component}}}
}
