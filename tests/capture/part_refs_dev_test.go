//go:build wwdev

package capture

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestPartReferenceOutputLinksRequestToCurrentTargetPart(t *testing.T) {
	var output, diagnostic bytes.Buffer
	p := newProcessor(nil, &bytes.Buffer{}, &diagnostic, nil)
	p.sessionNumber = 3
	p.frameOrdinal = 45
	p.partRefOut = &output
	if _, err := p.entities.applyEntityAddNotify(entityAddWithPartComponent(902, partComponentWire(partInformationWire(2, 28046.3, true, 17)))); err != nil {
		t.Fatal(err)
	}
	frame := gameFrame{direction: "out", commandID: 19470, timestamp: time.Date(2026, 10, 30, 3, 4, 5, 0, time.UTC)}
	p.handleDamageRequests(frame, damageRequestWithPart(91, 902, 2, true))

	var row partReferenceRecord
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	if row.SchemaVersion != partReferenceSchemaVersion || row.EventType != "DAMAGE_REQUEST_PART_REFERENCE" || row.FrameOrdinal != 44 || row.SessionNumber != 3 || row.Direction != "out" || row.CommandID != 19470 || row.RPCID != 91 || row.FieldNumber != 5 {
		t.Fatalf("request envelope = %+v", row)
	}
	if row.Status != partReferenceCorrelated || row.PartID == nil || *row.PartID != 2 || !row.HasPartID || row.TargetEntityID == nil || *row.TargetEntityID != 902 {
		t.Fatalf("reference = %+v, want exact target-local PartId link", row)
	}
	if row.PartInformation == nil || row.PartInformation.PartID != 2 || row.PartInformation.LifeMax == nil || math.Abs(float64(*row.PartInformation.LifeMax-28046.3)) > 0.01 || row.PartInformation.Activated == nil || !*row.PartInformation.Activated || row.PartInformation.PartTag == nil || *row.PartInformation.PartTag != 17 {
		t.Fatalf("part attributes = %+v", row.PartInformation)
	}
	if row.PartInformationSource != "EntityAddNotify.EntityPbs[].ComponentPbs.PartComponent.PartLifeInfos[]" {
		t.Fatalf("part source = %q", row.PartInformationSource)
	}
	if _, ok := p.pending[91]; !ok {
		t.Fatal("request sidecar must not alter the existing RPC pending state")
	}
}
