package capture

import (
	"fmt"
	"time"
)

// combatNotifyPlayerBattleState is CombatNotifyData.PlayerBattleStateChangeNotify in the pinned Windows schema.
const combatNotifyPlayerBattleState = 62

// Battle-state anchors: why a PlayerBattleStateChangeNotify was accepted as the local player's own state.
const (
	battleStateAnchorLocalPlayer  = "local_player_anchor"
	battleStateAnchorSinglePlayer = "single_player_scene"
)

// playerBattleStateSignal is one decoded PlayerBattleStateChangeNotify. InBattle false is the proto3 default, so
// an exit carries only the PlayerId.
type playerBattleStateSignal struct {
	inBattle    bool
	playerID    uint64
	hasPlayerID bool
}

func decodePlayerBattleStateChange(body []byte) (playerBattleStateSignal, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return playerBattleStateSignal{}, err
	}
	var signal playerBattleStateSignal
	for _, field := range fields {
		if field.wire != 0 {
			continue
		}
		switch field.number {
		case 5: // PlayerBattleStateChangeNotify.InBattle
			signal.inBattle = field.value != 0
		case 15: // PlayerBattleStateChangeNotify.PlayerId
			signal.playerID, signal.hasPlayerID = field.value, field.value != 0
		}
	}
	return signal, nil
}

// battleStateAnchor accepts a signal only when its PlayerId is the local player: either the PlayerId anchored by
// the local role report, or, before that anchor exists, the only player listed in the current scene. Signals for
// any other player, or without a PlayerId, are rejected.
func (p *processor) battleStateAnchor(signal playerBattleStateSignal) string {
	if !signal.hasPlayerID || p.playerRelevance == nil {
		return ""
	}
	if p.playerRelevance.isLocalPlayerID(signal.playerID) {
		return battleStateAnchorLocalPlayer
	}
	if len(p.playerRelevance.localPlayerIDs) == 0 && len(p.scenePlayerIDs) == 1 && p.scenePlayerIDs[0] == signal.playerID {
		return battleStateAnchorSinglePlayer
	}
	return ""
}

func (p *processor) handlePlayerBattleState(frame gameFrame, container string, body []byte) error {
	signal, err := decodePlayerBattleStateChange(body)
	if err != nil {
		return fmt.Errorf("parse PlayerBattleStateChangeNotify: %w", err)
	}
	anchor := p.battleStateAnchor(signal)
	p.writeBattleStateSignal(frame.timestamp, container, signal, anchor)
	if p.damageStatistics == nil {
		return nil
	}
	if anchor == "" {
		p.damageStatistics.observeUnanchoredBattleState()
		return nil
	}
	p.observeContextBattleSignal(frame.timestamp)
	p.damageStatistics.observeBattleState(signal.inBattle, frame.timestamp, p.sessionNumber)
	p.flushEncounterTransitions()
	return nil
}

// scenePlayerIDs lists SceneInfo.PlayerInfos[].PlayerId from a JoinSceneNotify.
func scenePlayerIDs(body []byte) ([]uint64, error) {
	fields, err := parseProtoFields(body)
	if err != nil {
		return nil, err
	}
	var ids []uint64
	for _, field := range fields {
		if field.number != 5 || field.wire != 2 { // JoinSceneNotify.SceneInfo
			continue
		}
		sceneFields, err := parseProtoFields(field.data)
		if err != nil {
			return nil, err
		}
		for _, sceneField := range sceneFields {
			if sceneField.number != 4 || sceneField.wire != 2 { // SceneInformation.PlayerInfos
				continue
			}
			playerFields, err := parseProtoFields(sceneField.data)
			if err != nil {
				return nil, err
			}
			for _, playerField := range playerFields {
				if playerField.number == 1 && playerField.wire == 0 && playerField.value != 0 { // ScenePlayerInformation.PlayerId
					ids = append(ids, playerField.value)
				}
			}
		}
	}
	return ids, nil
}

// observeSceneJoin records the scene's players and closes any open encounter at the scene change.
func (p *processor) observeSceneJoin(body []byte, at time.Time) {
	ids, err := scenePlayerIDs(body)
	if err != nil {
		ids = nil
	}
	p.scenePlayerIDs = ids
	if p.damageStatistics != nil {
		p.damageStatistics.observeSceneTransition(at)
		p.flushEncounterTransitions()
	}
}

// observeEncounterClock advances encounter timers on every frame, so an end is decided at the capture time the
// grace or timeout window expired even when no combat event arrives.
func (p *processor) observeEncounterClock(at time.Time) {
	if p.damageStatistics == nil || at.IsZero() {
		return
	}
	p.damageStatistics.observeCaptureTime(at)
	p.flushEncounterTransitions()
}

// finishCapture closes the encounter tracker when a capture or replay ends.
func (p *processor) finishCapture() {
	if p == nil || p.damageStatistics == nil {
		return
	}
	p.damageStatistics.finishCapture()
	p.flushEncounterTransitions()
}

// combatNotifyContainer names the outer message that carried a CombatNotifyData entry.
func combatNotifyContainer(commandID uint16) string {
	switch commandID {
	case 25706:
		return "CombatReceivePackNotify"
	case 20332:
		return "CombatSendPackResponse"
	}
	return fmt.Sprintf("command_%d", commandID)
}
