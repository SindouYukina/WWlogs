//go:build wwdev

package capture

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func clientVersionAuditFields(report *clientVersionReport) (build, versions, commands string) {
	if report == nil {
		return "", "", ""
	}
	encoded, _ := json.Marshal(report.CheckClientVersions)
	commandIDs := make([]string, 0, len(report.SourceCommands))
	for _, commandID := range report.SourceCommands {
		commandIDs = append(commandIDs, fmt.Sprint(commandID))
	}
	return report.ClientBuild, string(encoded), strings.Join(commandIDs, ",")
}

// resolveDamageElement names the element written in the damage master row. The row is looked up by DamageId,
// the same basis as the damage class; the protocol's ElementType field is not used here.
func resolveDamageElement(masters *gameMaster, damageID uint64, hasDamageID bool) *combatEventElementJSONL {
	if masters == nil || !hasDamageID {
		return nil
	}
	record, ok := masters.damage[damageID]
	if !ok || !record.HasElement {
		return nil
	}
	result := &combatEventElementJSONL{ID: record.Element}
	if name, found := masters.elementNames[record.Element]; found {
		result.Name = name
		result.Status = resolutionCorrelated
		result.Evidence = fmt.Sprintf("damage.element of the DamageId row -> elementinfo_locale (%s); client build observed, master compatibility unverified", masters.locale)
	} else {
		result.Status = resolutionUnknown
		result.Evidence = "damage.element has no elementinfo_locale name for the master locale; raw element id kept"
	}
	return result
}

type combatEventCreditJSONL struct {
	CharacterID *uint64          `json:"characterId,omitempty"`
	Name        string           `json:"name,omitempty"`
	Status      resolutionStatus `json:"status"`
	NameStatus  resolutionStatus `json:"nameStatus"`
	Evidence    string           `json:"evidence,omitempty"`
}

type combatEventElementJSONL struct {
	ID       int32            `json:"id"`
	Name     string           `json:"name,omitempty"`
	Status   resolutionStatus `json:"status"`
	Evidence string           `json:"evidence,omitempty"`
}

// combatEventPushJSONL is what a server-executed DamageExecuteNotify adds to the common event fields of a development event
// line. The result itself went through the shared accounting (damage_notify.go); these fields only describe it.
type combatEventPushJSONL struct {
	Stream        string `json:"stream"`
	PairedWithRPC bool   `json:"pairedWithRpc"`
	// NotifyKey is the deduplication key of the result (notifyEventKey); zero when the frame had no sequence.
	NotifyKey      uint64 `json:"notifyKey"`
	NegativeStatus bool   `json:"negativeStatus"`
	// NegativeStatusLocal and NegativeStatusBasis are negativeStatusOwnership's decision for a negative-status result.
	NegativeStatusLocal bool   `json:"negativeStatusLocal,omitempty"`
	NegativeStatusBasis string `json:"negativeStatusBasis,omitempty"`
	// Credit* describe the role credit of a local negative-status result (negative_status_credit.go): the settlement
	// (forced, instant, own, applied, spread or undetermined), how many entities the evidence leaves as its cause, how many
	// overflows on other enemies can have spread it and whether an instant status Buff of its moment can be it, the basis
	// (negative_status_trigger or negative_status_owner), the credited role, the executing status Buff, the InvokePeriod or
	// instant status Buff that caused it, the instance owner, and why there is no credit.
	Settlement          string `json:"settlement,omitempty"`
	CreditCandidates    int    `json:"creditCandidates,omitempty"`
	SpreadCandidates    int    `json:"spreadCandidates,omitempty"`
	InstantTrigger      bool   `json:"instantTrigger,omitempty"`
	CreditBasis         string `json:"creditBasis,omitempty"`
	CreditRoleID        uint64 `json:"creditRoleId,omitempty"`
	CreditEntityID      uint64 `json:"creditEntityId,omitempty"`
	StatusEffectID      uint64 `json:"statusEffectId,omitempty"`
	TriggerEffectID     uint64 `json:"triggerEffectId,omitempty"`
	StatusOwnerEntityID uint64 `json:"statusOwnerEntityId,omitempty"`
	CreditReason        string `json:"creditReason,omitempty"`
	DamageRaw           int32  `json:"damageRaw"`
	ChangeLifeRaw       int32  `json:"changeLifeRaw"`
	HasChangeLife       bool   `json:"hasChangeLife"`
}

type combatEventJSONL struct {
	SchemaVersion              int                         `json:"schemaVersion"`
	EventType                  string                      `json:"eventType"`
	Timestamp                  time.Time                   `json:"timestamp"`
	SessionNumber              int                         `json:"sessionNumber"`
	RPCID                      uint64                      `json:"rpcId"`
	RequestEpoch               uint64                      `json:"requestEpoch,omitempty"` // wraps of the client's RequestId counter before the request
	Source                     combatEventEntityJSONL      `json:"source"`
	Target                     combatEventEntityJSONL      `json:"target"`
	Credit                     combatEventCreditJSONL      `json:"credit"`
	Summoner                   combatEventSummonerJSONL    `json:"summoner"`
	Skill                      combatEventSkillJSONL       `json:"skill"`
	CombatResult               combatEventResultJSONL      `json:"combatResult"`
	Healing                    *combatEventHealingJSONL    `json:"healing,omitempty"`
	Actors                     actorsJSONL                 `json:"actors"`
	Master                     combatEventMasterJSONL      `json:"master"`
	Raw                        combatEventRawJSONL         `json:"raw"`
	Bullet                     *combatEventBulletJSONL     `json:"bullet,omitempty"`
	DamageContributorCandidate *damageContributorCandidate `json:"damageContributorCandidate,omitempty"`
	// DamageOwners lists the static skill and Buff owners of the DamageId; it is a master reference, not the runtime attacker.
	DamageOwners *damageOwnersJSONL `json:"damageOwners,omitempty"`
	// Response is set on DAMAGE_RESPONSE_WITHOUT_AMOUNT events, whose server response carried no usable Damage field.
	Response *combatEventNoAmountJSONL `json:"response,omitempty"`
	// Push is set on DAMAGE_PUSH_RESULT events: server-pushed damage that no client request asked for.
	Push *combatEventPushJSONL `json:"push,omitempty"`
}

// combatEventNoAmountJSONL records why a server-confirmed hit has no amount. HasAmount=false means proto3 omitted Damage
// (the default 0) and the amount is therefore zero or not reported; the event never contributes to damage totals.
type combatEventNoAmountJSONL struct {
	HasAmount       bool   `json:"hasAmount"`
	ErrorCode       int32  `json:"errorCode"`
	ImmuneType      int32  `json:"immuneType"`
	ChangeLifeRaw   *int32 `json:"changeLifeRaw,omitempty"`
	Outcome         string `json:"outcome"`
	OutcomeEvidence string `json:"outcomeEvidence"`
}

// combatEventBulletJSONL is the static namespace translation of the event's BulletId.
// It is kept apart from Raw, which only copies wire values.
type combatEventBulletJSONL struct {
	BulletID       uint64 `json:"bulletId"`
	Namespace      string `json:"namespace,omitempty"`
	ActorBlueprint string `json:"actorBlueprint,omitempty"`
	Via            string `json:"via,omitempty"`
	DevLabelZhCn   string `json:"devLabelZhCn,omitempty"`
	// DevLabel is the localized developer note of the bullet row; never an official name.
	DevLabel    *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
	RowDamageID uint64                     `json:"rowDamageId,omitempty"`
	Status      resolutionStatus           `json:"status"`
	Evidence    string                     `json:"evidence,omitempty"`
}

