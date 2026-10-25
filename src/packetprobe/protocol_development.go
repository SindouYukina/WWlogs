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
	if p.stats.damageRequests != 117 || p.stats.candidateResponses != 117 || p.stats.printed != 116 || p.stats.sumAbsDamage != 584134 ||
		len(p.pending) != 1 || p.stats.unpairedResponses != 0 || p.stats.invalidResponses != 1 ||
		p.stats.parseErrors != 0 || p.stats.decryptErrors != 0 || p.stats.compressedErrors != 0 ||
		p.stats.handshakeFrames != 1 || p.stats.handshakeOK != 1 || p.stats.handshakeErrors != 0 {
		return fmt.Errorf("requests=%d responses=%d matched=%d abs_sum=%d unmatched_requests=%d unmatched_responses=%d invalid_responses=%d parse_errors=%d decrypt_errors=%d compressed_errors=%d handshake=%d/%d/%d; expected 117/117/116/584134, 1/0/1, zero decode errors, handshake 1/1/0",
			p.stats.damageRequests, p.stats.candidateResponses, p.stats.printed, p.stats.sumAbsDamage, len(p.pending), p.stats.unpairedResponses, p.stats.invalidResponses,
			p.stats.parseErrors, p.stats.decryptErrors, p.stats.compressedErrors, p.stats.handshakeFrames, p.stats.handshakeOK, p.stats.handshakeErrors)
	}
	return nil
}
