package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type masterRecord struct {
	ID              uint64 `json:"id"`
	Name            string `json:"name"`
	NameStatus      string `json:"nameStatus"`
	GameVersion     string `json:"gameVersion"`
	ResourceVersion string `json:"resourceVersion"`
	SourceCommit    string `json:"sourceCommit"`
	SourceHash      string `json:"sourceHash,omitempty"`
	Language        string `json:"language"`
	Status          string `json:"status"`
	SourceTable     string `json:"sourceTable"`
	IconRef         string `json:"iconRef,omitempty"`
	SkinID          uint64 `json:"skinId,omitempty"`
	RoleID          uint64 `json:"roleId,omitempty"`
	RoleStatus      string `json:"roleStatus,omitempty"`
	CharacterName   string `json:"characterName,omitempty"`
	NameKind        string `json:"nameKind,omitempty"`
}

type damageMasterRecord struct {
	ID              uint64 `json:"id"`
	CalculateType   int32  `json:"calculateType"`
	GameVersion     string `json:"gameVersion"`
	ResourceVersion string `json:"resourceVersion"`
	SourceCommit    string `json:"sourceCommit"`
	SourceHash      string `json:"sourceHash,omitempty"`
	SourceTable     string `json:"sourceTable"`
}

type masterManifest struct {
	GameVersion                 string   `json:"gameVersion"`
	ResourceVersion             string   `json:"resourceVersion"`
	ClientRegion                string   `json:"clientRegion"`
	Language                    string   `json:"language"`
	SourceURL                   string   `json:"sourceUrl"`
	SourceCommit                string   `json:"sourceCommit"`
	SourceHash                  string   `json:"sourceHash,omitempty"`
	Verification                string   `json:"verification"`
	SourceTables                []string `json:"sourceTables"`
	CharacterCount              int      `json:"characterCount"`
	LocalizedCharacterNameCount int      `json:"localizedCharacterNameCount"`
	MonsterCount                int      `json:"monsterCount"`
	LocalizedMonsterNameCount   int      `json:"localizedMonsterNameCount"`
	SkillCount                  int      `json:"skillCount"`
	LocalizedSkillNameCount     int      `json:"localizedSkillNameCount"`
	DamageCount                 int      `json:"damageCount,omitempty"`
	BuffStatus                  string   `json:"buffStatus"`
	BuffReason                  string   `json:"buffReason"`
}

type sceneIdentityManifest struct {
	GameVersion         string `json:"gameVersion"`
	ClientRegion        string `json:"clientRegion"`
	Platform            string `json:"platform"`
	SourceHash          string `json:"sourceHash"`
	InstanceRows        int    `json:"instanceRows"`
	LevelRows           int    `json:"levelRows"`
	TemplateRows        int    `json:"templateRows,omitempty"`
	TemplateCatalogRows int    `json:"templateCatalogRows,omitempty"`
}

type sceneInstanceRecord struct {
	ID          uint64 `json:"id"`
	MapConfigID uint64 `json:"mapConfigId"`
}

type sceneLevelMonsterRecord struct {
	MapID      uint64   `json:"mapId"`
	EntityID   uint64   `json:"entityId"`
	MonsterIDs []uint64 `json:"monsterIds"`
}

type sceneTemplateCandidate struct {
	TemplateID    uint64 `json:"templateId"`
	BlueprintType string `json:"blueprintType"`
	RawName       string `json:"rawName"`
}

type sceneLevelTemplateRecord struct {
	MapID       uint64   `json:"mapId"`
	EntityID    uint64   `json:"entityId"`
	TemplateIDs []uint64 `json:"templateIds"`
}

type sceneIdentityFile struct {
	Manifest        sceneIdentityManifest      `json:"manifest"`
	Instances       []sceneInstanceRecord      `json:"instances"`
	LevelMonsters   []sceneLevelMonsterRecord  `json:"levelMonsters"`
	TemplateCatalog []sceneTemplateCandidate   `json:"templateCatalog,omitempty"`
	LevelTemplates  []sceneLevelTemplateRecord `json:"levelTemplates,omitempty"`
}

