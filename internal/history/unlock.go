package history

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Finding the key of a history.
//
// unlock decides which key opens the file and proves it against the file before SQLite sees it (preflight.go), so SQLite
// only ever opens a history with its own key:
//
//   - a new or empty file gets a derived key (keyderive.go) and the record of it (keyformat.go), written first;
//   - a file with a record is opened with the derived key whose check value the record names, for the source it names;
//   - a file of WWlogs v0.7.0 (its DPAPI key file beside it, no record) is opened with that key;
//   - a file with neither is tried with every derived key of this computer, so a lost record never locks a history out.
//
// Every refusal leaves every file as it was: nothing has been opened.

// readOnlyPreflightAttempts and readOnlyPreflightPause bound how often a read-only open reads a first sector again.
const (
	readOnlyPreflightAttempts = 3
	readOnlyPreflightPause    = 50 * time.Millisecond
)

// refuse makes the store unavailable for reason and returns err.
func (s *Store) refuse(reason string, err error) error {
	s.setStatus(StateUnavailable, reason)
	return err
}

// keyErrorReason is the status reason of a failure to derive a key.
func keyErrorReason(err error) string {
	switch {
	case errors.Is(err, errHardwareIdentity):
		return ReasonHardwareIdentity
	case errors.Is(err, errKeyMaterial):
		return ReasonKeyMaterial
	}
	return ReasonOpenFailed
}

// unlock sets the store's key for the file of kind, or refuses it. readOnly is a history another process records into: it
// is never created or repaired, and its logs belong to that process.
func (s *Store) unlock(kind fileKind, readOnly bool) error {
	record, state, err := readKeyRecord(s.path)
	if err != nil {
		return s.refuse(ReasonOpenFailed, err)
	}
	legacy, err := legacyKeyPresent(s.path)
	if err != nil {
		return s.refuse(ReasonOpenFailed, err)
	}
	if kind == fileAbsent || kind == fileEmpty {
		if readOnly {
			return s.refuse(ReasonLocked, errors.New("the history another process records into holds nothing yet"))
		}
		return s.unlockNew(record, state)
	}
	switch {
	case state == recordUnsupported:
		return s.refuse(ReasonFormatUnsupported, errors.New("the history is of a format or key derivation this build does not know"))
	case state == recordValid:
		before := s.status
		err := s.unlockDerived(record, readOnly)
		if err != nil && legacy {
			// A v0.7.0 history beside a record that does not describe it (for example one restored from a backup) opens with
			// its own key; the record keeps describing nothing until the history is migrated.
			derived := s.status
			s.status = before
			if s.unlockLegacy(readOnly) == nil {
				return nil
			}
			s.status = derived
		}
		return err
	case legacy:
		return s.unlockLegacy(readOnly)
	}
	return s.unlockBySector(state, readOnly)
}

// unlockNew derives the key of a new history and writes its record before the database exists, so a history is never
// created without a record of its key. The file holds nothing, so a record that describes another key is replaced.
func (s *Store) unlockNew(record keyRecord, state recordState) error {
	if state == recordUnsupported {
		return s.refuse(ReasonFormatUnsupported, errors.New("a later WWlogs made the history record beside an empty history file"))
	}
	key, fresh, err := newHistoryKey(s.inputs)
	if err != nil {
		return s.refuse(keyErrorReason(err), err)
	}
	if state != recordValid || record != fresh {
		if err := writeKeyRecord(s.path, fresh, state != recordAbsent); err != nil {
			clear(key.key)
			return s.refuse(ReasonOpenFailed, err)
		}
	}
	s.key, s.protection = key.key, keyProtectionDerived
	return nil
}

// unlockDerived opens a history with the derived key its record names.
func (s *Store) unlockDerived(record keyRecord, readOnly bool) error {
	keys, keyID, err := existingHistoryKeys(s.inputs, record.Source)
	if err != nil {
		return s.refuse(keyErrorReason(err), err)
	}
	defer wipeKeys(keys)
	match := matchingKey(keys, record)
	if match == nil {
		detail := "no key this computer derives matches the history's key check"
		if keyID != record.RSAKeyID {
			detail += "; the history was made with another protocol key"
		}
		return s.refuse(ReasonKeyVerification, errors.New(detail))
	}
	outcome, err := s.preflightWith(match.key, readOnly)
	switch {
	case err != nil:
		return s.refuse(ReasonOpenFailed, fmt.Errorf("check history key: %w", err))
	case outcome == preflightKeyMismatch:
		// The check value proves the key; a first sector that does not decrypt with it is damage.
		return s.refuse(ReasonCorrupt, errors.New("the history's key check matches but its first sector does not decrypt"))
	case outcome == preflightWALDamaged:
		return s.refuse(ReasonWALIntegrity, errors.New("the write-ahead log beside the history does not decrypt with its key"))
	case outcome == preflightJournalDamaged:
		return s.refuse(ReasonCorrupt, errors.New("the rollback journal beside the history does not decrypt with its key"))
	}
	s.key, s.protection = append([]byte(nil), match.key...), keyProtectionDerived
	return nil
}

