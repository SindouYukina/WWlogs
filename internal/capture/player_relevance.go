package capture

import "fmt"

// observeLocalPlayerInfo consumes the receiving player's explicit BasicInfoNotify.Id.
// A scene owner or an arbitrary EntityPb.PlayerId is not a substitute for this identity.
func (p *Processor) observeLocalPlayerInfo(body []byte) error {
	if p.SessionNumber == 0 {
		return nil
	}
	fields, err := ParseProtoFields(body)
	if err != nil {
		return fmt.Errorf("parse BasicInfoNotify: %w", err)
	}
	var playerID uint64
	seen := false
	for _, field := range fields {
		if field.Number != FieldBasicInfoNotifyID { // BasicInfoNotify.Id, int32
			continue
		}
		if field.Wire != 0 || field.Value > 0x7fffffff || (seen && playerID != field.Value) {
			return fmt.Errorf("invalid local player identity in BasicInfoNotify")
		}
		playerID, seen = field.Value, true
	}
	if !seen || playerID == 0 {
		return nil
	}
	if p.PlayerRelevance == nil {
		p.PlayerRelevance = newPlayerRelevanceResolver()
	}
	if len(p.PlayerRelevance.LocalPlayerIDs) != 0 && !p.PlayerRelevance.IsLocalPlayerID(playerID) {
		return fmt.Errorf("conflicting local player identity in BasicInfoNotify")
	}
	if p.PlayerRelevance.LocalPlayerIDs == nil {
		p.PlayerRelevance.LocalPlayerIDs = make(map[uint64]struct{})
	}
	p.PlayerRelevance.LocalPlayerIDs[playerID] = struct{}{}
	p.refreshEffectRelations()
	// Identity evidence alone does not repair scene/entity context invalidated by transport loss.
	return nil
}

const maxTrackedLocalRoleEntities = 8192

// PlayerRelation describes what the capture can establish about one runtime
// entity. UNKNOWN is intentionally kept visible: lack of evidence that an
// entity belongs to the local player is not evidence that it is unrelated.
type PlayerRelation uint8

const (
	PlayerRelationUnknown PlayerRelation = iota
	PlayerRelationLocal
	PlayerRelationUnrelated
)

// playerRelevanceResolver anchors the receiving player through BasicInfoNotify,
// or through a local role report joined to a verified scene role group. It does
// not infer ownership from a character ID, damage request, or skill name.
type playerRelevanceResolver struct {
	RoleEntities   map[uint64]struct{}
	LocalPlayerIDs map[uint64]struct{}
	order          []uint64
}

func newPlayerRelevanceResolver() *playerRelevanceResolver {
	return &playerRelevanceResolver{
		RoleEntities:   make(map[uint64]struct{}),
		LocalPlayerIDs: make(map[uint64]struct{}),
	}
}

func (r *playerRelevanceResolver) ObserveRoleEntity(entityID uint64) {
	if r == nil || entityID == 0 {
		return
	}
	if r.RoleEntities == nil {
		r.RoleEntities = make(map[uint64]struct{})
	}
	if _, exists := r.RoleEntities[entityID]; exists {
		return
	}
	r.RoleEntities[entityID] = struct{}{}
	r.order = append(r.order, entityID)
	for len(r.RoleEntities) > maxTrackedLocalRoleEntities && len(r.order) > 0 {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.RoleEntities, oldest)
	}
}

func (r *playerRelevanceResolver) RemoveEntity(entityID uint64) {
	if r == nil || entityID == 0 {
		return
	}
	delete(r.RoleEntities, entityID)
	for i, trackedID := range r.order {
		if trackedID == entityID {
			r.order = append(r.order[:i], r.order[i+1:]...)
			return
		}
	}
}

func (r *playerRelevanceResolver) Reset() {
	if r == nil {
		return
	}
	r.RoleEntities = make(map[uint64]struct{})
	r.LocalPlayerIDs = make(map[uint64]struct{})
	r.order = nil
}

// ResetSceneEntities drops scene-scoped entity IDs while retaining the stable
// local PlayerId anchor for the current capture session.
func (r *playerRelevanceResolver) ResetSceneEntities() {
	if r == nil {
		return
	}
	r.RoleEntities = make(map[uint64]struct{})
	r.order = nil
}