type sceneIdentityMaster struct {
	manifest        sceneIdentityManifest
	instanceMaps    map[uint64]uint64
	levelMonsters   map[sceneLevelKey]uint64
	levelTemplates  map[sceneLevelKey][]sceneTemplateCandidate
	templateCatalog map[uint64]sceneTemplateCandidate
}

type sceneLevelKey struct {
	mapID    uint64
	entityID uint64
}

type gameMaster struct {
	manifest       masterManifest
	characters     map[uint64]masterRecord
	charactersSkin map[uint64]masterRecord
	ambiguousSkins map[uint64]struct{}
	monsters       map[uint64]masterRecord
	skills         map[uint64]masterRecord
	damage         map[uint64]damageMasterRecord
	sceneIdentity  *sceneIdentityMaster
}

func loadGameMaster(dir string) (*gameMaster, error) {
	var manifest masterManifest
	if err := readMasterJSON(filepath.Join(dir, "manifest.json"), &manifest); err != nil {
		return nil, err
	}
	if manifest.GameVersion == "" || manifest.ClientRegion != "Global" || manifest.Language == "" || (manifest.SourceCommit == "" && manifest.SourceHash == "") {
		return nil, errors.New("master manifest is missing version, Global region, language, and source commit or source hash")
	}
	if manifest.SourceHash != "" {
		if len(manifest.SourceHash) != 64 {
			return nil, errors.New("master manifest source hash must be a SHA-256 hex digest")
		}
		if _, err := hex.DecodeString(manifest.SourceHash); err != nil {
			return nil, errors.New("master manifest source hash must be a SHA-256 hex digest")
		}
	}
	if len(manifest.SourceTables) == 0 || manifest.Verification == "" {
		return nil, errors.New("master manifest is missing source evidence")
	}
	master := &gameMaster{
		manifest:       manifest,
		characters:     make(map[uint64]masterRecord),
		charactersSkin: make(map[uint64]masterRecord),
		ambiguousSkins: make(map[uint64]struct{}),
		monsters:       make(map[uint64]masterRecord),
		skills:         make(map[uint64]masterRecord),
		damage:         make(map[uint64]damageMasterRecord),
	}
	if sceneIdentity, err := loadSceneIdentity(filepath.Join(filepath.Dir(dir), "entity-identity.json"), manifest); err != nil {
		return nil, err
	} else {
		master.sceneIdentity = sceneIdentity
	}
	if err := loadMasterRecords(filepath.Join(dir, "characters.json"), &master.characters, manifest); err != nil {
		return nil, err
	}
	if err := loadMasterRecords(filepath.Join(dir, "monsters.json"), &master.monsters, manifest); err != nil {
		return nil, err
	}
	if master.sceneIdentity != nil {
		for _, monsterID := range master.sceneIdentity.levelMonsters {
			if _, exists := master.monsters[monsterID]; !exists {
				return nil, fmt.Errorf("scene identity references MonsterId %d absent from the selected monster master", monsterID)
			}
		}
	}
	if err := loadMasterRecords(filepath.Join(dir, "skills.json"), &master.skills, manifest); err != nil {
		return nil, err
	}
	if manifest.DamageCount > 0 {
		if err := loadDamageMasterRecords(filepath.Join(dir, "damage.json"), &master.damage, manifest); err != nil {
			return nil, err
		}
		if len(master.damage) != manifest.DamageCount {
			return nil, fmt.Errorf("damage master count mismatch: %d/%d", len(master.damage), manifest.DamageCount)
		}
	}
	if len(master.characters) != manifest.CharacterCount || len(master.monsters) != manifest.MonsterCount || len(master.skills) != manifest.SkillCount {
		return nil, fmt.Errorf("master count mismatch: characters=%d/%d monsters=%d/%d skills=%d/%d",
			len(master.characters), manifest.CharacterCount, len(master.monsters), manifest.MonsterCount, len(master.skills), manifest.SkillCount)
	}
	if manifest.LocalizedCharacterNameCount > 0 && countLocalizedRecords(master.characters) != manifest.LocalizedCharacterNameCount {
		return nil, fmt.Errorf("localized character name count mismatch: %d/%d", countLocalizedRecords(master.characters), manifest.LocalizedCharacterNameCount)
	}
	if manifest.LocalizedMonsterNameCount > 0 && countLocalizedRecords(master.monsters) != manifest.LocalizedMonsterNameCount {
		return nil, fmt.Errorf("localized monster name count mismatch: %d/%d", countLocalizedRecords(master.monsters), manifest.LocalizedMonsterNameCount)
	}
	if manifest.LocalizedSkillNameCount > 0 && countLocalizedRecords(master.skills) != manifest.LocalizedSkillNameCount {
		return nil, fmt.Errorf("localized skill name count mismatch: %d/%d", countLocalizedRecords(master.skills), manifest.LocalizedSkillNameCount)
	}
	for _, character := range master.characters {
		if character.SkinID == 0 {
			continue
		}
		if existing, ok := master.charactersSkin[character.SkinID]; ok && existing.ID != character.ID {
			delete(master.charactersSkin, character.SkinID)
			master.ambiguousSkins[character.SkinID] = struct{}{}
			continue
		}
		if _, ambiguous := master.ambiguousSkins[character.SkinID]; !ambiguous {
			master.charactersSkin[character.SkinID] = character
		}
	}
	return master, nil
}