// combatEventRawJSONL holds only values copied from the decoded damage request
// and response. Master lookups and runtime resolution never write here, so a
// consumer can compare this block with the wire without reproducing resolution.
type combatEventRawJSONL struct {
	DamageID         *uint64 `json:"damageId,omitempty"`
	AttackerEntityID *uint64 `json:"attackerEntityId,omitempty"`
	TargetEntityID   *uint64 `json:"targetEntityId,omitempty"`
	SkillID          *uint64 `json:"skillId,omitempty"`
	SkillMessageID   *uint64 `json:"skillMessageId,omitempty"`
	DamageSourceType *uint64 `json:"damageSourceType,omitempty"`
	BulletID         *int64  `json:"bulletId,omitempty"`
	ServerResultRaw  *int32  `json:"serverResultRaw,omitempty"`
	ChangeLifeRaw    *int32  `json:"changeLifeRaw,omitempty"`
}

type combatEventEntityJSONL struct {
	EntityID        *uint64                      `json:"entityId,omitempty"`
	RuntimeStatus   resolutionStatus             `json:"runtimeStatus"`
	RuntimeEvidence string                       `json:"runtimeEvidence,omitempty"`
	RuntimeFields   json.RawMessage              `json:"runtimeFields"`
	Lifecycle       entityLifecycleJSONL         `json:"lifecycle"`
	Character       *combatEventMasterIDJSONL    `json:"character,omitempty"`
	Monster         *combatEventMasterIDJSONL    `json:"monster,omitempty"`
	SceneTemplate   *combatEventTemplateJSONL    `json:"sceneTemplate,omitempty"`
	RoleSkin        *combatEventRoleSkinJSONL    `json:"roleSkin,omitempty"`
	SummonSkill     *combatEventSummonSkillJSONL `json:"summonSkill,omitempty"`
}

// combatEventSummonSkillJSONL is the SummonSkillId namespace translation of one entity.
type combatEventSummonSkillJSONL struct {
	SummonSkillID uint64           `json:"summonSkillId"`
	SummonType    *uint64          `json:"summonType,omitempty"`
	Namespace     string           `json:"namespace,omitempty"`
	Status        resolutionStatus `json:"status"`
	Evidence      string           `json:"evidence,omitempty"`
	// DevLabel is the developer note of the resolved Vision row.
	DevLabel *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
}

// combatEventRoleSkinJSONL is the RoleSkinId -> RoleSkin.RoleId translation of one entity.
type combatEventRoleSkinJSONL struct {
	SkinID       uint64           `json:"skinId"`
	MasterRoleID uint64           `json:"masterRoleId,omitempty"`
	Status       resolutionStatus `json:"status"`
	Evidence     string           `json:"evidence,omitempty"`
}

// entityLifecycleJSONL is the normalized lifecycle of one entity. State UNKNOWN
// with status UNKNOWN means no add, remove or scene snapshot named the entity.
type entityLifecycleJSONL struct {
	State             entityLifecycleState `json:"state"`
	Status            resolutionStatus     `json:"status"`
	AddCount          uint64               `json:"addCount"`
	RemoveCount       uint64               `json:"removeCount"`
	ReusedAfterRemove uint64               `json:"reusedAfterRemove"`
	LastSource        string               `json:"lastSource,omitempty"`
	Evidence          string               `json:"evidence,omitempty"`
}

type combatEventMasterIDJSONL struct {
	ID       uint64           `json:"id"`
	Name     string           `json:"name,omitempty"`
	Status   resolutionStatus `json:"status"`
	Evidence string           `json:"evidence,omitempty"`
}

type combatEventTemplateJSONL struct {
	Name         string           `json:"name,omitempty"`
	Subtitle     string           `json:"subtitle,omitempty"`
	IdentityType string           `json:"identityType"`
	Status       resolutionStatus `json:"status"`
	NameStatus   resolutionStatus `json:"nameStatus"`
	// DevLabel is set instead of Name when the single candidate has only a developer note; never an official name.
	DevLabel   *combatEventLabelTextJSONL          `json:"devLabel,omitempty"`
	Candidates []combatEventTemplateCandidateJSONL `json:"candidates"`
	Evidence   string                              `json:"evidence,omitempty"`
}

type combatEventTemplateCandidateJSONL sceneTemplateCandidate

type combatEventSummonerJSONL struct {
	EntityIDs   []uint64         `json:"entityIds,omitempty"`
	CharacterID *uint64          `json:"characterId,omitempty"`
	Name        string           `json:"name,omitempty"`
	Status      resolutionStatus `json:"status"`
	Evidence    string           `json:"evidence,omitempty"`
}

type combatEventSkillJSONL struct {
	ID         uint64                    `json:"id"`
	MessageID  uint64                    `json:"messageId"`
	Name       string                    `json:"name,omitempty"`
	NameKind   string                    `json:"nameKind,omitempty"`
	Status     resolutionStatus          `json:"status"`
	NameStatus resolutionStatus          `json:"nameStatus"`
	Evidence   string                    `json:"evidence,omitempty"`
	Request    *combatActionRequestJSONL `json:"request,omitempty"`
	// DevLabelZhCn is the SkillInfo developer label; it is not an official localized name and never fills Name.
	DevLabelZhCn   string           `json:"devLabelZhCn,omitempty"`
	DevLabelStatus resolutionStatus `json:"devLabelStatus,omitempty"`
	DevLabelProof  string           `json:"devLabelEvidence,omitempty"`
	// DevLabel is the developer label as display text in the master locale (zh-CN original as fallback).
	DevLabel *combatEventLabelTextJSONL `json:"devLabel,omitempty"`
}

type combatActionRequestJSONL struct {
	SessionNumber      int              `json:"sessionNumber"`
	MessageID          uint64           `json:"messageId"`
	EntityID           uint64           `json:"entityId,omitempty"`
	SkillID            uint64           `json:"skillId"`
	TargetID           uint64           `json:"targetId,omitempty"`
	RequestType        string           `json:"requestType"`
	Timestamp          string           `json:"timestamp"`
	LinkedDamageEvents int              `json:"linkedDamageEvents"`
	LinkStatus         resolutionStatus `json:"linkStatus"`
	Evidence           string           `json:"evidence"`
}

type combatEventResultJSONL struct {
	DamageID        *uint64          `json:"damageId,omitempty"`
	SourceType      *uint64          `json:"sourceType,omitempty"`
	BulletID        *int64           `json:"bulletId,omitempty"`
	CombatResultAbs int64            `json:"combatResultAbs"`
	ServerResultRaw *int32           `json:"serverResultRaw,omitempty"`
	ChangeLifeRaw   *int32           `json:"changeLifeRaw,omitempty"`
	CalculateType   *int32           `json:"calculateType,omitempty"`
	ClassCandidate  string           `json:"classCandidate,omitempty"`
	ClassStatus     resolutionStatus `json:"classStatus"`
	ClassEvidence   string           `json:"classEvidence,omitempty"`
	// Element names damage.element of the DamageId row; absent when the row is unknown or has no element.
	Element *combatEventElementJSONL `json:"element,omitempty"`
}

