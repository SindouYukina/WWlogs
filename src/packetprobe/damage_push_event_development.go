//go:build wwdev

package main

import (
	"database/sql"
	"fmt"
	"strconv"
)

// loadSQLiteAbnormalDamageIDs reads the DamageIds that AbnormalDamageConfig names through its Abnormal<id> columns.
func loadSQLiteAbnormalDamageIDs(db *sql.DB, master *gameMaster) error {
	exists, err := masterTableExists(db, "abnormaldamageconfig")
	if err != nil || !exists {
		return err
	}
	rows, err := db.Query(`PRAGMA table_info("abnormaldamageconfig")`)
	if err != nil {
		return fmt.Errorf("inspect abnormaldamageconfig columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return fmt.Errorf("read abnormaldamageconfig column: %w", err)
		}
		if match := abnormalColumn.FindStringSubmatch(name); match != nil {
			id, err := strconv.ParseUint(match[1], 10, 64)
			if err != nil {
				return fmt.Errorf("abnormaldamageconfig column %q: %w", name, err)
			}
			master.abnormalDamageIDs[id] = struct{}{}
		}
	}
	return rows.Err()
}

// writePushDamageEvent writes one pushed DamageExecuteNotify as a DAMAGE_PUSH_RESULT event. It is a separate category:
// not printed, not counted in encounter totals, not fed to the overlap statistics, and it never credits a character
// because the push names no instigator. Ids use the auxiliary namespace so existing event ids do not shift.
func (p *processor) writePushDamageEvent(record damagePushRecord) {
	if p.combatEventsJSONL == nil {
		return
	}
	request := pendingDamageRequest{
		attackerEntityID: uint64(record.AttackerID), hasAttackerEntityID: record.AttackerID > 0,
		targetEntityID: uint64(record.TargetID), hasTargetEntityID: record.TargetID > 0,
		damageID: uint64(record.DamageID), hasDamageID: record.DamageID > 0,
	}
	amount := record.Damage
	if amount < 0 {
		amount = -amount
	}
	event := p.buildDamageEvent(0, request, int32(record.Damage), int32(record.ChangeLife), true, amount)
	view := event.jsonlView(p.masters)
	view.EventType = "DAMAGE_PUSH_RESULT"

	push := &combatEventPushJSONL{
		Stream: record.Stream, PairedWithRPC: false, ProtocolElement: record.ElementType, ElementName: record.ElementName,
		PartID: record.PartID, IsCrit: record.IsCrit, KilledTarget: record.KilledTarget, DamageRaw: record.Damage, ChangeLifeRaw: record.ChangeLife,
		Category: pushCategoryUnclassified, CategoryStatus: resolutionUnknown,
		CategoryEvidence: "The DamageId is not named by AbnormalDamageConfig; the pushed damage is kept uncategorized",
		Attribution:      "The push carries an attacker entity id as observed but no instigator; no character is credited",
	}
	if p.masters != nil {
		if _, abnormal := p.masters.abnormalDamageIDs[uint64(record.DamageID)]; abnormal && record.DamageID > 0 {
			push.Category, push.CategoryStatus = pushCategoryAbnormal, resolutionCorrelated
			push.CategoryEvidence = "DamageId equals an Abnormal<id> column of AbnormalDamageConfig (one per element); pushed damage lowered the target's Life by its amount in the saved captures; the in-game name of the status is not confirmed"
		}
	}
	view.Push = push
	view.Credit = combatEventCreditJSONL{Status: resolutionUnknown, NameStatus: resolutionUnknown, Evidence: "Pushed damage names no instigator; the credited character is not established"}
	view.Actors.CreditedCharacter = actorRoleJSONL{ActorType: "Unknown", Status: resolutionUnknown, Evidence: "Pushed damage names no instigator; the credited character is not established"}
	view.Summoner = combatEventSummonerJSONL{Status: resolutionUnknown}
	p.writeEnvelopedEventJSONL(view, "p")
}
