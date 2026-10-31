package capture

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadGameMasterDatabaseLoadsTrialRoleParents(t *testing.T) {
	path := buildMasterDatabaseFixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE trialroleinfo(id TEXT PRIMARY KEY,parent_role_id TEXT NOT NULL REFERENCES roleinfo(id))",
		"INSERT INTO trialroleinfo(id,parent_role_id) VALUES('13120001','1312')",
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	master, err := loadGameMasterDatabase(path, "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if parentID, ok := master.trialRoleParents[13120001]; !ok || parentID != 1312 {
		t.Fatalf("TrialRoleInfo parent = %d, ok=%t; want 1312", parentID, ok)
	}
	resolution, ok := master.resolveRoleConfigRole(13120001)
	if !ok || resolution.CanonicalRoleID != 1312 || resolution.Status != "CORRELATED_TRIALROLEINFO_PARENT" {
		t.Fatalf("TrialRoleInfo ID resolution = %+v, ok=%t", resolution, ok)
	}
	if _, ok := master.resolveRoleConfigRole(999999); ok {
		t.Fatal("unknown role config ID unexpectedly resolved")
	}
}

func TestArchiveRoleConfigRecordsPreservesRawAndCanonicalRoleIDs(t *testing.T) {
	roleConfigType := schemaType{Kind: "class", Type: "RoleConfigInfo"}
	schema := &protocolSchema{
		Messages: map[string]schemaMessage{
			"RoleConfigInfoNotify": {Fields: []schemaField{{
				Number: 7, Name: "RoleConfigs", schemaType: schemaType{Kind: "repeated", Element: &roleConfigType},
			}}},
			"RoleConfigInfo": {Fields: []schemaField{{Number: 1, Name: "RoleId", schemaType: schemaType{Kind: "int32"}}}},
		},
		Enums: map[string]schemaEnum{"ENotifyMessageId": {Members: map[string]int64{"RoleConfigInfoNotify": 18250}}},
	}

	body := appendBytesField(nil, 7, appendVarintField(nil, 1, 1312))
	body = appendBytesField(body, 7, appendVarintField(nil, 1, 12117004))
	body = appendBytesField(body, 7, appendVarintField(nil, 1, 999999))
	body = appendBytesField(body, 7, nil)
	var out strings.Builder
	p := &processor{
		protocolSchema: schema,
		roleConfigOut:  &out,
		frameOrdinal:   10,
		sessionNumber:  2,
		masters: &gameMaster{
			locale: "zh-TW",
			characters: map[uint64]masterRecord{
				1312: {ID: 1312, Name: "測試角色", NameStatus: string(resolutionVerified), Language: "zh-TW"},
			},
			trialRoleParents: map[uint64]uint64{12117004: 1312},
		},
	}
	p.archiveSchemaDecode(gameFrame{commandID: 18250, direction: "in"}, body)

	var records []roleConfigRecord
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var record roleConfigRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode output record: %v", err)
		}
		records = append(records, record)
	}
	if len(records) != 4 {
		t.Fatalf("records = %d, want 4: %s", len(records), out.String())
	}
	if records[0].RawRoleID == nil || *records[0].RawRoleID != 1312 || records[0].CanonicalRoleID == nil || *records[0].CanonicalRoleID != 1312 || records[0].Status != "MATCHED_ROLEINFO_ID" {
		t.Fatalf("direct RoleInfo match = %+v", records[0])
	}
	if records[1].RawRoleID == nil || *records[1].RawRoleID != 12117004 || records[1].CanonicalRoleID == nil || *records[1].CanonicalRoleID != 1312 || records[1].Status != "CORRELATED_TRIALROLEINFO_PARENT" || records[1].CanonicalRoleName != "測試角色" {
		t.Fatalf("TrialRoleInfo alias = %+v", records[1])
	}
	if records[2].RawRoleID == nil || *records[2].RawRoleID != 999999 || records[2].CanonicalRoleID != nil || records[2].Status != string(resolutionUnknown) {
		t.Fatalf("unknown RoleId must remain raw and unresolved: %+v", records[2])
	}
	if records[3].RawRoleID != nil || records[3].Status != "MISSING_ROLE_ID" {
		t.Fatalf("missing RoleId must remain distinct from an unknown numeric key: %+v", records[3])
	}
	if records[1].FrameOrdinal != 9 || records[1].SessionNumber != 2 || records[1].CommandID != 18250 || records[1].ConfigIndex != 1 {
		t.Fatalf("role config context = %+v", records[1])
	}
}
