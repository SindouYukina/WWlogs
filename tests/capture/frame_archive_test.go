package capture

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func decodeArchiveRecords(t *testing.T, data []byte) []frameArchiveRecord {
	t.Helper()
	var records []frameArchiveRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record frameArchiveRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("archive line is not valid JSON: %v", err)
		}
		records = append(records, record)
	}
	return records
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("test write failure") }
