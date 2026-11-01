package capture

import (
	"sort"
	"sync"
	"time"
)

const maxCombatStatisticsEntities = 8192

type combatStatisticsKey struct {
	sessionNumber    int
	entityID         uint64
	entityGeneration uint64
}

// PlayerEntityDamageSnapshot は、セッション内の EntityId ごとに確認できた傷害合計を保持します。
type PlayerEntityDamageSnapshot struct {
	SessionNumber    int    `json:"sessionNumber"`
	EntityID         uint64 `json:"entityId"`
	EntityGeneration uint64 `json:"entityGeneration"`
	DamageDealt      int64  `json:"damageDealt"`
	DamageTaken      int64  `json:"damageTaken"`
	OutgoingEvents   uint64 `json:"outgoingEvents"`
	IncomingEvents   uint64 `json:"incomingEvents"`
	// 捕獲フレームの観測時刻であり、ゲーム内イベントの権威時刻ではありません。
	FirstObserved time.Time `json:"firstObserved,omitempty"`
	LastObserved  time.Time `json:"lastObserved,omitempty"`
}

// DamageStatisticsSnapshot は、確認済み合計と帰属未確定の候補を分けて返します。
type DamageStatisticsSnapshot struct {
	DamageDealt                int64                        `json:"damageDealt"`
	DamageTaken                int64                        `json:"damageTaken"`
	OutgoingEvents             uint64                       `json:"outgoingEvents"`
	IncomingEvents             uint64                       `json:"incomingEvents"`
	UnattributedOutgoingDamage int64                        `json:"unattributedOutgoingDamage"`
	UnattributedIncomingDamage int64                        `json:"unattributedIncomingDamage"`
	UnattributedOutgoingEvents uint64                       `json:"unattributedOutgoingEvents"`
	UnattributedIncomingEvents uint64                       `json:"unattributedIncomingEvents"`
	UngroupedDamageDealt       int64                        `json:"ungroupedDamageDealt"`
	UngroupedDamageTaken       int64                        `json:"ungroupedDamageTaken"`
	UngroupedOutgoingEvents    uint64                       `json:"ungroupedOutgoingEvents"`
	UngroupedIncomingEvents    uint64                       `json:"ungroupedIncomingEvents"`
	ByPlayerEntity             []PlayerEntityDamageSnapshot `json:"byPlayerEntity"`
}

// DamageStatistics は、サーバー応答で確認したプレイヤー関連傷害を集計します。
type DamageStatistics struct {
	mu sync.RWMutex

	entities                   map[combatStatisticsKey]PlayerEntityDamageSnapshot
	damageDealt                int64
	damageTaken                int64
	outgoingEvents             uint64
	incomingEvents             uint64
	unattributedOutgoingDamage int64
	unattributedIncomingDamage int64
	unattributedOutgoingEvents uint64
	unattributedIncomingEvents uint64
	ungroupedDamageDealt       int64
	ungroupedDamageTaken       int64
	ungroupedOutgoingEvents    uint64
	ungroupedIncomingEvents    uint64
}

// NewDamageStatistics は、新しい空の統計器を作成します。
func NewDamageStatistics() *DamageStatistics {
	return &DamageStatistics{entities: make(map[combatStatisticsKey]PlayerEntityDamageSnapshot)}
}

// Snapshot は、内部状態を変更できない独立したスナップショットを返します。
func (s *DamageStatistics) Snapshot() DamageStatisticsSnapshot {
	if s == nil {
		return DamageStatisticsSnapshot{ByPlayerEntity: []PlayerEntityDamageSnapshot{}}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := DamageStatisticsSnapshot{
		DamageDealt: s.damageDealt, DamageTaken: s.damageTaken,
		OutgoingEvents: s.outgoingEvents, IncomingEvents: s.incomingEvents,
		UnattributedOutgoingDamage: s.unattributedOutgoingDamage, UnattributedIncomingDamage: s.unattributedIncomingDamage,
		UnattributedOutgoingEvents: s.unattributedOutgoingEvents, UnattributedIncomingEvents: s.unattributedIncomingEvents,
		UngroupedDamageDealt: s.ungroupedDamageDealt, UngroupedDamageTaken: s.ungroupedDamageTaken,
		UngroupedOutgoingEvents: s.ungroupedOutgoingEvents, UngroupedIncomingEvents: s.ungroupedIncomingEvents,
		ByPlayerEntity: make([]PlayerEntityDamageSnapshot, 0, len(s.entities)),
	}
	for _, entity := range s.entities {
		snapshot.ByPlayerEntity = append(snapshot.ByPlayerEntity, entity)
	}
	sort.Slice(snapshot.ByPlayerEntity, func(i, j int) bool {
		if snapshot.ByPlayerEntity[i].SessionNumber != snapshot.ByPlayerEntity[j].SessionNumber {
			return snapshot.ByPlayerEntity[i].SessionNumber < snapshot.ByPlayerEntity[j].SessionNumber
		}
		if snapshot.ByPlayerEntity[i].EntityID != snapshot.ByPlayerEntity[j].EntityID {
			return snapshot.ByPlayerEntity[i].EntityID < snapshot.ByPlayerEntity[j].EntityID
		}
		return snapshot.ByPlayerEntity[i].EntityGeneration < snapshot.ByPlayerEntity[j].EntityGeneration
	})
	return snapshot
}

// Reset は、集計値と EntityId 別の状態をすべて消去します。
func (s *DamageStatistics) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entities = make(map[combatStatisticsKey]PlayerEntityDamageSnapshot)
	s.damageDealt = 0
	s.damageTaken = 0
	s.outgoingEvents = 0
	s.incomingEvents = 0
	s.unattributedOutgoingDamage = 0
	s.unattributedIncomingDamage = 0
	s.unattributedOutgoingEvents = 0
	s.unattributedIncomingEvents = 0
	s.ungroupedDamageDealt = 0
	s.ungroupedDamageTaken = 0
	s.ungroupedOutgoingEvents = 0
	s.ungroupedIncomingEvents = 0
}

