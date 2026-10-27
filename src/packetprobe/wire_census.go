package main

import "fmt"

// maxTopLevelFieldKeys bounds the per-command field histogram. Random or
// corrupted bodies can produce many field numbers, so further distinct keys
// are counted as overflow instead of growing the census without limit.
const maxTopLevelFieldKeys = 64

// observeTopLevelFields counts field number and wire type pairs for a body
// that is syntactically valid Protobuf at the top level. The keys are wire
// facts only. They do not show that a field has a semantic meaning, and nested
// messages are not inspected here.
func (e *commandCensusEntry) observeTopLevelFields(fields []protoField) {
	for _, field := range fields {
		key := fmt.Sprintf("%d/w%d", field.number, field.wire)
		if e.TopLevelFields == nil {
			e.TopLevelFields = make(map[string]int)
		}
		if _, known := e.TopLevelFields[key]; !known && len(e.TopLevelFields) >= maxTopLevelFieldKeys {
			e.TopLevelFieldOverflow++
			continue
		}
		e.TopLevelFields[key]++
	}
}

// observeKnownBodyWire records top-level wire facts for a decrypted body of an
// implemented command. It reads the same plaintext as the typed decoder and
// must not change the typed decoder's result. The field counts include fields
// the typed decoder already uses, so they are not an unknown-field census by
// themselves.
func (e *commandCensusEntry) observeKnownBodyWire(plain []byte) {
	fields, err := parseProtoFields(plain)
	if err != nil {
		e.FailureReasons["implemented body top-level wire parse failed"]++
		return
	}
	e.ImplementedWireBodies++
	e.observeTopLevelFields(fields)
}
