//go:build wwdev

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSummonOwnersWriteSummonerAndDependenceFacts(t *testing.T) {
	summoner := decodedField{Number: 1, Name: "SummonerComponentPb", Kind: "class", Message: &decodedMessage{Type: "SummonerComponentPb", Fields: []decodedField{
		{Number: 1, Name: "SummonerId", Kind: "int64", Value: int64(51522)},
		{Number: 2, Name: "SummonCfgId", Kind: "int32", Value: int32(280026)},
		{Number: 5, Name: "Type", Kind: "enum", Value: int32(1)},
	}}}
	dependence := decodedField{Number: 2, Name: "DependenceComponentPb", Kind: "class", Message: &decodedMessage{Type: "DependenceComponentPb", Fields: []decodedField{
		{Number: 1, Name: "DependenceEntityIds", Kind: "repeated", Value: int64(51520)},
		{Number: 1, Name: "DependenceEntityIds", Kind: "repeated", Value: int64(51518)},
	}}}
	entity := decodedField{Number: 3, Name: "Entities", Kind: "class", Message: &decodedMessage{Type: "EntityPb", Fields: []decodedField{
		{Number: 1, Name: "Id", Kind: "int64", Value: int64(51524)},
		componentWrapper(summoner),
	}}}
	dependent := decodedField{Number: 3, Name: "Entities", Kind: "class", Message: &decodedMessage{Type: "EntityPb", Fields: []decodedField{
		{Number: 1, Name: "Id", Kind: "int64", Value: int64(51522)},
		componentWrapper(dependence),
	}}}
	decoded := &decodedMessage{Type: "JoinSceneNotify", Fields: []decodedField{entity, dependent}}

	var out strings.Builder
	p := &processor{
		protocolSchema: &protocolSchema{Enums: map[string]schemaEnum{"ESummonType": {Members: map[string]int64{"ConcomitantVision": 1}}}},
		summonOwnerOut: &out,
		frameOrdinal:   2,
		sessionNumber:  1,
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
