//go:build wwdev

package capture

import (
	"encoding/json"
	"testing"
)

func TestEventEnvelopeOrderResetsEachFrame(t *testing.T) {
	p := &processor{sessionNumber: 2}
	p.dev.frameOrdinal = 5 // archiveFrame has already consumed frame 4
	first := p.nextEventEnvelope()
	second := p.nextEventEnvelope()
	if first.EventID != "s2-f4-e0" || second.EventID != "s2-f4-e1" {
		t.Fatalf("ids = %q, %q; want s2-f4-e0, s2-f4-e1", first.EventID, second.EventID)
	}
	p.dev.frameOrdinal = 6
	next := p.nextEventEnvelope()
	if next.EventID != "s2-f5-e0" || next.EmitFrameOrdinal != 5 || next.EmitOrderInFrame != 0 {
		t.Fatalf("next frame envelope = %+v", next)
	}
}

func TestPrependEnvelopeMergesIntoEventObject(t *testing.T) {
	envelope := eventEnvelope{EventID: "s1-f0-e0", EmitFrameOrdinal: 0, EmitOrderInFrame: 0}
	line, err := prependEnvelope(envelope, struct {
		EventType string `json:"eventType"`
		Value     int    `json:"value"`
	}{"DAMAGE", 7})
	if err != nil {
		t.Fatalf("prependEnvelope: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(line, &decoded); err != nil {
		t.Fatalf("merged line is not valid JSON: %v (%s)", err, line)
	}
	if decoded["eventId"] != "s1-f0-e0" || decoded["eventType"] != "DAMAGE" || decoded["value"] != float64(7) {
		t.Fatalf("merged object = %v", decoded)
	}
	if line[len(line)-1] != '\n' {
		t.Fatal("merged line must end with a newline")
	}
}

func TestPrependEnvelopeAcceptsEmptyObject(t *testing.T) {
	line, err := prependEnvelope(eventEnvelope{EventID: "s1-f0-e0"}, struct{}{})
	if err != nil {
		t.Fatalf("prependEnvelope: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(line, &decoded); err != nil || decoded["eventId"] != "s1-f0-e0" {
		t.Fatalf("empty object line = %s (%v)", line, err)
	}
}
