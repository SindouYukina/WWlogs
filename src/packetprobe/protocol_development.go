//go:build wwdev

package main

import (
	"fmt"
	"io"
)

func (p *processor) printSummary(w io.Writer) {
	fmt.Fprintf(w, "damage_rpc_summary requests=%d candidate_responses=%d printed=%d unmatched_requests=%d unmatched_responses=%d invalid_responses=%d duplicate_responses=%d missing_attacker_entity_id=%d abs_damage_sum=%d parse_errors=%d decrypt_errors=%d compressed_errors=%d handshake_frames=%d handshake_ok=%d handshake_errors=%d\n",
		p.stats.damageRequests, p.stats.candidateResponses, p.stats.printed, len(p.pending), p.stats.unpairedResponses, p.stats.invalidResponses, p.stats.duplicateResponses, p.stats.missingAttackerEntityID, p.stats.sumAbsDamage, p.stats.parseErrors, p.stats.decryptErrors, p.stats.compressedErrors, p.stats.handshakeFrames, p.stats.handshakeOK, p.stats.handshakeErrors)
}

func (p *processor) verifyKnownBaseline() error {
	if p.stats.damageRequests != 99 || p.stats.candidateResponses != 85 || p.stats.printed != 80 || p.stats.sumAbsDamage != 310455 {
		return fmt.Errorf("requests=%d responses=%d matched=%d abs_sum=%d; expected 99/85/80/310455",
			p.stats.damageRequests, p.stats.candidateResponses, p.stats.printed, p.stats.sumAbsDamage)
	}
	return nil
}