// unlockLegacy opens a history of WWlogs v0.7.0 with its DPAPI-sealed key.
func (s *Store) unlockLegacy(readOnly bool) error {
	key, err := loadKey(s.path, s.provider)
	if err != nil {
		reason := ReasonKeyUnavailable
		if errors.Is(err, errKeyMissing) {
			reason = ReasonKeyMissing
		}
		return s.refuse(reason, err)
	}
	outcome, err := s.preflightWith(key, readOnly)
	if err != nil || outcome != preflightReady {
		clear(key)
	}
	switch {
	case err != nil:
		return s.refuse(ReasonOpenFailed, fmt.Errorf("check history key: %w", err))
	case outcome == preflightKeyMismatch:
		return s.refuse(ReasonKeyMismatch, errors.New("the history key does not decrypt the history file"))
	case outcome == preflightWALDamaged:
		return s.refuse(ReasonWALIntegrity, errors.New("the write-ahead log beside the history does not decrypt with the history key"))
	case outcome == preflightJournalDamaged:
		return s.refuse(ReasonCorrupt, errors.New("the rollback journal beside the history does not decrypt with the history key"))
	}
	s.key, s.protection = key, s.provider.ID()
	return nil
}

// unlockBySector opens a derived history whose record is lost or damaged by trying every key this computer derives against
// the file's first sector. A key that opens it gets its record written again once the history is open (Store.open).
func (s *Store) unlockBySector(state recordState, readOnly bool) error {
	keys, keyID, err := existingHistoryKeys(s.inputs, sourceSystemUUID, sourceBaseboardSerial)
	if err != nil {
		return s.refuse(keyErrorReason(err), err)
	}
	defer wipeKeys(keys)
	for i := range keys {
		outcome, err := s.preflightWith(keys[i].key, readOnly)
		switch {
		case err != nil:
			return s.refuse(ReasonOpenFailed, fmt.Errorf("check history key: %w", err))
		case outcome == preflightKeyMismatch:
			continue
		case outcome == preflightWALDamaged:
			return s.refuse(ReasonWALIntegrity, errors.New("the write-ahead log beside the history does not decrypt with its key"))
		case outcome == preflightJournalDamaged:
			return s.refuse(ReasonCorrupt, errors.New("the rollback journal beside the history does not decrypt with its key"))
		}
		s.key, s.protection = append([]byte(nil), keys[i].key...), keyProtectionDerived
		if !readOnly {
			record := newKeyRecord(keys[i].source, keys[i].check, keyID)
			s.repair, s.repairReplace = &record, state == recordDamaged
		}
		return nil
	}
	return s.refuse(ReasonKeyVerification, errors.New("no key this computer derives decrypts the history, and no record names its key"))
}

// preflightWith runs the preflight with key. A read-only open checks the database header only, and reads it again while the
// recording process may be rewriting it.
func (s *Store) preflightWith(key []byte, readOnly bool) (preflightOutcome, error) {
	if !readOnly {
		return preflight(s.path, key, true)
	}
	outcome, err := preflightKeyMismatch, error(nil)
	for attempt := 0; attempt < readOnlyPreflightAttempts && err == nil && outcome != preflightReady; attempt++ {
		if attempt > 0 {
			time.Sleep(readOnlyPreflightPause)
		}
		outcome, err = preflight(s.path, key, false)
	}
	return outcome, err
}

// legacyKeyPresent reports whether the DPAPI key file of a v0.7.0 history sits beside path.
func legacyKeyPresent(path string) (bool, error) {
	info, err := os.Stat(keyPath(path))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("inspect history key file: %w", err)
	}
	return info.Mode().IsRegular(), nil
}
