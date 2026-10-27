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
