//go:build wwdev

package capture

import (
	"fmt"
	"strings"
)

// bulletResolution records which static bullet row-key namespace a BulletId belongs to.
// BulletId alone does not name a table: it is a row key in one of several DataTable
// families, and the same number can recur across actors. It is translated only when
// the source chain leads to exactly one actor (or the key belongs to exactly one family).
type bulletResolution struct {
	BulletID       uint64
	Namespace      string
	ActorBlueprint string
	Via            string
	DevLabelZh     string
	// DevLabel is the localized developer note of the CommonBulletData row; never an official name.
	DevLabel *combatEventLabelTextJSONL
	// RowDamageID is the DamageId written in the matched CDT_CommonBulletData row (0 when none).
	RowDamageID uint64
	Status      resolutionStatus
	Proof       string
}

// bulletSource is what an event knows about the entity that fired a bullet.
type bulletSource struct {
	RoleSkinID          uint64
	HasRoleSkin         bool
	BlueprintTypes      []string
	FollowerSummonCfgID uint64
	HasFollower         bool
	SkillID             uint64
	// VisionSummonCfgs are SummonCfg ids of live ConcomitantVision entities summoned by the source entity.
	VisionSummonCfgs []uint64
	// OwnSummonCfgID is the source entity's own SummonerComponent.SummonCfgId, set when the source is itself a summon.
	OwnSummonCfgID    uint64
	HasOwnSummonCfgID bool
}

type bulletChain struct {
	via   string
	actor string
}

// newBulletSource collects what an entity identity knows about a bullet's origin.
func newBulletSource(identity entityIdentity, skillID uint64, visionSummonCfgs []uint64) bulletSource {
	source := bulletSource{SkillID: skillID, VisionSummonCfgs: visionSummonCfgs}
	source.RoleSkinID, source.HasRoleSkin = identity.Runtime.roleSkinID, identity.Runtime.hasRoleSkinID
	source.OwnSummonCfgID, source.HasOwnSummonCfgID = identity.Runtime.summonConfigID, identity.Runtime.hasSummonConfigID
	source.FollowerSummonCfgID, source.HasFollower = identity.Runtime.followerSummonConfigID, identity.Runtime.hasFollowerSummonConfigID
	for _, candidate := range identity.TemplateCandidates {
		source.BlueprintTypes = append(source.BlueprintTypes, candidate.BlueprintType)
	}
	return source
}