func loadSceneIdentity(path string, master masterManifest) (*sceneIdentityMaster, error) {
	var source sceneIdentityFile
	err := readMasterJSON(path, &source)
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) && errors.Is(pathErr.Err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if source.Manifest.GameVersion != master.GameVersion || source.Manifest.ClientRegion != master.ClientRegion || source.Manifest.Platform != "Windows UHD" || source.Manifest.SourceHash == "" {
		return nil, errors.New("scene identity metadata does not match the selected game master version and region")
	}
	if len(source.Manifest.SourceHash) != 64 {
		return nil, errors.New("scene identity source hash must be a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(source.Manifest.SourceHash); err != nil {
		return nil, errors.New("scene identity source hash must be a SHA-256 hex digest")
	}
	if len(source.Instances) != source.Manifest.InstanceRows || len(source.LevelMonsters) != source.Manifest.LevelRows || len(source.LevelTemplates) != source.Manifest.TemplateRows {
		return nil, errors.New("scene identity row count does not match its manifest")
	}
	if len(source.TemplateCatalog) != source.Manifest.TemplateCatalogRows {
		return nil, errors.New("scene template catalog row count does not match its manifest")
	}
	result := &sceneIdentityMaster{
		manifest:        source.Manifest,
		instanceMaps:    make(map[uint64]uint64, len(source.Instances)),
		levelMonsters:   make(map[sceneLevelKey]uint64, len(source.LevelMonsters)),
		levelTemplates:  make(map[sceneLevelKey][]sceneTemplateCandidate, len(source.LevelTemplates)),
		templateCatalog: make(map[uint64]sceneTemplateCandidate, len(source.TemplateCatalog)),
	}
	for _, instance := range source.Instances {
		if instance.ID == 0 || instance.MapConfigID == 0 {
			return nil, errors.New("scene identity contains a zero instance or map ID")
		}
		if _, exists := result.instanceMaps[instance.ID]; exists {
			return nil, fmt.Errorf("duplicate scene identity instance ID %d", instance.ID)
		}
		result.instanceMaps[instance.ID] = instance.MapConfigID
	}
	for _, candidate := range source.TemplateCatalog {
		if candidate.TemplateID == 0 || candidate.BlueprintType == "" {
			return nil, errors.New("scene template catalog candidate must contain a TemplateId and BlueprintType")
		}
		if _, exists := result.templateCatalog[candidate.TemplateID]; exists {
			return nil, fmt.Errorf("duplicate scene TemplateConfig Id %d", candidate.TemplateID)
		}
		result.templateCatalog[candidate.TemplateID] = candidate
	}
	seenLevelKeys := make(map[sceneLevelKey]struct{}, len(source.LevelMonsters))
	for _, level := range source.LevelMonsters {
		if level.MapID == 0 || level.EntityID == 0 || len(level.MonsterIDs) == 0 {
			return nil, errors.New("scene identity level entry must contain non-zero keys and MonsterIds")
		}
		key := sceneLevelKey{mapID: level.MapID, entityID: level.EntityID}
		if _, exists := seenLevelKeys[key]; exists {
			return nil, fmt.Errorf("duplicate scene identity level key map=%d entity=%d", level.MapID, level.EntityID)
		}
		seenLevelKeys[key] = struct{}{}
		seenIDs := make(map[uint64]struct{}, len(level.MonsterIDs))
		for _, monsterID := range level.MonsterIDs {
			if monsterID == 0 {
				return nil, errors.New("scene identity contains a zero MonsterId")
			}
			if _, exists := seenIDs[monsterID]; exists {
				return nil, fmt.Errorf("duplicate MonsterId %d in scene identity key map=%d entity=%d", monsterID, level.MapID, level.EntityID)
			}
			seenIDs[monsterID] = struct{}{}
		}
		if len(level.MonsterIDs) == 1 {
			result.levelMonsters[key] = level.MonsterIDs[0]
		}
	}
	for _, level := range source.LevelTemplates {
		if level.MapID == 0 || level.EntityID == 0 || len(level.TemplateIDs) == 0 {
			return nil, errors.New("scene identity template entry must contain non-zero keys and candidates")
		}
		key := sceneLevelKey{mapID: level.MapID, entityID: level.EntityID}
		if _, exists := result.levelTemplates[key]; exists {
			return nil, fmt.Errorf("duplicate scene template key map=%d entity=%d", level.MapID, level.EntityID)
		}
		seenTemplateIDs := make(map[uint64]struct{}, len(level.TemplateIDs))
		for _, templateID := range level.TemplateIDs {
			candidate, exists := result.templateCatalog[templateID]
			if !exists {
				return nil, fmt.Errorf("scene template key map=%d entity=%d references missing TemplateConfig Id %d", level.MapID, level.EntityID, templateID)
			}
			if _, exists := seenTemplateIDs[templateID]; exists {
				return nil, fmt.Errorf("duplicate TemplateId %d in scene template key map=%d entity=%d", templateID, level.MapID, level.EntityID)
			}
			seenTemplateIDs[templateID] = struct{}{}
			result.levelTemplates[key] = append(result.levelTemplates[key], candidate)
		}
	}
	return result, nil
}

func (m *gameMaster) sceneMonster(instanceID, entityID uint64) (uint64, bool) {
	if m == nil || m.sceneIdentity == nil {
		return 0, false
	}
	mapID, ok := m.sceneMapConfigID(instanceID)
	if !ok {
		return 0, false
	}
	monsterID, ok := m.sceneIdentity.levelMonsters[sceneLevelKey{mapID: mapID, entityID: entityID}]
	return monsterID, ok
}

func (m *gameMaster) sceneTemplates(instanceID, entityID uint64) ([]sceneTemplateCandidate, bool) {
	if m == nil || m.sceneIdentity == nil {
		return nil, false
	}
	mapID, ok := m.sceneMapConfigID(instanceID)
	if !ok {
		return nil, false
	}
	templates, ok := m.sceneIdentity.levelTemplates[sceneLevelKey{mapID: mapID, entityID: entityID}]
	return templates, ok
}

func (m *gameMaster) sceneMapConfigID(instanceID uint64) (uint64, bool) {
	if m == nil || m.sceneIdentity == nil {
		return 0, false
	}
	mapID, ok := m.sceneIdentity.instanceMaps[instanceID]
	return mapID, ok
}

func countLocalizedRecords(records map[uint64]masterRecord) int {
	count := 0
	for _, record := range records {
		if record.Name != "" && record.NameStatus == string(resolutionVerified) {
			count++
		}
	}
	return count
}

func loadDamageMasterRecords(path string, destination *map[uint64]damageMasterRecord, manifest masterManifest) error {
	var records []damageMasterRecord
	if err := readMasterJSON(path, &records); err != nil {
		return err
	}
	loaded := make(map[uint64]damageMasterRecord, len(records))
	for _, record := range records {
		if record.ID == 0 || record.SourceTable == "" {
			return fmt.Errorf("%s contains a record missing ID or source table", filepath.Base(path))
		}
		if record.CalculateType < 0 || record.CalculateType > 2 {
			return fmt.Errorf("%s ID %d has unsupported CalculateType %d", filepath.Base(path), record.ID, record.CalculateType)
		}
		if record.GameVersion != manifest.GameVersion || record.ResourceVersion != manifest.ResourceVersion || record.SourceCommit != manifest.SourceCommit || record.SourceHash != manifest.SourceHash {
			return fmt.Errorf("%s ID %d metadata does not match its manifest", filepath.Base(path), record.ID)
		}
		if _, exists := loaded[record.ID]; exists {
			return fmt.Errorf("%s contains duplicate ID %d", filepath.Base(path), record.ID)
		}
		loaded[record.ID] = record
	}
	*destination = loaded
	return nil
}

func loadMasterRecords(path string, destination *map[uint64]masterRecord, manifest masterManifest) error {
	var records []masterRecord
	if err := readMasterJSON(path, &records); err != nil {
		return err
	}
	loaded := make(map[uint64]masterRecord, len(records))
	for _, record := range records {
		if record.ID == 0 || record.SourceTable == "" {
			return fmt.Errorf("%s contains a record missing ID or source table", filepath.Base(path))
		}
		if record.Status != string(resolutionVerified) && record.Status != string(resolutionCorrelated) && record.Status != string(resolutionHypothesis) && record.Status != string(resolutionUnknown) {
			return fmt.Errorf("%s ID %d has unsupported status %q", filepath.Base(path), record.ID, record.Status)
		}
		if record.NameStatus == "" && record.Name != "" {
			record.NameStatus = record.Status
		}
		if record.NameStatus != string(resolutionVerified) && record.NameStatus != string(resolutionCorrelated) && record.NameStatus != string(resolutionHypothesis) && record.NameStatus != string(resolutionUnknown) {
			return fmt.Errorf("%s ID %d has unsupported name status %q", filepath.Base(path), record.ID, record.NameStatus)
		}
		if record.Name == "" && record.NameStatus != string(resolutionUnknown) {
			return fmt.Errorf("%s ID %d has no name but name status is %q", filepath.Base(path), record.ID, record.NameStatus)
		}
		if record.RoleStatus != "" && record.RoleStatus != string(resolutionVerified) && record.RoleStatus != string(resolutionCorrelated) && record.RoleStatus != string(resolutionHypothesis) && record.RoleStatus != string(resolutionUnknown) {
			return fmt.Errorf("%s ID %d has unsupported role status %q", filepath.Base(path), record.ID, record.RoleStatus)
		}
		if record.RoleStatus == string(resolutionVerified) && record.RoleID == 0 {
			return fmt.Errorf("%s ID %d has verified role mapping without a role ID", filepath.Base(path), record.ID)
		}
		if record.RoleStatus == string(resolutionUnknown) && record.RoleID != 0 {
			return fmt.Errorf("%s ID %d has unknown role mapping with a role ID", filepath.Base(path), record.ID)
		}
		if record.GameVersion != manifest.GameVersion || record.ResourceVersion != manifest.ResourceVersion || record.SourceCommit != manifest.SourceCommit || record.SourceHash != manifest.SourceHash || record.Language != manifest.Language {
			return fmt.Errorf("%s ID %d metadata does not match its manifest", filepath.Base(path), record.ID)
		}
		if _, exists := loaded[record.ID]; exists {
			return fmt.Errorf("%s contains duplicate ID %d", filepath.Base(path), record.ID)
		}
		loaded[record.ID] = record
	}
	*destination = loaded
	return nil
}

func readMasterJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read local master %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("parse local master %s: %w", filepath.Base(path), err)
	}
	return nil
}

func (m *gameMaster) characterBySkin(skinID uint64) (masterRecord, bool) {
	if _, ambiguous := m.ambiguousSkins[skinID]; ambiguous {
		return masterRecord{}, false
	}
	value, ok := m.charactersSkin[skinID]
	return value, ok
}
