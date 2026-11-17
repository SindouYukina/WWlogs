//go:build wwdev

package capture

func componentWrapper(component decodedField) decodedField {
	return decodedField{Number: FieldEntityPbComponentPbs, Name: "ComponentPbs", Kind: "class", Message: &decodedMessage{Type: "EntityComponentPb", Fields: []decodedField{component}}}
}
