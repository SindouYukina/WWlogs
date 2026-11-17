package devcapture

import "wwlogs/internal/capture"

func componentWrapper(component decodedField) decodedField {
	return decodedField{Number: capture.FieldEntityPbComponentPbs, Name: "ComponentPbs", Kind: "class", Message: &decodedMessage{Type: "EntityComponentPb", Fields: []decodedField{component}}}
}