func bulletChains(m *gameMaster, source bulletSource) []bulletChain {
	var chains []bulletChain
	if source.HasRoleSkin {
		if mesh, ok := m.roleSkinMesh[source.RoleSkinID]; ok {
			if actor, ok := m.actorOfModel(mesh); ok {
				chains = append(chains, bulletChain{"EntityPb.RoleSkinId -> RoleSkin.MeshId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	for _, blueprint := range source.BlueprintTypes {
		if actor, ok := m.actorOfBlueprint(blueprint); ok {
			chains = append(chains, bulletChain{"scene TemplateConfig.BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
		}
	}
	if source.HasOwnSummonCfgID {
		if blueprint, ok := m.summonBlueprint[source.OwnSummonCfgID]; ok {
			if actor, ok := m.actorOfBlueprint(blueprint); ok {
				chains = append(chains, bulletChain{"source entity SummonerComponent.SummonCfgId -> SummonCfg.BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	if source.HasFollower {
		if blueprint, ok := m.summonBlueprint[source.FollowerSummonCfgID]; ok {
			if actor, ok := m.actorOfBlueprint(blueprint); ok {
				chains = append(chains, bulletChain{"FollowerSummonConfigId -> SummonCfg.BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	for _, cfg := range source.VisionSummonCfgs {
		if blueprint, ok := m.summonBlueprint[cfg]; ok {
			if actor, ok := m.actorOfBlueprint(blueprint); ok {
				chains = append(chains, bulletChain{"ConcomitantVision SummonerComponent(SummonerId=source entity).SummonCfgId -> SummonCfg.BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	for _, vision := range m.visionCast[source.SkillID] {
		if blueprint, ok := m.summonBlueprint[vision]; ok {
			if actor, ok := m.actorOfBlueprint(blueprint); ok {
				chains = append(chains, bulletChain{"DamageContext.SkillId -> Vision.BirthCastSkillId -> SummonCfg(Vision.Id).BlueprintType -> BlueprintConfig.ModelId -> ModelConfigPreload.ActorClassPath", actor})
			}
		}
	}
	return chains
}

func resolveBullet(masters *gameMaster, bulletID uint64, source bulletSource) *bulletResolution {
	if masters == nil {
		return nil
	}
	result := &bulletResolution{BulletID: bulletID, Status: resolutionUnknown}
	label, inCommon := masters.commonBullets[bulletID]
	_, inPreload := masters.preloadBullets[bulletID]
	chains := bulletChains(masters, source)
	if len(chains) == 0 && source.SkillID != 0 {
		if actor, ok := uniqueActorForSkill(masters, source.SkillID); ok {
			if _, listed := masters.actorBullets[actor][bulletID]; listed {
				chains = append(chains, bulletChain{
					via:   "same combat reference SkillId -> unique EntitySkillPreload actor asset; BulletId -> that actor's BulletPreload.BulletId",
					actor: actor,
				})
			}
		}
	}
	matched := make(map[string]string)
	for _, chain := range chains {
		if _, ok := masters.actorBullets[chain.actor][bulletID]; ok {
			if _, seen := matched[chain.actor]; !seen {
				matched[chain.actor] = chain.via
			}
		}
	}
	switch {
	case len(matched) > 1:
		result.Proof = fmt.Sprintf("BulletId is held by %d distinct source-chain actors; the serving actor is not determined", len(matched))
	case len(matched) == 1 && inCommon:
		result.Proof = "BulletId is held by the source actor and by CDT_CommonBulletData; the table that served this bullet is not determined"
	case len(matched) == 1:
		for actor, via := range matched {
			result.Namespace = "BulletPreload.ActorBlueprint"
			result.ActorBlueprint = masters.assetPath[actor]
			result.Via = via
			result.Status = resolutionCorrelated
			result.Proof = via + "; the actor's BulletPreload rows in the combat master list this BulletId; client build observed, master compatibility unverified"
		}
	case inCommon && inPreload:
		result.Proof = "BulletId is a row key in both CDT_CommonBulletData and BulletPreload and no source-chain actor holds it; the serving table is not determined"
	case inCommon:
		result.Namespace = "CDT_CommonBulletData.RowKey"
		result.DevLabelZh = label
		result.DevLabel = masters.commonBulletDevLabels[bulletID]
		result.RowDamageID = masters.commonBulletDamage[bulletID]
		result.Status = resolutionCorrelated
		result.Proof = "BulletId is a row key of CDT_CommonBulletData and of no actor-level BulletPreload family; the loader that serves this common table in normal combat is unproven, client build observed, master compatibility unverified"
	case inPreload:
		result.Proof = fmt.Sprintf("BulletId matches an actor-level BulletPreload row key but none of the %d source-chain actors holds it; only a numeric match is recorded", len(chains))
	default:
		result.Proof = "BulletId has no row in the loaded bullet definitions"
	}
	return result
}

const bulletRefSchemaVersion = 1

// bulletRefRecord is one BulletId carried by an outbound combat message, with the
// static actor-chain translation of that BulletId for the entity that sent it.
type bulletRefRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	FrameOrdinal  uint64 `json:"frameOrdinal"`
	SessionNumber int    `json:"sessionNumber"`
	Timestamp     string `json:"timestamp"`
	Direction     string `json:"direction"`
	FieldPath     string `json:"fieldPath"`
	BulletID      int64  `json:"bulletId"`
	// HandleID and ResolvedBulletID are set when the BulletId field actually holds an ActiveBulletHandle.HandleId.
	HandleID         *int64           `json:"handleId,omitempty"`
	ResolvedBulletID *int64           `json:"resolvedBulletId,omitempty"`
	SourceEntityID   *int64           `json:"sourceEntityId,omitempty"`
	SourceBasis      string           `json:"sourceBasis,omitempty"`
	SkillID          *int64           `json:"skillId,omitempty"`
	DtType           *int64           `json:"dtType,omitempty"`
	Status           resolutionStatus `json:"status"`
	Namespace        string           `json:"namespace,omitempty"`
	ActorBlueprint   string           `json:"actorBlueprint,omitempty"`
	Via              string           `json:"via,omitempty"`
	DevLabelZhCn     string           `json:"devLabelZhCn,omitempty"`
	// DevLabel is the localized developer note of the bullet row; never an official name.
	DevLabel *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
	Evidence string                     `json:"evidence"`
}

const maxBulletHandles = 1 << 16

// bulletHandleKey scopes a bullet handle id to the network session, since handle ids restart.
type bulletHandleKey struct {
	session  int
	handleID int64
}

// bulletHandleEntry remembers the bullet a CreateBullet message created under a handle id.
type bulletHandleEntry struct {
	bulletID  int64
	skillID   int64
	hasSkill  bool
	status    resolutionStatus
	namespace string
	actor     string
	via       string
	label     string
}

// bulletSourceCandidate names where the firing entity id is read from.
type bulletSourceCandidate struct {
	basis string
	id    int64
}

func (p *processor) rememberBulletHandle(message *decodedMessage, record bulletRefRecord) {
	handle := firstChildMessage(message, "Handle")
	handleID, ok := scalarValue(handle, "HandleId")
	if !ok || handleID <= 0 {
		return
	}
	if p.dev.bulletHandles == nil {
		p.dev.bulletHandles = make(map[bulletHandleKey]bulletHandleEntry)
	}
	key := bulletHandleKey{p.sessionNumber, handleID}
	if _, exists := p.dev.bulletHandles[key]; !exists {
		p.dev.bulletHandleOrder = append(p.dev.bulletHandleOrder, key)
	}
	entry := bulletHandleEntry{bulletID: record.BulletID, status: record.Status, namespace: record.Namespace, actor: record.ActorBlueprint, via: record.Via, label: record.DevLabelZhCn}
	if record.SkillID != nil {
		entry.skillID, entry.hasSkill = *record.SkillID, true
	}
	p.dev.bulletHandles[key] = entry
	for len(p.dev.bulletHandleOrder) > maxBulletHandles {
		delete(p.dev.bulletHandles, p.dev.bulletHandleOrder[0])
		p.dev.bulletHandleOrder = p.dev.bulletHandleOrder[1:]
	}
}

// resolveBulletHandle translates a value that is a bullet handle id rather than a bullet row key.
// It requires an earlier CreateBullet in the same session under that handle id with the same SkillId.
func (p *processor) resolveBulletHandle(record *bulletRefRecord) {
	handleID := record.BulletID
	record.HandleID = &handleID
	entry, ok := p.dev.bulletHandles[bulletHandleKey{p.sessionNumber, handleID}]
	switch {
	case !ok:
		record.Evidence = "InterruptSkillInfo.BulletId is not the HandleId of any earlier CreateBullet in this session; the value is not a static bullet row key"
	case entry.hasSkill && record.SkillID != nil && entry.skillID != *record.SkillID:
		record.Evidence = "the handle id matches an earlier CreateBullet whose SkillId differs from this message; the match is rejected"
	default:
		resolved := entry.bulletID
		record.ResolvedBulletID = &resolved
		record.Status, record.Namespace, record.ActorBlueprint, record.DevLabelZhCn = entry.status, "ActiveBulletHandle.HandleId", entry.actor, entry.label
		record.Via = "InterruptSkillInfo.BulletId equals the Handle.HandleId of an earlier CreateBullet in this session"
		if entry.via != "" {
			record.Via += "; " + entry.via
		}
		record.Evidence = "the field holds a runtime bullet handle (identity VERIFIED by exact handle-id and SkillId match); the static bullet it points to inherits the CreateBullet translation status " + string(entry.status)
	}
}

// observeBulletReferences walks one CombatSendData for every BulletId field, at any
// depth, and resolves it through the source entity's actor chain. The source entity
// comes from a fixed per-message rule (see bulletSourceCandidates), not from trying
// several fields until one resolves.
func (p *processor) observeBulletReferences(frame gameFrame, data *decodedMessage, direction string) {
	p.walkBulletReferences(frame, data, "Data", nil, nil, direction)
}

// archiveBulletRefReceiveFrame handles standalone and response-embedded inbound combat notifications.
func (p *processor) archiveBulletRefReceiveFrame(frame gameFrame, plain []byte) {
	if (p.dev.bulletRefOut == nil && p.dev.skillRefOut == nil && p.dev.shieldRefOut == nil) || p.dev.protocolSchema == nil || (frame.commandID != cmdCombatReceivePackNotify && frame.commandID != cmdCombatSendPackResponse) {
		return
	}
	payloads, err := receivePackNotifyBodies(frame.commandID, plain)
	if err != nil {
		if p.dev.bulletRefOut != nil {
			p.dev.bulletRefErrors++
		}
		if p.dev.skillRefOut != nil {
			p.dev.skillRefErrors++
		}
		if p.dev.shieldRefOut != nil {
			p.dev.shieldRefErrors++
		}
		return
	}
	for _, payload := range payloads {
		decoded, err := decodeSchemaMessage(p.dev.protocolSchema, "CombatReceivePackNotify", payload, 0)
		if err != nil {
			if p.dev.bulletRefOut != nil {
				p.dev.bulletRefErrors++
			}
			if p.dev.skillRefOut != nil {
				p.dev.skillRefErrors++
			}
			if p.dev.shieldRefOut != nil {
				p.dev.shieldRefErrors++
			}
			continue
		}
		for _, data := range childMessages(decoded, "Data") {
			if p.dev.bulletRefOut != nil {
				p.observeBulletReferences(frame, data, "in")
			}
			if p.dev.skillRefOut != nil {
				p.observeSkillReferences(frame, data, "in")
			}
			if p.dev.shieldRefOut != nil {
				p.observeShieldReferences(frame, data, "in")
			}
		}
	}
}

func (p *processor) walkBulletReferences(frame gameFrame, message *decodedMessage, path string, parent, common *decodedMessage, direction string) {
	if message == nil {
		return
	}
	if own := firstChildMessage(message, "CombatCommon"); own != nil {
		common = own
	}
	if bulletID, ok := scalarValue(message, "BulletId"); ok && bulletID > 0 {
		p.writeBulletReference(frame, message, parent, path+".BulletId", bulletID, common, direction)
	}
	for _, field := range message.Fields {
		if field.Message != nil {
			p.walkBulletReferences(frame, field.Message, path+"."+field.Name, message, common, direction)
		}
	}
}

// bulletSourceCandidates returns the ordered entity-id sources for the message that holds a
// BulletId. The rule is fixed by message type: CreateBulletPush names its OwnerEntityId (else the
// sender in CombatCommon), DodgeInfoPush names BulletOwnerId, DamageContext sits under a
// DamageExecuteRequest or DamageExecuteNotify whose AttackerEntityId fired the bullet, and HitInformation.Id is the
// hitting entity (it equals the paired DamageExecuteRequest.AttackerEntityId in 1,563 of 1,815
// saved pairs; the rest differ and are therefore CORRELATED, not VERIFIED). Any other holder
// falls back to the enclosing CombatCommon.EntityId and says so in the basis.
func bulletSourceCandidates(message, parent, common *decodedMessage) []bulletSourceCandidate {
	var out []bulletSourceCandidate
	add := func(basis string, source *decodedMessage, name string) {
		if value, ok := scalarValue(source, name); ok && value > 0 {
			out = append(out, bulletSourceCandidate{basis, value})
		}
	}
	switch message.Type {
	case "CreateBulletPush", "CreateBulletRequest", "CreateBulletNotify":
		add(message.Type+".OwnerEntityId", message, "OwnerEntityId")
		add("CombatCommon.EntityId", common, "EntityId")
	case "DamageRecordNotify":
		add("DamageRecordNotify.Attacker", message, "Attacker")
	case "InterruptSkillInfo", "AwakeDynamicPortalRequest", "TargetGearHitRequest":
		add(message.Type+".EntityId", message, "EntityId")
	case "DodgeInfoPush":
		add("DodgeInfoPush.BulletOwnerId", message, "BulletOwnerId")
	case "DamageContext":
		if parent != nil {
			add(parent.Type+".AttackerEntityId", parent, "AttackerEntityId")
		}
	case "HitInformation":
		add("HitInformation.Id", message, "Id")
	default:
		add("CombatCommon.EntityId (default)", common, "EntityId")
	}
	return out
}

func (p *processor) writeBulletReference(frame gameFrame, message, parent *decodedMessage, fieldPath string, bulletID int64, common *decodedMessage, direction string) {
	record := bulletRefRecord{
		SchemaVersion: bulletRefSchemaVersion,
		FrameOrdinal:  p.dev.frameOrdinal - 1,
		SessionNumber: p.sessionNumber,
		Timestamp:     formatTimestamp(frame.timestamp),
		Direction:     direction,
		FieldPath:     strings.TrimPrefix(fieldPath, "Data."),
		BulletID:      bulletID,
		Status:        resolutionUnknown,
	}
	var skillID uint64
	if value, ok := scalarValue(message, "SkillId"); ok && value > 0 {
		record.SkillID = &value
		skillID = uint64(value)
	}
	if value, ok := scalarValue(message, "DtType"); ok {
		record.DtType = &value
	}
	candidates := bulletSourceCandidates(message, parent, common)
	var identity entityIdentity
	found := false
	if len(candidates) != 0 {
		candidate := candidates[0]
		id := candidate.id
		record.SourceEntityID, record.SourceBasis = &id, candidate.basis
		resolved := resolveEntityIdentity(p.entities, p.masters, uint64(candidate.id))
		if resolved.RuntimeStatus != resolutionUnknown {
			identity, found = resolved, true
		}
	}
	switch {
	case message.Type == "InterruptSkillInfo":
		p.resolveBulletHandle(&record)
	case p.masters == nil:
		record.Evidence = "no master database loaded"
	default:
		source := bulletSource{SkillID: skillID}
		if found {
			source = newBulletSource(identity, skillID, p.entities.visionSummonConfigs(uint64(*record.SourceEntityID)))
		}
		if resolution := resolveBullet(p.masters, uint64(bulletID), source); resolution != nil {
			record.Status, record.Namespace, record.ActorBlueprint, record.Via = resolution.Status, resolution.Namespace, resolution.ActorBlueprint, resolution.Via
			record.DevLabelZhCn, record.Evidence = resolution.DevLabelZh, resolution.Proof
			record.DevLabel = resolution.DevLabel
		}
	}
	if message.Type == "CreateBulletPush" || message.Type == "CreateBulletNotify" {
		p.rememberBulletHandle(message, record)
	}
	writeResearchRecord(p.dev.bulletRefOut, record, &p.dev.bulletRefErrors)
}
