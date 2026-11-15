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
//   - a new or empty file gets a derived key (keyderive.go) and the record of it (keyformat.go), written first; a log left
//     beside it by a history that is gone refuses it instead;
//   - an existing file is tried with the derived key its record names, then with the DPAPI key of WWlogs v0.7.0 when that
//     key file sits beside it, then with every other key this computer derives. A record proves nothing alone: one copied
//     from another computer, or a history put beside this computer's record, neither locks out a history this computer can
//     open nor makes a foreign one look like this computer's. A key that opens a history whose record does not name it gets
//     its record written again once the history is open (Store.open).
//
// When no key opens the file, the keys are tried once more against the rest of it: a key that decrypts the rest proves the
// first sector damaged (ReasonCorrupt); otherwise the file was made with a key this computer and build do not have
// (ReasonKeyVerification, or ReasonKeyMismatch for a v0.7.0 history). Every refusal leaves every file as it was: nothing
// has been opened.

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
		orphaned, err := orphanedLogs(s.path)
		if err != nil {
			return s.refuse(ReasonOpenFailed, err)
		}
		if orphaned {
			// SQLite would delete the log of a database with no pages; it is kept for the history it belongs to.
			return s.refuse(ReasonWALIntegrity, errors.New("a write-ahead log or rollback journal is left beside a history file that is missing or empty"))
		}
		return s.unlockNew(record, state)
	}
	if state == recordUnsupported {
		return s.refuse(ReasonFormatUnsupported, errors.New("the history is of a format or key derivation this build does not know"))
	}
	return s.unlockExisting(record, state, legacy, readOnly)
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

// keyCandidate is one key an existing history may have been written with. derived is nil for the DPAPI key of v0.7.0.
type keyCandidate struct {
	key     []byte
	derived *derivedKey
}

// unlockExisting opens an existing history with the first key that the preflight proves to be the file's (see the top of
// this file for the order and the classification of a file no key opens).
func (s *Store) unlockExisting(record keyRecord, state recordState, legacy, readOnly bool) error {
	derived, keyID, derivedErr := existingHistoryKeys(s.inputs, sourceSystemUUID, sourceBaseboardSerial)
	defer wipeKeys(derived)
	var named *derivedKey
	if state == recordValid {
		named = matchingKey(derived, record)
	}
	var candidates []keyCandidate
	if named != nil {
		candidates = append(candidates, keyCandidate{key: named.key, derived: named})
	}
	var legacyKey []byte
	var legacyErr error
	if legacy {
		if legacyKey, legacyErr = loadKey(s.path, s.provider); legacyErr == nil {
			candidates = append(candidates, keyCandidate{key: legacyKey})
		}
	}
	for i := range derived {
		if &derived[i] != named {
			candidates = append(candidates, keyCandidate{key: derived[i].key, derived: &derived[i]})
		}
	}
	keepLegacy := false
	defer func() {
		if !keepLegacy {
			clear(legacyKey)
		}
	}()
	if len(candidates) == 0 {
		if legacy && state != recordValid {
			return s.refuse(legacyKeyReason(legacyErr), legacyErr)
		}
		return s.refuse(keyErrorReason(derivedErr), derivedErr)
	}
	for _, candidate := range candidates {
		outcome, err := s.preflightWith(candidate.key, readOnly)
		switch {
		case err != nil:
			return s.refuse(ReasonOpenFailed, fmt.Errorf("check history key: %w", err))
		case outcome == preflightKeyMismatch:
			continue
		case outcome == preflightWALDamaged:
			return s.refuse(ReasonWALIntegrity, errors.New("the write-ahead log beside the history does not decrypt with the history's key"))
		case outcome == preflightJournalDamaged:
			return s.refuse(ReasonCorrupt, errors.New("the rollback journal beside the history does not decrypt with the history's key"))
		}
		if candidate.derived == nil {
			keepLegacy = true
			s.key, s.protection = legacyKey, s.provider.ID()
			return nil
		}
		s.key, s.protection = append([]byte(nil), candidate.key...), keyProtectionDerived
		if candidate.derived != named && !readOnly {
			repaired := newKeyRecord(candidate.derived.source, candidate.derived.check, keyID)
			s.repair, s.repairReplace = &repaired, state != recordAbsent
		}
		return nil
	}
	for _, candidate := range candidates {
		damaged, err := keyDecryptsStructure(s.path, candidate.key)
		if err != nil {
			return s.refuse(ReasonOpenFailed, fmt.Errorf("check history key: %w", err))
		}
		if damaged {
			return s.refuse(ReasonCorrupt, errors.New("the history decrypts with its key except for its first sector, which is damaged"))
		}
	}
	switch {
	case derivedErr != nil && state == recordValid:
		// The record names a derived key, and this computer could not derive one to try.
		return s.refuse(keyErrorReason(derivedErr), derivedErr)
	case state == recordValid:
		detail := "the database does not decrypt with any key this computer derives, although its record names one"
		if named == nil {
			detail = "no key this computer derives matches the history's key check or decrypts the history"
		}
		if keyID != "" && keyID != record.RSAKeyID {
			detail += "; the history was made with another protocol key"
		}
		return s.refuse(ReasonKeyVerification, errors.New(detail))
	case legacy && legacyErr != nil:
		return s.refuse(legacyKeyReason(legacyErr), legacyErr)
	case legacy:
		return s.refuse(ReasonKeyMismatch, errors.New("neither the history key file nor a key this computer derives decrypts the history file"))
	}
	return s.refuse(ReasonKeyVerification, errors.New("no key this computer derives decrypts the history, and no record names its key"))
}

// legacyKeyReason is the status reason of a DPAPI key file that could not be read or unsealed.
func legacyKeyReason(err error) string {
	if errors.Is(err, errKeyMissing) {
		return ReasonKeyMissing
	}
	return ReasonKeyUnavailable
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
