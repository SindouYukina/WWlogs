//go:build wwdev

package capture

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummonOwnersWriteSummonerAndDependenceFacts(t *testing.T) {
	summoner := decodedField{Number: fieldEntityComponentPbSummonerComponent, Name: "SummonerComponentPb", Kind: "class", Message: &decodedMessage{Type: "SummonerComponentPb", Fields: []decodedField{
		{Number: fieldSummonerComponentPbSummonerID, Name: "SummonerId", Kind: "int64", Value: int64(51522)},
		{Number: fieldSummonerComponentPbSummonCfgID, Name: "SummonCfgId", Kind: "int32", Value: int32(280026)},
		{Number: fieldSummonerComponentPbType, Name: "Type", Kind: "enum", Value: int32(1)},
	}}}
	dependence := decodedField{Number: fieldEntityComponentPbDependenceComponentPb, Name: "DependenceComponentPb", Kind: "class", Message: &decodedMessage{Type: "DependenceComponentPb", Fields: []decodedField{
		{Number: fieldDependenceComponentPbDependenceEntityIDs, Name: "DependenceEntityIds", Kind: "repeated", Value: int64(51520)},
		{Number: fieldDependenceComponentPbDependenceEntityIDs, Name: "DependenceEntityIds", Kind: "repeated", Value: int64(51518)},
	}}}
	entity := decodedField{Number: fieldPlayerSceneAoiDataEntities, Name: "Entities", Kind: "class", Message: &decodedMessage{Type: "EntityPb", Fields: []decodedField{
		{Number: fieldEntityPbID, Name: "Id", Kind: "int64", Value: int64(51524)},
		componentWrapper(summoner),
	}}}
	dependent := decodedField{Number: fieldPlayerSceneAoiDataEntities, Name: "Entities", Kind: "class", Message: &decodedMessage{Type: "EntityPb", Fields: []decodedField{
		{Number: fieldEntityPbID, Name: "Id", Kind: "int64", Value: int64(51522)},
		componentWrapper(dependence),
	}}}
	decoded := &decodedMessage{Type: "JoinSceneNotify", Fields: []decodedField{entity, dependent}}

	var out strings.Builder
	p := &processor{
		sessionNumber: 1,
		dev: processorDiagnostics{
			protocolSchema: &protocolSchema{Enums: map[string]schemaEnum{"ESummonType": {Members: map[string]int64{"ConcomitantVision": 1}}}},
			summonOwnerOut: &out,
			frameOrdinal:   2,
		},
	}
	p.archiveSummonOwners(gameFrame{}, "JoinSceneNotify", decoded)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("records = %d, want 3 (one summoner, two dependence)", len(lines))
	}
	var summonerRow summonOwnerRecord
	if err := json.Unmarshal([]byte(lines[0]), &summonerRow); err != nil {
		t.Fatalf("summoner row: %v", err)
	}
	if summonerRow.Kind != "summoner" || summonerRow.EntityID != 51524 || *summonerRow.SummonerID != 51522 || *summonerRow.SummonCfgID != 280026 || summonerRow.SummonType != "ConcomitantVision" {
		t.Fatalf("summoner row = %+v", summonerRow)
	}
	var depRow summonOwnerRecord
	if err := json.Unmarshal([]byte(lines[2]), &depRow); err != nil {
		t.Fatalf("dependence row: %v", err)
	}
	if depRow.Kind != "dependence" || depRow.EntityID != 51522 || *depRow.RelatedID != 51518 {
		t.Fatalf("dependence row = %+v", depRow)
	}
}
