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
