//go:build !wwdev

package capture

import (
	"io"
	"reflect"
	"testing"
	"unsafe"
)

// diagnosticConsole reports whether this build profile prints per-event console diagnostics.
const diagnosticConsole = false

// A public build has no Development state: the processor's Development part has no field and takes no memory, and the
// processor holds no writer but its operational diagnostic stream, so no console, audit or diagnostic sink can exist.
func TestPublicProcessorHasNoDevelopmentState(t *testing.T) {
	if fields, size := reflect.TypeOf(processorDiagnostics{}).NumField(), unsafe.Sizeof(processorDiagnostics{}); fields != 0 || size != 0 {
		t.Fatalf("Public processorDiagnostics has %d fields and %d bytes", fields, size)
	}
	writer := reflect.TypeOf((*io.Writer)(nil)).Elem()
	processorType := reflect.TypeOf(processor{})
	for i := 0; i < processorType.NumField(); i++ {
		field := processorType.Field(i)
		if field.Type.Kind() == reflect.Interface && field.Type.Implements(writer) && field.Name != "diagnostic" {
			t.Fatalf("Public processor has the writer field %s", field.Name)
		}
	}
	newProcessor(nil, nopWriter{}, nopWriter{}, nopWriter{}).dev.beginProtocolSession()
}

// discardConsole does nothing: a public processor has no console.
func discardConsole(*processor) {}

// researchSchema is nil: a public processor has no research schema.
func researchSchema(*processor) *protocolSchema { return nil }

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
