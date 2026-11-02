package capture

const maxTrackedLocalRoleEntities = 8192

// playerRelation describes what the capture can establish about one runtime
// entity. UNKNOWN is intentionally kept visible: lack of evidence that an
// entity belongs to the local player is not evidence that it is unrelated.
type playerRelation uint8

const (
	playerRelationUnknown playerRelation = iota
	playerRelationLocal
	playerRelationUnrelated
)

// playerRelevanceResolver anchors local role entities to PlayerIds observed in
// verified scene role groups. It does not infer ownership from a character ID,
// damage request, or skill name.
type playerRelevanceResolver struct {
	roleEntities   map[uint64]struct{}
	localPlayerIDs map[uint64]struct{}
	order          []uint64
}

func newPlayerRelevanceResolver() *playerRelevanceResolver {
	return &playerRelevanceResolver{
		roleEntities:   make(map[uint64]struct{}),
		localPlayerIDs: make(map[uint64]struct{}),
	}
}

func (r *playerRelevanceResolver) observeRoleEntity(entityID uint64) {
	if r == nil || entityID == 0 {
		return
	}
	if r.roleEntities == nil {
		r.roleEntities = make(map[uint64]struct{})
	}
	if _, exists := r.roleEntities[entityID]; exists {
		return
	}
	r.roleEntities[entityID] = struct{}{}
	r.order = append(r.order, entityID)
	for len(r.roleEntities) > maxTrackedLocalRoleEntities && len(r.order) > 0 {
		oldest := r.order[0]
		r.order = r.order[1:]
		delete(r.roleEntities, oldest)
	}
}

func (r *playerRelevanceResolver) removeEntity(entityID uint64) {
	if r == nil || entityID == 0 {
		return
	}
	delete(r.roleEntities, entityID)
	for i, trackedID := range r.order {
		if trackedID == entityID {
			r.order = append(r.order[:i], r.order[i+1:]...)
			return
		}
	}
}

func (r *playerRelevanceResolver) reset() {
	if r == nil {
		return
	}
	r.roleEntities = make(map[uint64]struct{})
	r.localPlayerIDs = make(map[uint64]struct{})
	r.order = nil
}

func (r *playerRelevanceResolver) reconcileLocalPlayers(entities *entityResolver) {
	if r == nil || entities == nil {
		return
	}
	if r.localPlayerIDs == nil {
		r.localPlayerIDs = make(map[uint64]struct{})
	}
	for entityID := range r.roleEntities {
		entity, status := entities.resolve(entityID)
		if status == resolutionUnknown || entity.entityID != entityID || !hasPlayerSceneRoleMapping(entity) {
			continue
		}
		r.localPlayerIDs[entity.playerID] = struct{}{}
	}
}

func (r *playerRelevanceResolver) isLocalPlayerRoleEntity(entity runtimeEntity) bool {
	if r == nil || !hasPlayerSceneRoleMapping(entity) {
		return false
	}
	_, exists := r.localPlayerIDs[entity.playerID]
	return exists
}

func (r *playerRelevanceResolver) isLocalPlayerID(playerID uint64) bool {
	if r == nil || playerID == 0 {
		return false
	}
	_, exists := r.localPlayerIDs[playerID]
	return exists
}

func (p *processor) observeLocalRoleEntity(entityID uint64) {
	if p == nil || entityID == 0 {
		return
	}
	if p.playerRelevance == nil {
		p.playerRelevance = newPlayerRelevanceResolver()
	}
	p.playerRelevance.observeRoleEntity(entityID)
	p.playerRelevance.reconcileLocalPlayers(p.entities)
	p.refreshEffectRelations()
}

func (r *playerRelevanceResolver) isRoleEntity(entityID uint64) bool {
	if r == nil || entityID == 0 {
		return false
	}
	_, exists := r.roleEntities[entityID]
	return exists
}

func (p *processor) isLocallyOwnedCombatEntity(entityID uint64, identity entityIdentity) bool {
	return p.playerRelationForEntity(entityID, identity) == playerRelationLocal
}