func (r *playerRelevanceResolver) ReconcileLocalPlayers(entities *EntityResolver) {
	if r == nil || entities == nil {
		return
	}
	if r.LocalPlayerIDs == nil {
		r.LocalPlayerIDs = make(map[uint64]struct{})
	}
	for entityID := range r.RoleEntities {
		entity, status := entities.Resolve(entityID)
		if status == ResolutionUnknown || entity.EntityID != entityID || !HasPlayerSceneRoleMapping(entity) {
			continue
		}
		r.LocalPlayerIDs[entity.PlayerID] = struct{}{}
	}
}

func (r *playerRelevanceResolver) isLocalPlayerRoleEntity(entity RuntimeEntity) bool {
	if r == nil || !HasPlayerSceneRoleMapping(entity) {
		return false
	}
	_, exists := r.LocalPlayerIDs[entity.PlayerID]
	return exists
}

// isLocalPlayerEntity reports the local player's Proto_PlayerEntity. It holds team-level effects, so it is a local effect
// holder without being a role, and it says nothing about damage: only the effect relation uses it.
func (r *playerRelevanceResolver) isLocalPlayerEntity(entity RuntimeEntity) bool {
	return entity.HasEntityType && entity.EntityTypeCode == EntityTypePlayerEntity && entity.HasPlayerID && r.IsLocalPlayerID(entity.PlayerID)
}

func (r *playerRelevanceResolver) IsLocalPlayerID(playerID uint64) bool {
	if r == nil || playerID == 0 {
		return false
	}
	_, exists := r.LocalPlayerIDs[playerID]
	return exists
}

func (p *Processor) ObserveLocalRoleEntity(entityID uint64) {
	if p == nil || entityID == 0 {
		return
	}
	if p.PlayerRelevance == nil {
		p.PlayerRelevance = newPlayerRelevanceResolver()
	}
	p.PlayerRelevance.ObserveRoleEntity(entityID)
	p.PlayerRelevance.ReconcileLocalPlayers(p.Entities)
	p.observeContextLocalRole(entityID, p.CurrentFrameTime)
	p.refreshEffectRelations()
}

func (r *playerRelevanceResolver) IsRoleEntity(entityID uint64) bool {
	if r == nil || entityID == 0 {
		return false
	}
	_, exists := r.RoleEntities[entityID]
	return exists
}

func (p *Processor) isLocallyOwnedCombatEntity(entityID uint64, identity EntityIdentity) bool {
	return p.playerRelationForEntity(entityID, identity) == PlayerRelationLocal
}

// playerRelationForEntity marks an entity unrelated only when its observed
// runtime ownership chain is closed and ends at a known monster or template
// entity. Player-shaped entities, unresolved owners, and unclassified runtime
// entities remain UNKNOWN so that potentially relevant events are not lost.
func (p *Processor) playerRelationForEntity(entityID uint64, identity EntityIdentity) PlayerRelation {
	if p == nil || p.PlayerRelevance == nil || entityID == 0 {
		return PlayerRelationUnknown
	}
	if p.PlayerRelevance.IsRoleEntity(entityID) {
		return PlayerRelationLocal
	}
	// The scene's own combat entity (SceneInformation.SceneEntityId) belongs to no player. Its hit on the local team is
	// damage taken, like any other non-player source; what it executes on enemies is decided per result (damage_notify.go).
	if entityID == p.sceneCombatEntity() {
		return PlayerRelationUnrelated
	}
	if identity.Runtime.EntityID != entityID {
		return PlayerRelationUnknown
	}
	if p.PlayerRelevance.isLocalPlayerRoleEntity(identity.Runtime) {
		return PlayerRelationLocal
	}
	if identity.RuntimeStatus != ResolutionVerified || p.Entities == nil {
		return PlayerRelationUnknown
	}
	current := identity.Runtime
	seen := make(map[uint64]struct{}, maxSummonerEntityDepth)
	ambiguousOwnerLink := false
	chainClosed := false
	for depth := 0; depth < maxSummonerEntityDepth; depth++ {
		if p.PlayerRelevance.IsRoleEntity(current.EntityID) {
			return PlayerRelationLocal
		}
		if p.PlayerRelevance.isLocalPlayerRoleEntity(current) {
			return PlayerRelationLocal
		}
		if hasUnresolvedPlayerOrOwnerLink(current) {
			ambiguousOwnerLink = true
		}
		if !current.HasSummonerID || current.SummonerID == 0 {
			chainClosed = true
			break
		}
		parentID := current.SummonerID
		if p.PlayerRelevance.IsRoleEntity(parentID) {
			return PlayerRelationLocal
		}
		if _, duplicate := seen[parentID]; duplicate {
			return PlayerRelationUnknown
		}
		seen[parentID] = struct{}{}
		parent, status := p.Entities.Resolve(parentID)
		if status != ResolutionVerified || parent.EntityID != parentID {
			return PlayerRelationUnknown
		}
		if p.PlayerRelevance.isLocalPlayerRoleEntity(parent) {
			return PlayerRelationLocal
		}
		current = parent
	}

	if !chainClosed || ambiguousOwnerLink || isPlayerShapedEntity(current) {
		return PlayerRelationUnknown
	}
	environmentIdentity := EntityIdentity{}
	if current.EntityID == entityID {
		environmentIdentity = identity
	}
	if isKnownEnvironmentEntity(current, environmentIdentity) {
		return PlayerRelationUnrelated
	}
	return PlayerRelationUnknown
}

