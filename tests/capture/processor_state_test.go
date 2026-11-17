package capture

import (
	"io"
	"reflect"
	"testing"
)

// The core processor has no Development state in either build profile: no writer but its operational diagnostic
// stream, so no console, audit or diagnostic sink can exist in the core, and no research record. The Development
// diagnostics observe it from wwlogs/internal/devcapture (Observe).
func TestProcessorHasNoDevelopmentState(t *testing.T) {
	writer := reflect.TypeOf((*io.Writer)(nil)).Elem()
	processorType := reflect.TypeOf(Processor{})
	for i := 0; i < processorType.NumField(); i++ {
		field := processorType.Field(i)
		if field.Type.Kind() == reflect.Interface && field.Type.Implements(writer) && field.Name != "Diagnostic" {
			t.Fatalf("the core processor has the writer field %s", field.Name)
		}
	}
	if _, ok := processorType.FieldByName("Dev"); ok {
		t.Fatal("the core processor holds Development state")
	}
	p := NewProcessor(nil, nopWriter{})
	if p.observer != nil {
		t.Fatal("a new processor has an observer")
	}
	p.Observe(nil)
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
