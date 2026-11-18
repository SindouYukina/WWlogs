package capture

import (
	"fmt"
	"time"
)

// Battle-state anchors: why a PlayerBattleStateChangeNotify was accepted as the local player's own state.
const (
	battleStateAnchorLocalPlayer  = "local_player_anchor"
	battleStateAnchorSinglePlayer = "single_player_scene"
)

// PlayerBattleStateSignal is one decoded PlayerBattleStateChangeNotify. InBattle false is the proto3 default, so
// an exit carries only the PlayerId.
type PlayerBattleStateSignal struct {
	InBattle    bool
	playerID    uint64
	hasPlayerID bool
}

func decodePlayerBattleStateChange(body []byte) (PlayerBattleStateSignal, error) {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return PlayerBattleStateSignal{}, err
	}
	var signal PlayerBattleStateSignal
	for _, field := range fields {
		if field.Wire != 0 {
			continue
		}
		switch field.Number {
		case FieldPlayerBattleStateChangeNotifyInBattle: // PlayerBattleStateChangeNotify.InBattle
			signal.InBattle = field.Value != 0
		case FieldPlayerBattleStateChangeNotifyPlayerID: // PlayerBattleStateChangeNotify.PlayerId
			signal.playerID, signal.hasPlayerID = field.Value, field.Value != 0
		}
	}
	return signal, nil
}

// battleStateAnchor accepts a signal only when its PlayerId is the local player: either the PlayerId anchored by
// BasicInfoNotify or the local role report, or, before that anchor exists, the only player in the current scene. Signals for
// any other player, or without a PlayerId, are rejected.
func (p *Processor) battleStateAnchor(signal PlayerBattleStateSignal) string {
	if !signal.hasPlayerID || p.PlayerRelevance == nil {
		return ""
	}
	if p.PlayerRelevance.IsLocalPlayerID(signal.playerID) {
		return battleStateAnchorLocalPlayer
	}
	if len(p.PlayerRelevance.LocalPlayerIDs) == 0 && len(p.ScenePlayerIDs) == 1 && p.ScenePlayerIDs[0] == signal.playerID {
		return battleStateAnchorSinglePlayer
	}
	return ""
}

func (p *Processor) handlePlayerBattleState(frame GameFrame, container string, body []byte) error {
	signal, err := decodePlayerBattleStateChange(body)
	if err != nil {
		return fmt.Errorf("parse PlayerBattleStateChangeNotify: %w", err)
	}
	anchor := p.battleStateAnchor(signal)
	if p.observer != nil {
		p.observer.WriteBattleStateSignal(frame.Timestamp, container, signal, anchor)
	}
	if p.DamageStatistics == nil {
		return nil
	}
	if anchor == "" {
		p.DamageStatistics.observeUnanchoredBattleState()
		return nil
	}
	p.observeContextBattleSignal(frame.Timestamp)
	p.DamageStatistics.ObserveBattleState(signal.InBattle, frame.Timestamp, p.SessionNumber)
	if p.observer != nil {
		p.observer.FlushEncounterTransitions()
	}
	return nil
}

// scenePlayerIDs lists SceneInfo.PlayerInfos[].PlayerId from a JoinSceneNotify.
func scenePlayerIDs(body []byte) ([]uint64, error) {
	fields, err := ParseProtoFields(body)
	if err != nil {
		return nil, err
	}
	var ids []uint64
	for _, field := range fields {
		if field.Number != FieldJoinSceneNotifySceneInfo || field.Wire != 2 { // JoinSceneNotify.SceneInfo
			continue
		}
		sceneFields, err := ParseProtoFields(field.Data)
		if err != nil {
			return nil, err
		}
		for _, sceneField := range sceneFields {
			if sceneField.Number != FieldSceneInformationPlayerInfos || sceneField.Wire != 2 { // SceneInformation.PlayerInfos
				continue
			}
			playerFields, err := ParseProtoFields(sceneField.Data)
			if err != nil {
				return nil, err
			}
			for _, playerField := range playerFields {
				if playerField.Number == FieldScenePlayerInformationPlayerID && playerField.Wire == 0 && playerField.Value != 0 { // ScenePlayerInformation.PlayerId
					ids = append(ids, playerField.Value)
				}
			}
		}
	}
	return ids, nil
}

// observeSceneJoin records the scene's players and closes any open encounter at the scene change.
func (p *Processor) observeSceneJoin(body []byte, at time.Time) {
	ids, err := scenePlayerIDs(body)
	if err != nil {
		ids = nil
	}
	p.ScenePlayerIDs = ids
	if p.DamageStatistics != nil {
		p.DamageStatistics.observeSceneTransition(at)
		p.DamageStatistics.observeScene(p.Entities.sceneInstanceID, p.Entities.hasSceneInstanceID)
		if p.observer != nil {
			p.observer.FlushEncounterTransitions()
		}
	}
}

// observeEncounterClock advances encounter timers on every frame, so an end is decided at the capture time the
// grace or timeout window expired even when no combat event arrives.
func (p *Processor) observeEncounterClock(at time.Time) {
	if p.DamageStatistics == nil || at.IsZero() {
		return
	}
	p.DamageStatistics.observeCaptureTime(at)
	if p.observer != nil {
		p.observer.FlushEncounterTransitions()
	}
}

// FinishCapture closes the encounter tracker when a capture or replay ends.
func (p *Processor) FinishCapture() {
	if p == nil {
		return
	}
	// No request can arrive after the capture: responses still held stay unpaired.
	p.discardHeldResponses(&p.pairing.captureEnd)
	if p.DamageStatistics == nil {
		return
	}
	p.DamageStatistics.finishCapture()
	if p.observer != nil {
		p.observer.FlushEncounterTransitions()
	}
}

// combatNotifyContainer names the outer message that carried a CombatNotifyData entry.
func combatNotifyContainer(commandID uint16) string {
	switch commandID {
	case CmdCombatReceivePackNotify:
		return "CombatReceivePackNotify"
	case CmdCombatSendPackResponse:
		return "CombatSendPackResponse"
	}
	return fmt.Sprintf("command_%d", commandID)
}