func hasUnresolvedPlayerOrOwnerLink(entity RuntimeEntity) bool {
	if IsExplicitMonsterEntity(entity) {
		return (entity.HasOwnerID && entity.OwnerID != 0) ||
			(entity.HasSummonerPlayerID && entity.SummonerPlayerID != 0) ||
			(entity.HasSceneSummonerID && entity.SceneSummonerID != 0) ||
			(entity.HasSceneSummonConfigID && entity.SceneSummonConfigID != 0) ||
			(entity.HasSceneSummonSkillID && entity.SceneSummonSkillID != 0) ||
			(entity.HasPlayerEntityID && entity.PlayerEntityID != 0) ||
			(entity.HasPhantomRoleEntityID && entity.PhantomRoleEntityID != 0) ||
			(entity.HasWeakVisionEntityID && entity.WeakVisionEntityID != 0) ||
			monsterSummonHasNoLink(entity) ||
			(entity.HasFollowerSummonConfigID && entity.FollowerSummonConfigID != 0) ||
			(entity.HasFollowEntityID && entity.FollowEntityID != 0) ||
			len(entity.ConcomitantVisionIDs) > 0 || len(entity.ConcomitantCustomIDs) > 0 || len(entity.PlayerFollowers) > 0
	}
	return (entity.HasPlayerID && entity.PlayerID != 0) ||
		(entity.HasOwnerID && entity.OwnerID != 0) ||
		(entity.HasSummonerPlayerID && entity.SummonerPlayerID != 0) ||
		(entity.HasSceneSummonerID && entity.SceneSummonerID != 0) ||
		(entity.HasSceneSummonConfigID && entity.SceneSummonConfigID != 0) ||
		(entity.HasSceneSummonSkillID && entity.SceneSummonSkillID != 0) ||
		(entity.HasPlayerEntityID && entity.PlayerEntityID != 0) ||
		(entity.HasPhantomRoleEntityID && entity.PhantomRoleEntityID != 0) ||
		(entity.HasWeakVisionEntityID && entity.WeakVisionEntityID != 0) ||
		(entity.HasOwnerIncID && entity.OwnerIncID != 0) ||
		(entity.HasSummonConfigID && entity.SummonConfigID != 0) ||
		(entity.HasSummonSkillID && entity.SummonSkillID != 0) ||
		(entity.HasSummonType && entity.SummonType != 0) ||
		(entity.HasFollowerSummonConfigID && entity.FollowerSummonConfigID != 0) ||
		(entity.HasFollowEntityID && entity.FollowEntityID != 0) ||
		len(entity.ConcomitantVisionIDs) > 0 || len(entity.ConcomitantCustomIDs) > 0 ||
		len(entity.PlayerFollowers) > 0
}

