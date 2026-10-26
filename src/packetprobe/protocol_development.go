//go:build wwdev

package main

import (
	"fmt"
	"io"
)

func (p *processor) printSummary(w io.Writer) {
	fmt.Fprintf(w, "damage_rpc_summary requests=%d candidate_responses=%d matched=%d unmatched_requests=%d unmatched_responses=%d invalid_responses=%d duplicate_responses=%d evicted_pending=%d missing_attacker_entity_id=%d matched_combat_result_abs=%d damage_candidate_events=%d damage_candidate_result_abs=%d healing_candidate_events=%d healing_candidate_result_abs=%d other_combat_candidate_events=%d other_combat_candidate_result_abs=%d unclassified_events=%d unclassified_result_abs=%d entity_adds=%d entity_removes=%d entity_active_requests=%d entity_active_responses=%d active_response_identity_components=%d active_response_summoner_components=%d active_response_monster_components=%d active_response_follower_components=%d runtime_entities=%d observed_source_events=%d observed_target_events=%d verified_character_events=%d correlated_character_events=%d resolved_monster_events=%d resolved_skill_events=%d unknown_events=%d observed_buff_events=%d verified_buff_events=%d parse_errors=%d decrypt_errors=%d compressed_errors=%d handshake_frames=%d handshake_ok=%d handshake_errors=%d\n",
		p.stats.damageRequests, p.stats.candidateResponses, p.stats.printed, len(p.pending), p.stats.unpairedResponses, p.stats.invalidResponses, p.stats.duplicateResponses, p.stats.evictedPending, p.stats.missingAttackerEntityID, p.stats.matchedCombatResultAbs, p.stats.damageCandidateEvents, p.stats.damageCandidateResult, p.stats.healingCandidateEvents, p.stats.healingCandidateResult, p.stats.otherCandidateEvents, p.stats.otherCandidateResult, p.stats.unclassifiedEvents, p.stats.unclassifiedResult, p.stats.entityAdds, p.stats.entityRemoves, p.stats.entityActiveRequests, p.stats.buffSnapshotResponses, p.stats.activeResponseIdentityComponents, p.stats.activeResponseSummonerComponents, p.stats.activeResponseMonsterComponents, p.stats.activeResponseFollowerComponents, len(p.entities.entities), p.stats.observedSourceEntity, p.stats.observedTargetEntity, p.stats.verifiedCharacters, p.stats.correlatedCharacters, p.stats.monsterResolved, p.stats.skillResolved, p.stats.unknownDamageEvents, p.stats.observedBuffEvents, p.stats.verifiedBuffEvents, p.stats.parseErrors, p.stats.decryptErrors, p.stats.compressedErrors, p.stats.handshakeFrames, p.stats.handshakeOK, p.stats.handshakeErrors)
}

func (p *processor) verifyKnownBaseline() error {
	if p.stats.damageRequests != 99 || p.stats.candidateResponses != 85 || p.stats.printed != 80 || p.stats.matchedCombatResultAbs != 310455 {
		return fmt.Errorf("requests=%d responses=%d matched=%d combat_result_abs=%d; expected 99/85/80/310455",
			p.stats.damageRequests, p.stats.candidateResponses, p.stats.printed, p.stats.matchedCombatResultAbs)
	}
	return nil
}
