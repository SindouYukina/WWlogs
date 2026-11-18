package history

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The format of a history.
//
// The database is encrypted whole, so nothing in it can say which key opens it before the key is known. The files beside it
// say it instead:
//
//   - combat-history.kdf: a history whose key is derived (keyderive.go). A small record, written once and atomically beside
//     the database: the format, the derivation version, the identifier source, the key id of the protocol key and the key
//     check value. None of it is secret: it holds no identifier, no key and nothing derived from a key except the check
//     value, which only lets someone who already has both inputs confirm them. Nothing in it needs keeping or backing up.
//   - combat-history.key: a history of WWlogs v0.7.0 and earlier, whose random key DPAPI seals (keys.go).
//
// The record is read before SQLite is opened, so it never needs the database to decide its own key. The key check value is
// the first test of a derived key; the preflight (preflight.go) then proves the key against the file. A lost or damaged
// record never makes a readable history unreadable: the derived keys are tried against the file's first sector instead, and
// the record is written again once one of them has opened it. A record of an unknown format or of a later derivation version
// is never rewritten: a later WWlogs made that history.

const (
	keyRecordFormat = "wwlogs-history-hwid-kdf"
	maxKeyRecord    = 4096
)

// keyRecord is the record of a derived history.
type keyRecord struct {
	Format     string `json:"format"`
	KDFVersion int    `json:"kdfVersion"`
	Source     string `json:"source"`
	RSAKeyID   string `json:"rsaKeyId"`
	KeyCheck   string `json:"keyCheck"`
}

// recordState is what the record beside a history is.
type recordState int

const (
	recordAbsent recordState = iota
	recordValid
	// recordUnsupported: the record names another format or a later derivation version.
	recordUnsupported
	// recordDamaged: the file is not a readable record; it is treated as absent and may be written again.
	recordDamaged
)

// keyRecordPath is the record of a history file: combat-history.db keeps it in combat-history.kdf.
func keyRecordPath(historyPath string) string {
	return strings.TrimSuffix(historyPath, filepath.Ext(historyPath)) + ".kdf"
}

func readKeyRecord(historyPath string) (keyRecord, recordState, error) {
	file, err := os.Open(keyRecordPath(historyPath))
	if errors.Is(err, os.ErrNotExist) {
		return keyRecord{}, recordAbsent, nil
	}
	if err != nil {
		return keyRecord{}, recordAbsent, fmt.Errorf("read history format: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxKeyRecord+1))
	if err != nil {
		return keyRecord{}, recordAbsent, fmt.Errorf("read history format: %w", err)
	}
	if len(data) > maxKeyRecord {
		return keyRecord{}, recordDamaged, nil
	}
	var head struct {
		Format     string `json:"format"`
		KDFVersion int    `json:"kdfVersion"`
	}
	if json.Unmarshal(data, &head) != nil {
		return keyRecord{}, recordDamaged, nil
	}
	if head.Format != keyRecordFormat || head.KDFVersion != derivationVersion {
		if head.Format == "" && head.KDFVersion == 0 {
			return keyRecord{}, recordDamaged, nil
		}
		return keyRecord{}, recordUnsupported, nil
	}
	var record keyRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil || !record.wellFormed() {
		return keyRecord{}, recordDamaged, nil
	}
	return record, recordValid, nil
}

func (r keyRecord) wellFormed() bool {
	check, err := hex.DecodeString(r.KeyCheck)
	id, idErr := hex.DecodeString(r.RSAKeyID)
	return (r.Source == sourceSystemUUID || r.Source == sourceBaseboardSerial) && err == nil && len(check) == historyKeyCheck &&
		idErr == nil && len(id) == protocolKeyIDBytes
}

func (r keyRecord) check() []byte {
	check, _ := hex.DecodeString(r.KeyCheck)
	return check
}

// newKeyRecord describes a history whose key derives from source with the given check value and protocol key id.
func newKeyRecord(source string, check []byte, keyID string) keyRecord {
	return keyRecord{Format: keyRecordFormat, KDFVersion: derivationVersion, Source: source, RSAKeyID: keyID, KeyCheck: hex.EncodeToString(check)}
}

// writeKeyRecord writes the record atomically. It never replaces a record unless replace is set, which only a record that
// describes no readable history allows.
func writeKeyRecord(historyPath string, record keyRecord, replace bool) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := writeFileDurably(keyRecordPath(historyPath), append(data, '\n'), replace); err != nil {
		return fmt.Errorf("write history format: %w", err)
	}
	return nil
}

// writeFileDurably writes data to path atomically: a temporary file, flushed, then moved into place with write-through. It
// replaces an existing file only when replace is set.
func writeFileDurably(path string, data []byte, replace bool) error {
	temporary := path + ".tmp"
	_ = os.Remove(temporary)
	out, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = moveDurably(temporary, path, replace)
	}
	if err != nil {
		os.Remove(temporary)
	}
	return err
}