// addDamageEvent は、応答検証と RPC 重複排除を通過したイベントだけを受け取ります。
func (s *DamageStatistics) addDamageEvent(event combatEvent, sourceRelation, targetRelation playerRelation) {
	if s == nil || event.EventTypeCandidate != "DAMAGE_CANDIDATE" || event.Damage <= 0 ||
		!event.HasSourceEntity || event.SourceEntityID == 0 || !event.HasTargetEntity || event.TargetEntityID == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entities == nil {
		s.entities = make(map[combatStatisticsKey]PlayerEntityDamageSnapshot)
	}
	switch {
	case sourceRelation == playerRelationLocal && targetRelation == playerRelationUnrelated:
		s.damageDealt += event.Damage
		s.outgoingEvents++
		s.addEntityDamage(event.SessionNumber, event.SourceEntityID, event.Source.Lifecycle.ReusedAfterRemove, event.Damage, true, event.Timestamp)
	case sourceRelation == playerRelationUnrelated && targetRelation == playerRelationLocal:
		s.damageTaken += event.Damage
		s.incomingEvents++
		s.addEntityDamage(event.SessionNumber, event.TargetEntityID, event.Target.Lifecycle.ReusedAfterRemove, event.Damage, false, event.Timestamp)
	case sourceRelation == playerRelationLocal && targetRelation == playerRelationUnknown:
		s.unattributedOutgoingDamage += event.Damage
		s.unattributedOutgoingEvents++
	case sourceRelation == playerRelationUnknown && targetRelation == playerRelationLocal:
		s.unattributedIncomingDamage += event.Damage
		s.unattributedIncomingEvents++
	case sourceRelation != playerRelationUnrelated && targetRelation == playerRelationUnrelated:
		s.unattributedOutgoingDamage += event.Damage
		s.unattributedOutgoingEvents++
	case sourceRelation == playerRelationUnrelated && targetRelation != playerRelationUnrelated:
		s.unattributedIncomingDamage += event.Damage
		s.unattributedIncomingEvents++
	}
}

func (s *DamageStatistics) addEntityDamage(sessionNumber int, entityID, entityGeneration uint64, amount int64, outgoing bool, observedAt time.Time) {
	key := combatStatisticsKey{sessionNumber: sessionNumber, entityID: entityID, entityGeneration: entityGeneration}
	entity, exists := s.entities[key]
	if !exists && len(s.entities) >= maxCombatStatisticsEntities {
		if outgoing {
			s.ungroupedDamageDealt += amount
			s.ungroupedOutgoingEvents++
		} else {
			s.ungroupedDamageTaken += amount
			s.ungroupedIncomingEvents++
		}
		return
	}
	if !exists {
		entity = PlayerEntityDamageSnapshot{SessionNumber: sessionNumber, EntityID: entityID, EntityGeneration: entityGeneration}
	}
	if outgoing {
		entity.DamageDealt += amount
		entity.OutgoingEvents++
	} else {
		entity.DamageTaken += amount
		entity.IncomingEvents++
	}
	if !observedAt.IsZero() {
		if entity.FirstObserved.IsZero() || observedAt.Before(entity.FirstObserved) {
			entity.FirstObserved = observedAt
		}
		if entity.LastObserved.IsZero() || observedAt.After(entity.LastObserved) {
			entity.LastObserved = observedAt
		}
	}
	s.entities[key] = entity
}