// playerRelationForEntity marks an entity unrelated only when its observed
// runtime ownership chain is closed and ends at a known monster or template
// entity. Player-shaped entities, unresolved owners, and unclassified runtime
// entities remain UNKNOWN so that potentially relevant events are not lost.
func (p *processor) playerRelationForEntity(entityID uint64, identity entityIdentity) playerRelation {
	if p == nil || p.playerRelevance == nil || entityID == 0 {
		return playerRelationUnknown
	}
	if p.playerRelevance.isRoleEntity(entityID) {
		return playerRelationLocal
	}
	if identity.Runtime.entityID != entityID {
		return playerRelationUnknown
	}
	if p.playerRelevance.isLocalPlayerRoleEntity(identity.Runtime) {
		return playerRelationLocal
	}
	if identity.RuntimeStatus != resolutionVerified || p.entities == nil {
		return playerRelationUnknown
	}
	current := identity.Runtime
	seen := make(map[uint64]struct{}, maxSummonerEntityDepth)
	ambiguousOwnerLink := false
	chainClosed := false
	for depth := 0; depth < maxSummonerEntityDepth; depth++ {
		if p.playerRelevance.isRoleEntity(current.entityID) {
			return playerRelationLocal
		}
		if p.playerRelevance.isLocalPlayerRoleEntity(current) {
			return playerRelationLocal
		}
		if hasUnresolvedPlayerOrOwnerLink(current) {
			ambiguousOwnerLink = true
		}
		if !current.hasSummonerID || current.summonerID == 0 {
			chainClosed = true
			break
		}
		parentID := current.summonerID
		if p.playerRelevance.isRoleEntity(parentID) {
			return playerRelationLocal
		}
		if _, duplicate := seen[parentID]; duplicate {
			return playerRelationUnknown
		}
		seen[parentID] = struct{}{}
		parent, status := p.entities.resolve(parentID)
		if status != resolutionVerified || parent.entityID != parentID {
			return playerRelationUnknown
		}
		if p.playerRelevance.isLocalPlayerRoleEntity(parent) {
			return playerRelationLocal
		}
		current = parent
	}

	if !chainClosed || ambiguousOwnerLink || isPlayerShapedEntity(current) {
		return playerRelationUnknown
	}
	environmentIdentity := entityIdentity{}
	if current.entityID == entityID {
		environmentIdentity = identity
	}
	if isKnownEnvironmentEntity(current, environmentIdentity) {
		return playerRelationUnrelated
	}
	return playerRelationUnknown
}

func hasUnresolvedPlayerOrOwnerLink(entity runtimeEntity) bool {
	if isExplicitMonsterEntity(entity) {
		return (entity.hasOwnerID && entity.ownerID != 0) ||
			(entity.hasSummonerPlayerID && entity.summonerPlayerID != 0) ||
			(entity.hasSceneSummonerID && entity.sceneSummonerID != 0) ||
			(entity.hasSceneSummonConfigID && entity.sceneSummonConfigID != 0) ||
			(entity.hasSceneSummonSkillID && entity.sceneSummonSkillID != 0) ||
			(entity.hasPlayerEntityID && entity.playerEntityID != 0) ||
			(entity.hasPhantomRoleEntityID && entity.phantomRoleEntityID != 0) ||
			(entity.hasWeakVisionEntityID && entity.weakVisionEntityID != 0) ||
			(entity.hasSummonConfigID && entity.summonConfigID != 0) ||
			(entity.hasSummonSkillID && entity.summonSkillID != 0) ||
			(entity.hasSummonType && entity.summonType != 0) ||
			(entity.hasFollowerSummonConfigID && entity.followerSummonConfigID != 0) ||
			(entity.hasFollowEntityID && entity.followEntityID != 0) ||
			len(entity.concomitantVisionIDs) > 0 || len(entity.concomitantCustomIDs) > 0 || len(entity.playerFollowers) > 0
	}
	return (entity.hasPlayerID && entity.playerID != 0) ||
		(entity.hasOwnerID && entity.ownerID != 0) ||
		(entity.hasSummonerPlayerID && entity.summonerPlayerID != 0) ||
		(entity.hasSceneSummonerID && entity.sceneSummonerID != 0) ||
		(entity.hasSceneSummonConfigID && entity.sceneSummonConfigID != 0) ||
		(entity.hasSceneSummonSkillID && entity.sceneSummonSkillID != 0) ||
		(entity.hasPlayerEntityID && entity.playerEntityID != 0) ||
		(entity.hasPhantomRoleEntityID && entity.phantomRoleEntityID != 0) ||
		(entity.hasWeakVisionEntityID && entity.weakVisionEntityID != 0) ||
		(entity.hasOwnerIncID && entity.ownerIncID != 0) ||
		(entity.hasSummonConfigID && entity.summonConfigID != 0) ||
		(entity.hasSummonSkillID && entity.summonSkillID != 0) ||
		(entity.hasSummonType && entity.summonType != 0) ||
		(entity.hasFollowerSummonConfigID && entity.followerSummonConfigID != 0) ||
		(entity.hasFollowEntityID && entity.followEntityID != 0) ||
		len(entity.concomitantVisionIDs) > 0 || len(entity.concomitantCustomIDs) > 0 ||
		len(entity.playerFollowers) > 0
}

