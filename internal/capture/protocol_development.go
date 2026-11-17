//go:build wwdev

package capture

import (
	"fmt"
	"io"
)

func (p *Processor) printSummary(w io.Writer) {
	fmt.Fprintf(w, "damage_rpc_summary requests=%d candidate_responses=%d visible_matched=%d unmatched_requests=%d unmatched_responses=%d invalid_responses=%d duplicate_responses=%d evicted_pending=%d missing_attacker_entity_id=%d visible_combat_result_abs=%d decoded_matched_events=%d decoded_combat_result_abs=%d player_related_events=%d included_unresolved_relation_events=%d suppressed_confirmed_unrelated_events=%d player_role_reports=%d player_role_report_parse_errors=%d decoded_damage_candidate_events=%d decoded_damage_candidate_result_abs=%d decoded_healing_candidate_events=%d decoded_healing_candidate_result_abs=%d decoded_other_combat_candidate_events=%d decoded_other_combat_candidate_result_abs=%d decoded_unclassified_events=%d decoded_unclassified_result_abs=%d entity_adds=%d entity_removes=%d entity_active_requests=%d entity_active_responses=%d active_response_identity_components=%d active_response_summoner_components=%d active_response_monster_components=%d active_response_follower_components=%d runtime_entities=%d decoded_observed_source_events=%d decoded_observed_target_events=%d decoded_verified_character_events=%d decoded_correlated_character_events=%d decoded_resolved_monster_events=%d decoded_resolved_skill_events=%d decoded_unknown_events=%d observed_buff_events=%d verified_buff_events=%d parse_errors=%d decrypt_errors=%d compressed_errors=%d handshake_frames=%d handshake_ok=%d handshake_errors=%d\n",
		p.Stats.DamageRequests, p.Stats.CandidateResponses, p.Stats.Printed, len(p.Pending), p.Stats.UnpairedResponses, p.Stats.InvalidResponses, p.Stats.DuplicateResponses, p.Stats.EvictedPending, p.Stats.MissingAttackerEntityID, p.Stats.MatchedCombatResultAbs, p.Stats.DecodedMatchedEvents, p.Stats.DecodedCombatResultAbs, p.Stats.PlayerRelatedEvents, p.Stats.IncludedUnresolvedRelations, p.Stats.SuppressedUnrelatedEvents, p.Stats.PlayerRoleReports, p.Stats.PlayerRoleReportParseErrors, p.Stats.DamageCandidateEvents, p.Stats.DamageCandidateResult, p.Stats.HealingCandidateEvents, p.Stats.HealingCandidateResult, p.Stats.OtherCandidateEvents, p.Stats.OtherCandidateResult, p.Stats.UnclassifiedEvents, p.Stats.UnclassifiedResult, p.Stats.EntityAdds, p.Stats.EntityRemoves, p.Stats.EntityActiveRequests, p.Stats.BuffSnapshotResponses, p.Stats.ActiveResponseIdentityComponents, p.Stats.ActiveResponseSummonerComponents, p.Stats.ActiveResponseMonsterComponents, p.Stats.ActiveResponseFollowerComponents, len(p.Entities.Entities), p.Stats.ObservedSourceEntity, p.Stats.ObservedTargetEntity, p.Stats.VerifiedCharacters, p.Stats.CorrelatedCharacters, p.Stats.MonsterResolved, p.Stats.SkillResolved, p.Stats.UnknownDamageEvents, p.Stats.ObservedBuffEvents, p.Stats.VerifiedBuffEvents, p.Stats.ParseErrors, p.Stats.DecryptErrors, p.Stats.CompressedErrors, p.Stats.HandshakeFrames, p.Stats.HandshakeOK, p.Stats.HandshakeErrors)
	if p.Dev.partRefOut != nil {
		fmt.Fprintf(w, "part_reference_output errors=%d\n", p.Dev.partRefErrors)
	}
}