// monsterSummonHasNoLink reports a Monster that carries summon details but no SummonerId to follow, so its owner is unknown.
// With a SummonerId those details describe that one link, which playerRelationForEntity walks to a verified parent: a chain
// ending at a role is local, and one ending at a Monster with no player link is the enemy's own summon.
func monsterSummonHasNoLink(entity RuntimeEntity) bool {
	if entity.HasSummonerID && entity.SummonerID != 0 {
		return false
	}
	return (entity.HasSummonConfigID && entity.SummonConfigID != 0) ||
		(entity.HasSummonSkillID && entity.SummonSkillID != 0) ||
		(entity.HasSummonType && entity.SummonType != 0)
}

func IsExplicitMonsterEntity(entity RuntimeEntity) bool {
	return entity.HasEntityType && entity.EntityTypeCode == EntityTypeMonster && !entity.HasRoleID && !entity.RoleConflict
}

func isPlayerShapedEntity(entity RuntimeEntity) bool {
	if IsExplicitMonsterEntity(entity) {
		return false
	}
	return (entity.HasRoleID && entity.RoleID != 0) ||
		(entity.HasPlayerID && entity.PlayerID != 0) ||
		(entity.HasPlayerEntityID && entity.PlayerEntityID != 0) ||
		(entity.HasSummonerPlayerID && entity.SummonerPlayerID != 0) ||
		len(entity.PlayerFollowers) > 0
}

func isKnownEnvironmentEntity(entity RuntimeEntity, identity EntityIdentity) bool {
	if IsExplicitMonsterEntity(entity) {
		return true
	}
	if isPlayerShapedEntity(entity) {
		return false
	}
	return (entity.HasMonsterID && entity.MonsterID != 0) ||
		(entity.HasTemplateID && entity.TemplateID != 0) ||
		identity.HasMonster || len(identity.TemplateCandidates) > 0
}

// PlayerRelevantDamageEvent filters only events proven unrelated to the local
// player. A local endpoint or any unresolved relation keeps the event visible.
func (p *Processor) PlayerRelevantDamageEvent(event CombatEvent) (PlayerRelation, PlayerRelation, bool) {
	source := p.playerRelationForEntity(event.SourceEntityID, event.Source)
	if source != PlayerRelationLocal && p.isLocallyOwnedVehicleDamage(event) {
		source = PlayerRelationLocal
	}
	// The scene combat entity executes negative statuses for whoever applied them. On an enemy the damage is the local
	// team's only when negativeStatusOwnership established it; otherwise its owner is unknown, not unrelated.
	if event.Notify != nil && event.Notify.NegativeStatus && event.SourceEntityID == p.sceneCombatEntity() {
		target := p.playerRelationForEntity(event.TargetEntityID, event.Target)
		if target == PlayerRelationUnrelated {
			source = PlayerRelationUnknown
			if event.Notify.NegativeStatusLocal {
				source = PlayerRelationLocal
			}
		}
		return source, target, source != PlayerRelationUnrelated || target != PlayerRelationUnrelated
	}
	target := p.playerRelationForEntity(event.TargetEntityID, event.Target)
	return source, target, source != PlayerRelationUnrelated || target != PlayerRelationUnrelated
}

func (p *Processor) isLocallyOwnedVehicleDamage(event CombatEvent) bool {
	if p == nil || p.PlayerRelevance == nil || event.EventTypeCandidate != "DAMAGE_CANDIDATE" ||
		event.DamageContributorCandidate == nil || event.DamageContributorCandidate.ActorType != "Vehicle" ||
		(event.DamageContributorCandidate.Status != ResolutionCorrelated && event.DamageContributorCandidate.Status != ResolutionVerified) ||
		!event.HasSourceEntity || event.Source.RuntimeStatus != ResolutionVerified ||
		event.Source.Runtime.EntityID != event.SourceEntityID || !event.Source.Runtime.HasPlayerID ||
		IsExplicitMonsterEntity(event.Source.Runtime) {
		return false
	}
	return p.PlayerRelevance.IsLocalPlayerID(event.Source.Runtime.PlayerID)
}
