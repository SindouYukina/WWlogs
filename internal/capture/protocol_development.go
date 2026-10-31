//go:build wwdev

package capture

import (
	"fmt"
	"io"
)

func (p *processor) printSummary(w io.Writer) {
	fmt.Fprintf(w, "damage_rpc_summary requests=%d candidate_responses=%d visible_matched=%d unmatched_requests=%d unmatched_responses=%d invalid_responses=%d duplicate_responses=%d evicted_pending=%d missing_attacker_entity_id=%d visible_combat_result_abs=%d decoded_matched_events=%d decoded_combat_result_abs=%d player_related_events=%d included_unresolved_relation_events=%d suppressed_confirmed_unrelated_events=%d player_role_reports=%d player_role_report_parse_errors=%d decoded_damage_candidate_events=%d decoded_damage_candidate_result_abs=%d decoded_healing_candidate_events=%d decoded_healing_candidate_result_abs=%d decoded_other_combat_candidate_events=%d decoded_other_combat_candidate_result_abs=%d decoded_unclassified_events=%d decoded_unclassified_result_abs=%d entity_adds=%d entity_removes=%d entity_active_requests=%d entity_active_responses=%d active_response_identity_components=%d active_response_summoner_components=%d active_response_monster_components=%d active_response_follower_components=%d runtime_entities=%d decoded_observed_source_events=%d decoded_observed_target_events=%d decoded_verified_character_events=%d decoded_correlated_character_events=%d decoded_resolved_monster_events=%d decoded_resolved_skill_events=%d decoded_unknown_events=%d observed_buff_events=%d verified_buff_events=%d parse_errors=%d decrypt_errors=%d compressed_errors=%d handshake_frames=%d handshake_ok=%d handshake_errors=%d\n",
		p.stats.damageRequests, p.stats.candidateResponses, p.stats.printed, len(p.pending), p.stats.unpairedResponses, p.stats.invalidResponses, p.stats.duplicateResponses, p.stats.evictedPending, p.stats.missingAttackerEntityID, p.stats.matchedCombatResultAbs, p.stats.decodedMatchedEvents, p.stats.decodedCombatResultAbs, p.stats.playerRelatedEvents, p.stats.includedUnresolvedRelations, p.stats.suppressedUnrelatedEvents, p.stats.playerRoleReports, p.stats.playerRoleReportParseErrors, p.stats.damageCandidateEvents, p.stats.damageCandidateResult, p.stats.healingCandidateEvents, p.stats.healingCandidateResult, p.stats.otherCandidateEvents, p.stats.otherCandidateResult, p.stats.unclassifiedEvents, p.stats.unclassifiedResult, p.stats.entityAdds, p.stats.entityRemoves, p.stats.entityActiveRequests, p.stats.buffSnapshotResponses, p.stats.activeResponseIdentityComponents, p.stats.activeResponseSummonerComponents, p.stats.activeResponseMonsterComponents, p.stats.activeResponseFollowerComponents, len(p.entities.entities), p.stats.observedSourceEntity, p.stats.observedTargetEntity, p.stats.verifiedCharacters, p.stats.correlatedCharacters, p.stats.monsterResolved, p.stats.skillResolved, p.stats.unknownDamageEvents, p.stats.observedBuffEvents, p.stats.verifiedBuffEvents, p.stats.parseErrors, p.stats.decryptErrors, p.stats.compressedErrors, p.stats.handshakeFrames, p.stats.handshakeOK, p.stats.handshakeErrors)
	if p.partRefOut != nil {
		fmt.Fprintf(w, "part_reference_output errors=%d\n", p.partRefErrors)
	}
}

func (p *processor) verifyKnownBaseline() error {
	if p.stats.damageRequests != 117 || p.stats.candidateResponses != 117 || p.stats.decodedMatchedEvents != 116 || p.stats.decodedCombatResultAbs != 584134 ||
		p.stats.printed != 116 || p.stats.matchedCombatResultAbs != 584134 || p.stats.playerRelatedEvents != 32 ||
		p.stats.includedUnresolvedRelations != 84 || p.stats.suppressedUnrelatedEvents != 0 || p.stats.playerRoleReports == 0 ||
		len(p.pending) != 0 || p.stats.unpairedResponses != 0 || p.stats.invalidResponses != 1 ||
		p.stats.parseErrors != 0 || p.stats.decryptErrors != 0 || p.stats.compressedErrors != 0 ||
		p.stats.playerRoleReportParseErrors != 0 ||
		p.stats.handshakeFrames != 1 || p.stats.handshakeOK != 1 || p.stats.handshakeErrors != 0 {
		return fmt.Errorf("requests=%d responses=%d decoded_matched=%d decoded_combat_result_abs=%d visible_matched=%d visible_combat_result_abs=%d player_related=%d unresolved_relation=%d suppressed_unrelated=%d player_role_reports=%d player_role_report_parse_errors=%d unmatched_requests=%d unmatched_responses=%d invalid_responses=%d parse_errors=%d decrypt_errors=%d compressed_errors=%d handshake=%d/%d/%d; expected raw 117/117/116/584134, visible 116/584134 split into 32 confirmed player-related and 84 unresolved, 0 confirmed unrelated suppressed, 0/0/1 invalid, zero decode errors, handshake 1/1/0",
			p.stats.damageRequests, p.stats.candidateResponses, p.stats.decodedMatchedEvents, p.stats.decodedCombatResultAbs, p.stats.printed, p.stats.matchedCombatResultAbs, p.stats.playerRelatedEvents, p.stats.includedUnresolvedRelations, p.stats.suppressedUnrelatedEvents, p.stats.playerRoleReports, p.stats.playerRoleReportParseErrors, len(p.pending), p.stats.unpairedResponses, p.stats.invalidResponses,
			p.stats.parseErrors, p.stats.decryptErrors, p.stats.compressedErrors, p.stats.handshakeFrames, p.stats.handshakeOK, p.stats.handshakeErrors)
	}
	return nil
}