type combatEventHealingJSONL struct {
	ReportedMagnitudeAbs int64            `json:"reportedMagnitudeAbs"`
	MagnitudeStatus      resolutionStatus `json:"magnitudeStatus"`
	MagnitudeEvidence    string           `json:"magnitudeEvidence,omitempty"`
	EffectiveAmountAbs   *int64           `json:"effectiveAmountAbs"`
	EffectiveStatus      resolutionStatus `json:"effectiveStatus"`
	HPDeltaStatus        resolutionStatus `json:"hpDeltaStatus"`
	OverhealAmountAbs    *int64           `json:"overhealAmountAbs"`
	OverhealStatus       resolutionStatus `json:"overhealStatus"`
}

type combatEventMasterJSONL struct {
	GameVersion     string `json:"gameVersion,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	ClientRegion    string `json:"clientRegion,omitempty"`
	Locale          string `json:"locale,omitempty"`
	SourceHash      string `json:"sourceHash,omitempty"`
}

type optionalRuntimeID struct {
	Value   uint64 `json:"value"`
	Present bool   `json:"present"`
}

type runtimePlayerFollowerAudit struct {
	Type     optionalRuntimeID `json:"type"`
	EntityID optionalRuntimeID `json:"entityId"`
}

type runtimeIdentityAudit struct {
	EntityID                 optionalRuntimeID            `json:"entityId"`
	SceneInstanceID          optionalRuntimeID            `json:"sceneInstanceId"`
	EntityType               optionalRuntimeID            `json:"entityType"`
	ConfigID                 optionalRuntimeID            `json:"configId"`
	ConfigType               optionalRuntimeID            `json:"configType"`
	PrefabID                 optionalRuntimeID            `json:"prefabId"`
	PlayerID                 optionalRuntimeID            `json:"playerId"`
	OwnerID                  optionalRuntimeID            `json:"ownerId"`
	OwnerIncID               optionalRuntimeID            `json:"ownerIncId"`
	SummonerID               optionalRuntimeID            `json:"summonerId"`
	SummonerPlayerID         optionalRuntimeID            `json:"summonerPlayerId"`
	SummonConfigID           optionalRuntimeID            `json:"summonConfigId"`
	SummonSkillID            optionalRuntimeID            `json:"summonSkillId"`
	SummonType               optionalRuntimeID            `json:"summonType"`
	SceneSummonerID          optionalRuntimeID            `json:"sceneSummonerId"`
	SceneSummonConfigID      optionalRuntimeID            `json:"sceneSummonConfigId"`
	SceneSummonSkillID       optionalRuntimeID            `json:"sceneSummonSkillId"`
	PlayerEntityID           optionalRuntimeID            `json:"playerEntityId"`
	FollowerSummonConfigID   optionalRuntimeID            `json:"followerSummonConfigId"`
	PhantomRoleEntityID      optionalRuntimeID            `json:"phantomRoleEntityId"`
	ConcomitantVisionIDs     []uint64                     `json:"concomitantVisionEntityIds,omitempty"`
	ConcomitantCustomIDs     []uint64                     `json:"concomitantCustomEntityIds,omitempty"`
	WeakVisionEntityID       optionalRuntimeID            `json:"weakVisionEntityId"`
	FollowEntityID           optionalRuntimeID            `json:"followEntityId"`
	PlayerFollowers          []runtimePlayerFollowerAudit `json:"playerFollowers,omitempty"`
	MonsterID                optionalRuntimeID            `json:"monsterId"`
	MonsterCaptureEntityID   optionalRuntimeID            `json:"monsterCaptureEntityId"`
	TemplateID               optionalRuntimeID            `json:"templateId"`
	RoleSkinID               optionalRuntimeID            `json:"roleSkinId"`
	RoleID                   optionalRuntimeID            `json:"roleId"`
	RoleConflict             bool                         `json:"roleConflict"`
	RoleSourceFieldPath      string                       `json:"roleSourceFieldPath,omitempty"`
	SourceMessageID          uint16                       `json:"sourceMessageId"`
	SourceFieldPath          string                       `json:"sourceFieldPath,omitempty"`
	ComponentSourceMessageID uint16                       `json:"componentSourceMessageId,omitempty"`
	ComponentSourceFieldPath string                       `json:"componentSourceFieldPath,omitempty"`
}

func sceneTemplateEventLabel(identity entityIdentity, entityID string) (string, bool) {
	if len(identity.TemplateCandidates) != 1 || identity.TemplateStatus == resolutionUnknown {
		return "", false
	}
	candidate := identity.TemplateCandidates[0]
	if candidate.Name == "" || candidate.NameStatus != string(resolutionVerified) {
		if candidate.DevLabel != nil && candidate.DevLabel.Text != "" {
			return fmt.Sprintf("[internal] %s [Template:%d; %s] [Entity:%s]", candidate.DevLabel.Text, candidate.TemplateID, identity.TemplateStatus, entityID), true
		}
		return "", false
	}
	return fmt.Sprintf("%s [Template:%d; %s] [Entity:%s]", candidate.Name, candidate.TemplateID, identity.TemplateStatus, entityID), true
}

func summonSkillJSONL(identity entityIdentity) *combatEventSummonSkillJSONL {
	if identity.SummonSkill == nil {
		return nil
	}
	view := &combatEventSummonSkillJSONL{SummonSkillID: identity.SummonSkill.SummonSkillID, Namespace: identity.SummonSkill.Namespace, Status: identity.SummonSkill.Status, Evidence: identity.SummonSkill.Proof, DevLabel: identity.SummonSkill.DevLabel}
	if identity.SummonSkill.HasSummonType {
		view.SummonType = &identity.SummonSkill.SummonType
	}
	return view
}

func roleSkinJSONL(identity entityIdentity) *combatEventRoleSkinJSONL {
	if identity.RoleSkin == nil {
		return nil
	}
	return &combatEventRoleSkinJSONL{SkinID: identity.RoleSkin.SkinID, MasterRoleID: identity.RoleSkin.MasterRoleID, Status: identity.RoleSkin.Status, Evidence: identity.RoleSkin.Proof}
}

func lifecycleJSONL(view entityLifecycleView) entityLifecycleJSONL {
	if !view.Known {
		return entityLifecycleJSONL{State: lifecycleUnknown, Status: resolutionUnknown, Evidence: "no EntityAddNotify, EntityRemoveNotify or JoinSceneNotify observed for this entity ID"}
	}
	return entityLifecycleJSONL{
		State: view.State, Status: resolutionVerified,
		AddCount: view.AddCount, RemoveCount: view.RemoveCount, ReusedAfterRemove: view.ReusedAfterRemove,
		LastSource: view.LastSource, Evidence: lifecycleEvidence(view.LastSource),
	}
}

func lifecycleEvidence(source string) string {
	switch source {
	case "EntityAddNotify":
		return "EntityAddNotify.EntityPb.Id"
	case "EntityRemoveNotify":
		return "EntityRemoveNotify.EntityRemoveInfo.Id"
	case "JoinSceneNotify":
		return "JoinSceneNotify.SceneInfo entity snapshot"
	default:
		return ""
	}
}

func jsonlView(event combatEvent, masters *gameMaster) combatEventJSONL {
	view := combatEventJSONL{
		SchemaVersion: 5,
		EventType:     combatEventJSONLType(event.EventTypeCandidate),
		Timestamp:     event.Timestamp,
		SessionNumber: event.SessionNumber,
		RPCID:         event.RPCID,
		RequestEpoch:  event.RequestEpoch,
		Source:        combatEventEntityJSONL{RuntimeStatus: event.Source.RuntimeStatus, RuntimeEvidence: event.Source.RuntimeProof, RuntimeFields: json.RawMessage(marshalRuntimeIdentity(event.Source.Runtime))},
		Target:        combatEventEntityJSONL{RuntimeStatus: event.Target.RuntimeStatus, RuntimeEvidence: event.Target.RuntimeProof, RuntimeFields: json.RawMessage(marshalRuntimeIdentity(event.Target.Runtime))},
		Credit:        combatEventCreditJSONL{Status: event.CharacterStatus, NameStatus: event.CharacterNameStatus, Evidence: event.CharacterProof},
		Summoner:      combatEventSummonerJSONL{EntityIDs: append([]uint64(nil), event.SummonerCharacter.EntityIDs...), Status: event.SummonerCharacter.Status, Evidence: event.SummonerCharacter.Proof},
		Skill:         combatEventSkillJSONL{ID: event.SkillID, MessageID: event.SkillMessageID, Name: event.SkillName, NameKind: event.SkillNameKind, Status: event.SkillStatus, NameStatus: event.SkillNameStatus, Evidence: event.SkillProof},
		CombatResult: combatEventResultJSONL{
			DamageID: optionalUint64(event.DamageID, event.HasDamageID), SourceType: optionalUint64(event.DamageSourceType, event.HasDamageSourceType),
			BulletID: optionalInt64(event.BulletID, event.HasBulletID), CombatResultAbs: event.Damage,
			ServerResultRaw: optionalInt32(event.ServerResultRaw, event.HasServerResult), ChangeLifeRaw: optionalInt32(event.ChangeLifeRaw, event.HasChangeLife),
			CalculateType: optionalInt32(event.DamageCalculateType, event.HasCalculateType), ClassCandidate: event.EventTypeCandidate,
			ClassStatus: event.EventTypeStatus, ClassEvidence: event.EventTypeProof,
		},
	}
	if label := resolveSkillLabel(masters, event.SkillID); label != nil {
		view.Skill.DevLabelZhCn, view.Skill.DevLabelStatus, view.Skill.DevLabelProof = label.Label, label.Status, label.Evidence
	}
	view.Skill.DevLabel = resolveSkillLabelText(masters, event.SkillID)
	view.CombatResult.Element = resolveDamageElement(masters, event.DamageID, event.HasDamageID)
	view.Actors = projectActors(event)
	if event.HasBulletID && event.BulletID > 0 {
		source := newBulletSource(event.Source, event.SkillID, event.SourceVisionSummonCfgs)
		if resolution := resolveBullet(masters, uint64(event.BulletID), source); resolution != nil {
			view.Bullet = &combatEventBulletJSONL{BulletID: resolution.BulletID, Namespace: resolution.Namespace, ActorBlueprint: resolution.ActorBlueprint, Via: resolution.Via, DevLabelZhCn: resolution.DevLabelZh, DevLabel: resolution.DevLabel, RowDamageID: resolution.RowDamageID, Status: resolution.Status, Evidence: resolution.Proof}
		}
	}
	view.DamageContributorCandidate = event.DamageContributorCandidate
	view.DamageOwners = resolveDamageOwners(masters, event.DamageID, event.HasDamageID)
	view.Raw = combatEventRawJSONL{
		DamageID: optionalUint64(event.DamageID, event.HasDamageID), AttackerEntityID: optionalUint64(event.SourceEntityID, event.HasSourceEntity),
		TargetEntityID: optionalUint64(event.TargetEntityID, event.HasTargetEntity), SkillID: optionalUint64(event.SkillID, event.SkillID != 0),
		SkillMessageID: optionalUint64(event.SkillMessageID, event.SkillMessageID != 0), DamageSourceType: optionalUint64(event.DamageSourceType, event.HasDamageSourceType),
		BulletID: optionalInt64(event.BulletID, event.HasBulletID), ServerResultRaw: optionalInt32(event.ServerResultRaw, event.HasServerResult),
		ChangeLifeRaw: optionalInt32(event.ChangeLifeRaw, event.HasChangeLife),
	}
	if event.EventTypeCandidate == "HEALING_CANDIDATE" {
		view.Healing = &combatEventHealingJSONL{
			ReportedMagnitudeAbs: event.Damage, MagnitudeStatus: event.EventTypeStatus, MagnitudeEvidence: event.EventTypeProof,
			EffectiveStatus: resolutionUnknown, HPDeltaStatus: resolutionUnknown, OverhealStatus: resolutionUnknown,
		}
	}
	view.Source.EntityID = optionalUint64(event.SourceEntityID, event.HasSourceEntity)
	view.Target.EntityID = optionalUint64(event.TargetEntityID, event.HasTargetEntity)
	view.Source.Lifecycle = lifecycleJSONL(event.Source.Lifecycle)
	view.Source.RoleSkin = roleSkinJSONL(event.Source)
	view.Target.RoleSkin = roleSkinJSONL(event.Target)
	view.Source.SummonSkill = summonSkillJSONL(event.Source)
	view.Target.SummonSkill = summonSkillJSONL(event.Target)
	view.Target.Lifecycle = lifecycleJSONL(event.Target.Lifecycle)
	if event.Source.HasCharacter {
		view.Source.Character = &combatEventMasterIDJSONL{ID: event.Source.Character.ID, Name: event.Source.Character.Name, Status: event.Source.CharacterStatus, Evidence: event.Source.CharacterProof}
	}
	if event.Source.HasMonster {
		view.Source.Monster = &combatEventMasterIDJSONL{ID: event.Source.Monster.ID, Name: event.Source.Monster.Name, Status: event.Source.MonsterStatus, Evidence: event.Source.MonsterProof}
	}
	applySceneTemplateJSONL(&view.Source, event.Source)
	if event.Target.HasCharacter {
		view.Target.Character = &combatEventMasterIDJSONL{ID: event.Target.Character.ID, Name: event.Target.Character.Name, Status: event.Target.CharacterStatus, Evidence: event.Target.CharacterProof}
	}
	if event.Target.HasMonster {
		view.Target.Monster = &combatEventMasterIDJSONL{ID: event.Target.Monster.ID, Name: event.Target.Monster.Name, Status: event.Target.MonsterStatus, Evidence: event.Target.MonsterProof}
	}
	applySceneTemplateJSONL(&view.Target, event.Target)
	view.Credit.CharacterID = optionalUint64(event.CharacterID, event.CharacterID != 0)
	view.Credit.Name = event.CharacterName
	creditJSONLFallback(&view.Credit, event)
	view.Summoner.CharacterID = optionalUint64(event.SummonerCharacter.CharacterID, event.SummonerCharacter.HasCharacterID)
	if event.SummonerCharacter.HasCharacter {
		view.Summoner.Name = event.SummonerCharacter.Character.Name
	}
	if masters != nil {
		view.Master = combatEventMasterJSONL{
			ClientRegion: masters.manifest.ClientRegion, Locale: masters.locale,
			SourceHash: masters.manifest.SourceHash,
		}
	}
	return view
}

func applySceneTemplateJSONL(destination *combatEventEntityJSONL, identity entityIdentity) {
	if len(identity.TemplateCandidates) == 0 {
		return
	}
	template := &combatEventTemplateJSONL{
		IdentityType: "template-derived",
		Status:       identity.TemplateStatus,
		NameStatus:   resolutionUnknown,
		Candidates:   make([]combatEventTemplateCandidateJSONL, 0, len(identity.TemplateCandidates)),
		Evidence:     identity.TemplateProof,
	}
	for _, candidate := range identity.TemplateCandidates {
		if candidate.NameStatus == "" {
			candidate.NameStatus = string(resolutionUnknown)
		}
		template.Candidates = append(template.Candidates, combatEventTemplateCandidateJSONL(candidate))
	}
	if len(identity.TemplateCandidates) == 1 && (identity.TemplateStatus == resolutionCorrelated || identity.TemplateStatus == resolutionVerified) {
		candidate := identity.TemplateCandidates[0]
		if candidate.Name != "" && candidate.NameStatus == string(resolutionVerified) {
			template.Name = candidate.Name
			template.Subtitle = candidate.Subtitle
			template.NameStatus = resolutionVerified
		} else if candidate.DevLabel != nil {
			template.DevLabel = candidate.DevLabel
		}
	}
	destination.SceneTemplate = template
}

func combatEventJSONLType(candidate string) string {
	switch candidate {
	case "DAMAGE_CANDIDATE":
		return "DAMAGE_RESULT"
	case "HEALING_CANDIDATE":
		return "HEALING_RESULT_CANDIDATE"
	case "OTHER_COMBAT_RESULT_CANDIDATE":
		return "OTHER_COMBAT_RESULT_CANDIDATE"
	default:
		return "UNCLASSIFIED_RESULT"
	}
}

func optionalUint64(value uint64, present bool) *uint64 {
	if !present {
		return nil
	}
	return &value
}

func optionalInt64(value int64, present bool) *int64 {
	if !present {
		return nil
	}
	return &value
}

func optionalInt32(value int32, present bool) *int32 {
	if !present {
		return nil
	}
	return &value
}

func (p *processor) writeStructuredEventJSONL(event any) {
	if row, ok := event.(buffLifecycleAuditRow); ok {
		event = withMessagePath(row)
	}
	p.writeEnvelopedEventJSONL(event, "")
}

// skillRequestJSONL is the research link of a damage result to the outbound skill request with its SkillMessageId
// (linkSkillRequest), or nil without one. Only the research schema (-protocol-schema) records outbound requests.
func skillRequestJSONL(request *combatActionRequest) *combatActionRequestJSONL {
	if request == nil {
		return nil
	}
	return &combatActionRequestJSONL{
		SessionNumber: request.SessionNumber, MessageID: request.MessageID, EntityID: request.EntityID, SkillID: request.SkillID,
		TargetID: request.TargetID, RequestType: request.RequestType, Timestamp: request.Timestamp, LinkedDamageEvents: request.LinkedDamageEvents,
		LinkStatus: resolutionVerified,
		Evidence:   "outbound CombatSendPackRequest CombatCommon.MessageId exactly equals DamageContext.SkillMessageId; damage request is separately paired with server response by RPC ID",
	}
}

// writeDamageEventJSONL writes the development line of one damage result, with its research skill-request link. A
// server-executed DamageExecuteNotify is a DAMAGE_NOTIFY_RESULT in the auxiliary "p" id namespace, so response event ids
// stay as they were before notified results were counted.
func (p *processor) writeDamageEventJSONL(event combatEvent, skillRequest *combatActionRequest) {
	view := jsonlView(event, p.masters)
	view.Skill.Request = skillRequestJSONL(skillRequest)
	if event.Notify == nil {
		p.writeStructuredEventJSONL(view)
		return
	}
	view.EventType = "DAMAGE_NOTIFY_RESULT"
	view.Push = &combatEventPushJSONL{
		Stream: "DAMAGE_EXECUTE_NOTIFY", NotifyKey: event.Notify.Key,
		NegativeStatus: event.Notify.NegativeStatus, NegativeStatusLocal: event.Notify.NegativeStatusLocal, NegativeStatusBasis: event.Notify.NegativeStatusBasis,
		DamageRaw: event.ServerResultRaw, ChangeLifeRaw: event.ChangeLifeRaw, HasChangeLife: event.HasChangeLife,
	}
	if event.Notify.NegativeStatusLocal {
		execution := event.Notify.Execution
		view.Push.CreditEntityID, view.Push.StatusEffectID, view.Push.TriggerEffectID = execution.CreditEntity, execution.StatusEffectID, execution.TriggerEffectID
		view.Push.StatusOwnerEntityID, view.Push.CreditReason = execution.Owner, execution.Reason
		view.Push.Settlement, view.Push.CreditCandidates = execution.Settlement, execution.Candidates
		view.Push.SpreadCandidates, view.Push.InstantTrigger = execution.Spread, execution.Instant
		if credit := event.Notify.Credit; credit != nil {
			view.Push.CreditBasis, view.Push.CreditRoleID = credit.Basis, credit.RoleID
		}
	}
	p.writeEnvelopedEventJSONL(view, "p")
}

// writeDamageEventOutputs writes the development forms of one damage result after writeDamageEvent has recorded it in
// the core: the research link to its outbound skill request, the overlap record, the event line, the console line, the
// event audit row and the research lines. A failed console or audit write is counted (diagnostic_isolation_development.go); it never
// returns before the core counters, and a console line that failed is not flushed, so it is counted once.
func (p *processor) writeDamageEventOutputs(event *combatEvent, visible bool, sourceRelation, targetRelation playerRelation) {
	skillRequest := p.linkSkillRequest(event.SkillMessageID)
	if visible {
		p.recordDamageForOverlap(*event)
	}
	p.writeDamageEventJSONL(*event, skillRequest)
	consoleWritten := true
	if visible {
		normalOutput := formatNormalForSourceRelation(*event, sourceRelation)
		switch {
		case sourceRelation == playerRelationLocal && targetRelation == playerRelationLocal:
			normalOutput += " [PlayerTeamSide: BOTH]"
		case sourceRelation == playerRelationLocal:
			normalOutput += " [PlayerTeamSide: SOURCE]"
		case targetRelation == playerRelationLocal:
			normalOutput += " [PlayerTeamSide: TARGET]"
		default:
			normalOutput += " [PlayerTeamSide: UNCONFIRMED]"
		}
		if _, err := fmt.Fprintln(p.dev.stdout, normalOutput); err != nil {
			p.noteDiagnosticFailure("console", err)
			consoleWritten = false
		}
	}
	if p.dev.audit != nil {
		summonerID := "?"
		if event.Source.Runtime.hasSummonerID {
			summonerID = fmt.Sprintf("%d", event.Source.Runtime.summonerID)
		}
		playerEntityID := "?"
		if event.Source.Runtime.hasPlayerEntityID {
			playerEntityID = fmt.Sprintf("%d", event.Source.Runtime.playerEntityID)
		}
		monsterID := "?"
		if event.Target.HasMonster {
			monsterID = fmt.Sprintf("%d", event.Target.Monster.ID)
		}
		characterID := "?"
		if event.CharacterID != 0 {
			characterID = fmt.Sprintf("%d", event.CharacterID)
		}
		sourceEntityID := "?"
		if event.HasSourceEntity {
			sourceEntityID = fmt.Sprintf("%d", event.SourceEntityID)
		}
		targetEntityID := "?"
		if event.HasTargetEntity {
			targetEntityID = fmt.Sprintf("%d", event.TargetEntityID)
		}
		fields := []string{
			formatTimestamp(event.Timestamp), fmt.Sprintf("%d", event.RPCID), sourceEntityID, targetEntityID,
			string(event.Source.RuntimeStatus), event.Source.RuntimeProof,
			string(event.Target.RuntimeStatus), event.Target.RuntimeProof, characterID,
			event.CharacterName, string(event.CharacterStatus), event.CharacterProof,
			fmt.Sprintf("%d", event.SkillID), event.SkillName, string(event.SkillStatus), event.SkillProof,
			fmt.Sprintf("%d", event.SkillMessageID), formatOptionalUint(event.DamageID, event.HasDamageID), monsterID,
			event.Target.Monster.Name, string(event.Target.MonsterStatus), event.Target.MonsterProof, summonerID,
			formatOptionalUint(event.SummonerCharacter.CharacterID, event.SummonerCharacter.HasCharacterID),
			event.SummonerCharacter.Character.Name, string(event.SummonerCharacter.Status), event.SummonerCharacter.Proof,
			fmt.Sprint(event.SummonerCharacter.EntityIDs), playerEntityID,
			fmt.Sprintf("%d", event.Damage),
			formatOptionalUint(event.DamageSourceType, event.HasDamageSourceType), formatOptionalInt(event.BulletID, event.HasBulletID),
			marshalRuntimeIdentity(event.Source.Runtime), marshalRuntimeIdentity(event.Target.Runtime),
			formatOptionalInt(int64(event.ServerResultRaw), event.HasServerResult),
			formatOptionalInt(int64(event.ChangeLifeRaw), event.HasChangeLife),
			formatOptionalInt(int64(event.DamageCalculateType), event.HasCalculateType), event.EventTypeCandidate,
			string(event.EventTypeStatus), event.EventTypeProof,
			string(event.CharacterNameStatus), string(event.SkillNameStatus),
		}
		build, versionFields, versionCommands := clientVersionAuditFields(p.clientVersion)
		fields = append(fields, build, versionFields, versionCommands)
		for i := range fields {
			fields[i] = strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(fields[i])
		}
		if _, err := fmt.Fprintln(p.dev.audit, strings.Join(fields, "\t")); err != nil {
			p.noteDiagnosticFailure("event-audit", err)
		}
	}
	if p.dev.research {
		fmt.Fprintf(p.diagnostic, "[DAMAGE] rpc=%d source_entity=%d target_entity=%d skill_id=%d damage_id=%d source_type=%s bullet_id=%s damage=%d\n", event.RPCID, event.SourceEntityID, event.TargetEntityID, event.SkillID, event.DamageID, optionalDamageSource(event.DamageSourceType, event.HasDamageSourceType), formatOptionalInt(event.BulletID, event.HasBulletID), event.Damage)
		summonerID := "?"
		if event.Source.Runtime.hasSummonerID {
			summonerID = fmt.Sprintf("%d", event.Source.Runtime.summonerID)
		}
		playerEntityID := "?"
		if event.Source.Runtime.hasPlayerEntityID {
			playerEntityID = fmt.Sprintf("%d", event.Source.Runtime.playerEntityID)
		}
		fmt.Fprintf(p.diagnostic, "[ENTITY_RESOLVE] source_status=%s source_evidence=%q source_type=%d source_summoner_id=%s source_summoner_chain=%v source_summoner_character_id=%d source_summoner_character_name=%q source_summoner_character_status=%s source_summoner_evidence=%q source_player_entity_id=%s character_id=%d character_name=%q character_status=%s character_name_status=%s character_evidence=%q target_status=%s target_evidence=%q monster_id=%d monster_name=%q monster_status=%s monster_name_status=%s monster_evidence=%q\n",
			event.Source.RuntimeStatus, event.Source.RuntimeProof, event.Source.Runtime.entityTypeCode, summonerID,
			event.SummonerCharacter.EntityIDs, event.SummonerCharacter.CharacterID, event.SummonerCharacter.Character.Name, event.SummonerCharacter.Status, event.SummonerCharacter.Proof, playerEntityID,
			event.CharacterID, event.CharacterName, event.CharacterStatus, event.CharacterNameStatus, event.CharacterProof,
			event.Target.RuntimeStatus, event.Target.RuntimeProof, event.Target.Monster.ID, event.Target.Monster.Name, event.Target.MonsterStatus, event.Target.Monster.NameStatus, event.Target.MonsterProof)
		fmt.Fprintf(p.diagnostic, "[SKILL_RESOLVE] skill_id=%d name=%q kind=%q status=%s name_status=%s master_locale=%q source=%q\n",
			event.SkillID, event.SkillName, event.SkillNameKind, event.SkillStatus, event.SkillNameStatus,
			masterLocale(p.masters), event.SkillProof)
	}
	if flusher, ok := p.dev.stdout.(interface{ Flush() error }); ok && visible && consoleWritten {
		if err := flusher.Flush(); err != nil {
			p.noteDiagnosticFailure("console", err)
		}
	}
}

func formatOptionalUint(value uint64, present bool) string {
	if !present {
		return "?"
	}
	return fmt.Sprintf("%d", value)
}

func formatOptionalInt(value int64, present bool) string {
	if !present {
		return "?"
	}
	return fmt.Sprintf("%d", value)
}

func formatTimestamp(timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}
	return timestamp.UTC().Format(time.RFC3339Nano)
}

func masterLocale(masters *gameMaster) string {
	if masters == nil {
		return ""
	}
	return masters.locale
}

// writeEnvelopedEventJSONL writes one event line; a non-empty kind selects an auxiliary id namespace.
func (p *processor) writeEnvelopedEventJSONL(event any, kind string) {
	if p.dev.combatEventsJSONL == nil {
		return
	}
	envelope := eventEnvelope{}
	if kind != "" {
		envelope = p.nextAuxEventEnvelope(kind)
	} else {
		envelope = p.nextEventEnvelope()
	}
	line, err := prependEnvelope(envelope, event)
	if err != nil {
		p.noteDiagnosticFailure("combat-events", err)
		return
	}
	if _, err := p.dev.combatEventsJSONL.Write(line); err != nil {
		p.noteDiagnosticFailure("combat-events", err)
	}
}

func formatNormal(event combatEvent) string {
	return formatNormalForSourceRelation(event, playerRelationUnknown)
}

func formatNormalForSourceRelation(event combatEvent, sourceRelation playerRelation) string {
	timestamp := "--:--:--.---"
	if !event.Timestamp.IsZero() {
		timestamp = event.Timestamp.Local().Format("15:04:05.000")
	}
	sourceID := "?"
	if event.HasSourceEntity {
		sourceID = fmt.Sprintf("%d", event.SourceEntityID)
	}
	source := fmt.Sprintf("Unknown [Entity:%s]", sourceID)
	if event.Source.HasMonster && (event.Source.MonsterStatus == resolutionVerified || event.Source.MonsterStatus == resolutionCorrelated) {
		if event.Source.Monster.Name != "" && (event.Source.Monster.NameStatus == string(resolutionVerified) || event.Source.Monster.NameStatus == string(resolutionCorrelated)) {
			source = fmt.Sprintf("%s [Monster:%d] [Entity:%s]", event.Source.Monster.Name, event.Source.Monster.ID, sourceID)
		} else {
			source = fmt.Sprintf("Monster:%d [name %s] [Entity:%s]", event.Source.Monster.ID, event.Source.Monster.NameStatus, sourceID)
		}
	}
	credit := "Credit:?"
	if event.DamageContributorCandidate != nil && event.DamageContributorCandidate.ActorType == "Vehicle" {
		credit = "Credit:[Vehicle]"
		if name := event.DamageContributorCandidate.Name; name != nil && name.Text != "" {
			credit = fmt.Sprintf("Credit:[Vehicle] [internal] %s", name.Text)
		}
		credit += fmt.Sprintf(" [%s]", event.DamageContributorCandidate.Status)
	} else if event.CharacterID != 0 && (event.CharacterStatus == resolutionVerified || event.CharacterStatus == resolutionCorrelated) {
		if event.CharacterName != "" {
			credit = fmt.Sprintf("Credit:%s [%s]", event.CharacterName, event.CharacterStatus)
			if event.CharacterNameStatus != "" && event.CharacterNameStatus != event.CharacterStatus {
				credit = fmt.Sprintf("Credit:%s [%s; name %s]", event.CharacterName, event.CharacterStatus, event.CharacterNameStatus)
			}
		} else {
			credit = fmt.Sprintf("Credit:Character:%d [%s; name %s]", event.CharacterID, event.CharacterStatus, event.CharacterNameStatus)
		}
	}
	if credit == "Credit:?" && event.Source.HasMonster && (event.Source.MonsterStatus == resolutionVerified || event.Source.MonsterStatus == resolutionCorrelated) {
		credit = fmt.Sprintf("Credit:NonPlayerSource [Monster:%d; %s]", event.Source.Monster.ID, event.Source.MonsterStatus)
	}
	if credit == "Credit:?" && len(event.Source.TemplateCandidates) == 1 && (event.Source.TemplateStatus == resolutionVerified || event.Source.TemplateStatus == resolutionCorrelated) {
		credit = fmt.Sprintf("Credit:NonPlayerSource [Template:%d; %s]", event.Source.TemplateCandidates[0].TemplateID, event.Source.TemplateStatus)
	}
	if credit == "Credit:?" && sourceRelation == playerRelationLocal {
		credit = "Credit:PlayerTeam [role not resolved]"
	}
	skillID := "?"
	if event.SkillID != 0 {
		skillID = fmt.Sprintf("%d", event.SkillID)
	}
	skill := fmt.Sprintf("SkillId:%s", skillID)
	if event.SkillName != "" && (event.SkillNameStatus == resolutionVerified || event.SkillNameStatus == resolutionCorrelated) {
		skill = fmt.Sprintf("%s [SkillId:%s, %s]", event.SkillName, skillID, event.SkillStatus)
		if event.SkillNameStatus != "" && event.SkillNameStatus != event.SkillStatus {
			skill = fmt.Sprintf("%s [SkillId:%s, %s; name %s]", event.SkillName, skillID, event.SkillStatus, event.SkillNameStatus)
		}
		if event.SkillNameKind == "SkillButtonLabel" {
			skill = fmt.Sprintf("%s [UI label, SkillId:%s, %s]", event.SkillName, skillID, event.SkillStatus)
			if event.SkillNameStatus != "" && event.SkillNameStatus != event.SkillStatus {
				skill = fmt.Sprintf("%s [UI label, SkillId:%s, %s; name %s]", event.SkillName, skillID, event.SkillStatus, event.SkillNameStatus)
			}
		}
	}
	if skill == fmt.Sprintf("SkillId:%s", skillID) && event.SkillDevLabel != "" {
		skill = fmt.Sprintf("[internal] %s [SkillId:%s]", event.SkillDevLabel, skillID)
	}
	damageSource := "Unknown"
	if event.HasDamageSourceType {
		switch event.DamageSourceType {
		case 0:
			damageSource = "Bullet"
		case 1:
			damageSource = "Effect"
		default:
			damageSource = fmt.Sprintf("Unknown(%d)", event.DamageSourceType)
		}
	}
	provenance := fmt.Sprintf("DamageId:%s, Source:%s", formatOptionalUint(event.DamageID, event.HasDamageID), damageSource)
	if event.HasBulletID {
		provenance += fmt.Sprintf(", BulletId:%d", event.BulletID)
	}
	targetID := "?"
	if event.HasTargetEntity {
		targetID = fmt.Sprintf("%d", event.TargetEntityID)
	}
	target := fmt.Sprintf("Unknown [Entity:%s]", targetID)
	if event.Target.HasMonster && (event.Target.MonsterStatus == resolutionVerified || event.Target.MonsterStatus == resolutionCorrelated) {
		if event.Target.Monster.Name != "" && (event.Target.Monster.NameStatus == string(resolutionVerified) || event.Target.Monster.NameStatus == string(resolutionCorrelated)) {
			target = fmt.Sprintf("%s [Monster:%d] [Entity:%s]", event.Target.Monster.Name, event.Target.Monster.ID, targetID)
		} else {
			target = fmt.Sprintf("Monster:%d [name %s] [Entity:%s]", event.Target.Monster.ID, event.Target.Monster.NameStatus, targetID)
		}
	} else if event.Target.HasCharacter && (event.Target.CharacterStatus == resolutionVerified || event.Target.CharacterStatus == resolutionCorrelated) {
		if event.Target.Character.Name != "" && (event.Target.Character.NameStatus == string(resolutionVerified) || event.Target.Character.NameStatus == string(resolutionCorrelated)) {
			target = fmt.Sprintf("%s [Char:%d] [Entity:%s]", event.Target.Character.Name, event.Target.Character.ID, targetID)
		} else {
			target = fmt.Sprintf("Character:%d [name %s] [Entity:%s]", event.Target.Character.ID, event.Target.Character.NameStatus, targetID)
		}
	}
	if !event.Source.HasMonster && event.Source.HasCharacter && (event.Source.CharacterStatus == resolutionVerified || event.Source.CharacterStatus == resolutionCorrelated) && event.Source.Character.Name != "" && (event.Source.Character.NameStatus == string(resolutionVerified) || event.Source.Character.NameStatus == string(resolutionCorrelated)) {
		source = fmt.Sprintf("%s [Char:%d] [Entity:%s]", event.Source.Character.Name, event.Source.Character.ID, sourceID)
	}
	if label, ok := sceneTemplateEventLabel(event.Target, targetID); ok && !event.Target.HasMonster && !event.Target.HasCharacter {
		target = label
	}
	if label, ok := sceneTemplateEventLabel(event.Source, sourceID); ok && !event.Source.HasMonster && !event.Source.HasCharacter {
		source = label
	}
	line := fmt.Sprintf("%s  %s  %s  [%s]  %s  %s", timestamp, source, skill, provenance, credit, target)
	if event.SummonerCharacter.HasCharacterID {
		label := fmt.Sprintf("Character:%d", event.SummonerCharacter.CharacterID)
		if event.SummonerCharacter.HasCharacter && event.SummonerCharacter.Character.Name != "" && (event.SummonerCharacter.NameStatus == resolutionVerified || event.SummonerCharacter.NameStatus == resolutionCorrelated) {
			label = event.SummonerCharacter.Character.Name
		}
		line += fmt.Sprintf("  Summoner:%s [%s]", label, event.SummonerCharacter.Status)
	} else if len(event.SummonerCharacter.EntityIDs) > 0 {
		line += fmt.Sprintf("  SummonerEntityChain:%v [UNKNOWN]", event.SummonerCharacter.EntityIDs)
	}
	resultLabel := "Combat Result"
	switch event.EventTypeCandidate {
	case "DAMAGE_CANDIDATE":
		resultLabel = "Damage Candidate Result"
	case "HEALING_CANDIDATE":
		resultLabel = "Healing Candidate Result"
	case "OTHER_COMBAT_RESULT_CANDIDATE":
		resultLabel = "Other Combat Result Candidate"
	}
	if resultLabel == "Combat Result" {
		return fmt.Sprintf("%s  %d", line, event.Damage)
	}
	return fmt.Sprintf("%s  %d [%s]", line, event.Damage, resultLabel)
}

func marshalRuntimeIdentity(entity runtimeEntity) string {
	view := runtimeIdentityAudit{
		EntityID:                 optionalRuntimeID{entity.entityID, entity.entityID != 0},
		SceneInstanceID:          optionalRuntimeID{entity.sceneInstanceID, entity.hasSceneInstanceID},
		EntityType:               optionalRuntimeID{entity.entityTypeCode, entity.hasEntityType},
		ConfigID:                 optionalRuntimeID{entity.configID, entity.hasConfigID},
		ConfigType:               optionalRuntimeID{entity.configType, entity.hasConfigType},
		PrefabID:                 optionalRuntimeID{entity.prefabID, entity.hasPrefabID},
		PlayerID:                 optionalRuntimeID{entity.playerID, entity.hasPlayerID},
		OwnerID:                  optionalRuntimeID{entity.ownerID, entity.hasOwnerID},
		OwnerIncID:               optionalRuntimeID{entity.ownerIncID, entity.hasOwnerIncID},
		SummonerID:               optionalRuntimeID{entity.summonerID, entity.hasSummonerID},
		SummonerPlayerID:         optionalRuntimeID{entity.summonerPlayerID, entity.hasSummonerPlayerID},
		SummonConfigID:           optionalRuntimeID{entity.summonConfigID, entity.hasSummonConfigID},
		SummonSkillID:            optionalRuntimeID{entity.summonSkillID, entity.hasSummonSkillID},
		SummonType:               optionalRuntimeID{entity.summonType, entity.hasSummonType},
		SceneSummonerID:          optionalRuntimeID{entity.sceneSummonerID, entity.hasSceneSummonerID},
		SceneSummonConfigID:      optionalRuntimeID{entity.sceneSummonConfigID, entity.hasSceneSummonConfigID},
		SceneSummonSkillID:       optionalRuntimeID{entity.sceneSummonSkillID, entity.hasSceneSummonSkillID},
		PlayerEntityID:           optionalRuntimeID{entity.playerEntityID, entity.hasPlayerEntityID},
		FollowerSummonConfigID:   optionalRuntimeID{entity.followerSummonConfigID, entity.hasFollowerSummonConfigID},
		PhantomRoleEntityID:      optionalRuntimeID{entity.phantomRoleEntityID, entity.hasPhantomRoleEntityID},
		ConcomitantVisionIDs:     append([]uint64(nil), entity.concomitantVisionIDs...),
		ConcomitantCustomIDs:     append([]uint64(nil), entity.concomitantCustomIDs...),
		WeakVisionEntityID:       optionalRuntimeID{entity.weakVisionEntityID, entity.hasWeakVisionEntityID},
		FollowEntityID:           optionalRuntimeID{entity.followEntityID, entity.hasFollowEntityID},
		MonsterID:                optionalRuntimeID{entity.monsterID, entity.hasMonsterID},
		MonsterCaptureEntityID:   optionalRuntimeID{entity.monsterCaptureEntityID, entity.hasMonsterCaptureEntityID},
		TemplateID:               optionalRuntimeID{entity.templateID, entity.hasTemplateID},
		RoleSkinID:               optionalRuntimeID{entity.roleSkinID, entity.hasRoleSkinID},
		RoleID:                   optionalRuntimeID{entity.roleID, entity.hasRoleID},
		RoleConflict:             entity.roleConflict,
		RoleSourceFieldPath:      entity.roleSourceFieldPath,
		SourceMessageID:          entity.sourceMessageID,
		SourceFieldPath:          entity.sourceFieldPath,
		ComponentSourceMessageID: entity.componentSourceMessageID,
		ComponentSourceFieldPath: entity.componentSourceFieldPath,
	}
	for _, follower := range entity.playerFollowers {
		view.PlayerFollowers = append(view.PlayerFollowers, runtimePlayerFollowerAudit{
			Type:     optionalRuntimeID{follower.typeID, follower.hasType},
			EntityID: optionalRuntimeID{follower.entityID, follower.hasEntity},
		})
	}
	data, err := json.Marshal(view)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func optionalDamageSource(value uint64, present bool) string {
	if !present {
		return "?"
	}
	switch value {
	case 0:
		return "Bullet(0)"
	case 1:
		return "Effect(1)"
	default:
		return fmt.Sprintf("Unknown(%d)", value)
	}
}

// writeNoAmountEvent writes a server-confirmed hit whose response carried no usable Damage field as its own event type.
// It reuses the identity resolution of damage events but is not printed, not counted in encounter totals and not fed to
// the overlap statistics, so it can never add to or double-count damage.
func (p *processor) writeNoAmountEvent(rpcID uint64, request pendingDamageRequest, hasAmount bool, errorCode, immuneType int32, hasChangeLife bool, changeLife int32) {
	event := p.buildDamageEvent(rpcID, request, 0, changeLife, hasChangeLife, 0)
	view := jsonlView(event, p.masters)
	// A no-amount hit is not a damage event: its line shows the outbound skill request it answers, without counting a link.
	if request.skillMessageID != 0 {
		if action, ok := p.dev.combatActionRequests[combatActionRequestKey{sessionNumber: p.sessionNumber, messageID: request.skillMessageID}]; ok {
			view.Skill.Request = skillRequestJSONL(&action)
		}
	}
	view.EventType = "DAMAGE_RESPONSE_WITHOUT_AMOUNT"
	outcome := "NO_AMOUNT_FIELD"
	evidence := "The DamageExecuteResponse omitted Damage (proto3 default 0): a server-confirmed hit whose amount is zero or not reported"
	switch {
	case errorCode != 0:
		outcome = "ERROR_CODE"
		evidence = "The DamageExecuteResponse carried a non-zero ErrorCode; no amount is implied"
	case immuneType != 0:
		outcome = "IMMUNE"
		evidence = "The DamageExecuteResponse carried a non-zero ImmuneType; the hit was reported as immune and no amount is implied"
	}
	response := &combatEventNoAmountJSONL{HasAmount: hasAmount, ErrorCode: errorCode, ImmuneType: immuneType, Outcome: outcome, OutcomeEvidence: evidence}
	if hasChangeLife {
		response.ChangeLifeRaw = &changeLife
	}
	view.Response = response
	view.CombatResult.CombatResultAbs = 0
	p.writeEnvelopedEventJSONL(view, "x")
}