func isExplicitMonsterEntity(entity runtimeEntity) bool {
	return entity.hasEntityType && entity.entityTypeCode == entityTypeMonster && !entity.hasRoleID && !entity.roleConflict
}

func isPlayerShapedEntity(entity runtimeEntity) bool {
	if isExplicitMonsterEntity(entity) {
		return false
	}
	return (entity.hasRoleID && entity.roleID != 0) ||
		(entity.hasPlayerID && entity.playerID != 0) ||
		(entity.hasPlayerEntityID && entity.playerEntityID != 0) ||
		(entity.hasSummonerPlayerID && entity.summonerPlayerID != 0) ||
		len(entity.playerFollowers) > 0
}

func isKnownEnvironmentEntity(entity runtimeEntity, identity entityIdentity) bool {
	if isExplicitMonsterEntity(entity) {
		return true
	}
	if isPlayerShapedEntity(entity) {
		return false
	}
	return (entity.hasMonsterID && entity.monsterID != 0) ||
		(entity.hasTemplateID && entity.templateID != 0) ||
		identity.HasMonster || len(identity.TemplateCandidates) > 0
}

// playerRelevantDamageEvent filters only events proven unrelated to the local
// player. A local endpoint or any unresolved relation keeps the event visible.
func (p *processor) playerRelevantDamageEvent(event combatEvent) (playerRelation, playerRelation, bool) {
	source := p.playerRelationForEntity(event.SourceEntityID, event.Source)
	if source != playerRelationLocal && p.isLocallyOwnedVehicleDamage(event) {
		source = playerRelationLocal
	}
	target := p.playerRelationForEntity(event.TargetEntityID, event.Target)
	return source, target, source != playerRelationUnrelated || target != playerRelationUnrelated
}

func (p *processor) isLocallyOwnedVehicleDamage(event combatEvent) bool {
	if p == nil || p.playerRelevance == nil || event.EventTypeCandidate != "DAMAGE_CANDIDATE" ||
		event.DamageContributorCandidate == nil || event.DamageContributorCandidate.ActorType != "Vehicle" ||
		(event.DamageContributorCandidate.Status != resolutionCorrelated && event.DamageContributorCandidate.Status != resolutionVerified) ||
		!event.HasSourceEntity || event.Source.RuntimeStatus != resolutionVerified ||
		event.Source.Runtime.entityID != event.SourceEntityID || !event.Source.Runtime.hasPlayerID ||
		isExplicitMonsterEntity(event.Source.Runtime) {
		return false
	}
	return p.playerRelevance.isLocalPlayerID(event.Source.Runtime.playerID)
}
