//go:build wwdev

package capture

import (
	"os"
	"testing"
)

func TestOptionalFileWriterPreservesNil(t *testing.T) {
	if got := optionalFileWriter(nil); got != nil {
		t.Fatalf("optionalFileWriter(nil) = %#v, want nil", got)
	}
}

func TestOptionalFileWriterPreservesConfiguredFile(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "events-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if got := optionalFileWriter(file); got != file {
		t.Fatalf("optionalFileWriter(file) = %#v, want original file", got)
	}
}
