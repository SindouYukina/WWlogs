//go:build wwdev

package capture

func scalarField(name string, value int64) decodedField {
	return decodedField{Name: name, Kind: "int64", Value: value}
}

func messageField(name string, message *decodedMessage) decodedField {
	return decodedField{Name: name, Kind: "class", Message: message}
}
