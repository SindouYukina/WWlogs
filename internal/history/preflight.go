package history

import (
	"bytes"
	"crypto/aes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/xts"
)

// Preflight.
//
// A key reaches SQLite only after it has been shown, outside SQLite, to be the key the history file was written with.
// SQLite treats a write-ahead log or a rollback journal it cannot read as empty: opened with a key that is not the file's,
// it reads the log's encrypted header as noise, recovers nothing from it, and deletes the log when the connection closes,
// so every committed transaction not yet checkpointed into the database is lost (research/runs/2026-11-15/
// v0.8.0-phase1b-performance-integrity-gate and v0.8.0-hwid-preflight-safety).
//
// The preflight reads the first sector of the database, of its write-ahead log and of its rollback journal, read-only,
// and decrypts each in memory with the candidate key exactly as the xts VFS does: AES-256-XTS over 512-byte sectors, the
// sector number as the tweak (encryption.go). It opens no SQLite connection, so nothing recovers, checkpoints, truncates,
// rewrites or deletes a file. Only on preflightReady does the store open SQLite; on every other outcome every file of the
// history stays byte for byte as it was.
//
// The checks prove which key wrote the files, not that the files are intact: XTS has no authentication (encryption.go),
// and a damaged page beyond the first sector is still found by SQLite itself (quick_check) after a successful preflight.

// preflightOutcome is what the preflight concluded about a key and the files of a history.
type preflightOutcome string

const (
	// preflightReady: the database and its logs decrypt to valid headers with the key; SQLite may open them with it.
	preflightReady preflightOutcome = "ready"
	// preflightKeyMismatch: the first sector of the database does not decrypt to an SQLite header with the key. Either the
	// key is not the file's, or the first sector is damaged; from the file alone the two cannot be told apart, and the key is
	// the conservative explanation.
	preflightKeyMismatch preflightOutcome = "key_mismatch"
	// preflightWALDamaged: the database decrypts with the key but its write-ahead log header does not. SQLite would treat the
	// log as empty and drop it, so the history is not opened and the log is kept.
	preflightWALDamaged preflightOutcome = "wal_damaged"
	// preflightJournalDamaged: the database decrypts with the key but a rollback journal beside it is neither empty nor a
	// journal. SQLite would discard it instead of rolling the database back, so the history is not opened.
	preflightJournalDamaged preflightOutcome = "journal_damaged"
)

// sectorSize is the xts VFS sector: every encrypted file is a sequence of 512-byte sectors, and the tweak of a sector is its
// offset divided by this size.
const sectorSize = 512

// walMagic and walVersion start every SQLite write-ahead log (https://sqlite.org/fileformat.html#the_write_ahead_log); the
// lowest bit of the magic says whether the header checksums use big-endian words.
const (
	walMagicLittleEndian = 0x377f0682
	walMagicBigEndian    = 0x377f0683
	walVersion           = 3007000
	walHeaderSize        = 32
)

// journalMagic starts the header of a rollback journal (https://sqlite.org/fileformat.html#the_rollback_journal). A journal
// whose first byte is zero is not hot: SQLite ignores it.
var journalMagic = []byte{0xd9, 0xd5, 0x05, 0xf9, 0x20, 0xa1, 0x63, 0xd7}

// preflight checks key against the history at path and, when the database decrypts, against its write-ahead log and its
// rollback journal. withLogs is false only for a history another process records into: that process owns and rewrites the
// logs while they are read, and a read-only connection cannot discard them. An error is a failure to read a file, never
// a judgement of its content.
func preflight(path string, key []byte, withLogs bool) (preflightOutcome, error) {
	cipher, err := xts.NewCipher(aes.NewCipher, key)
	if err != nil {
		return "", fmt.Errorf("history key: %w", err)
	}
	header, err := decryptFirstSector(path, cipher)
	if err != nil {
		return "", err
	}
	defer clear(header)
	pageSize, ok := databaseHeader(header)
	if !ok {
		return preflightKeyMismatch, nil
	}
	if !withLogs {
		return preflightReady, nil
	}
	wal, err := decryptFirstSector(path+"-wal", cipher)
	switch {
	case errors.Is(err, errNoSector):
	case err != nil:
		return "", err
	default:
		defer clear(wal)
		if !walHeader(wal, pageSize) {
			return preflightWALDamaged, nil
		}
	}
	journal, err := decryptFirstSector(path+"-journal", cipher)
	switch {
	case errors.Is(err, errNoSector):
	case err != nil:
		return "", err
	default:
		defer clear(journal)
		if journal[0] != 0 && !bytes.Equal(journal[:len(journalMagic)], journalMagic) {
			return preflightJournalDamaged, nil
		}
	}
	return preflightReady, nil
}

// errNoSector says a file is absent or shorter than one sector. SQLite reads such a log as empty, so it holds nothing to
// protect; a database that short holds no header to check.
var errNoSector = errors.New("no complete first sector")

// decryptFirstSector reads the first sector of path read-only and decrypts it with sector number 0.
func decryptFirstSector(path string, cipher *xts.Cipher) ([]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNoSector
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	sector := make([]byte, sectorSize)
	if _, err := io.ReadFull(file, sector); err != nil {
		clear(sector)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errNoSector
		}
		return nil, err
	}
	cipher.Decrypt(sector, sector, 0)
	return sector, nil
}

// databaseHeader checks the fixed fields of an SQLite database header and returns the page size it declares.
func databaseHeader(header []byte) (uint32, bool) {
	if len(header) < 100 || !bytes.Equal(header[:len(sqliteHeader)], sqliteHeader) {
		return 0, false
	}
	pageSize := uint32(binary.BigEndian.Uint16(header[16:18]))
	if pageSize == 1 {
		pageSize = 65536
	}
	switch {
	case pageSize < 512 || pageSize > 65536 || pageSize&(pageSize-1) != 0:
		return 0, false
	case header[18] != 1 && header[18] != 2, header[19] != 1 && header[19] != 2:
		return 0, false
	case header[21] != 64 || header[22] != 32 || header[23] != 32:
		return 0, false
	}
	return pageSize, true
}

// walHeader checks a write-ahead log header: its magic and version, the page size of its database, and the checksum over
// its first 24 bytes, which SQLite checks too before it reads any frame of the log.
func walHeader(header []byte, pageSize uint32) bool {
	if len(header) < walHeaderSize {
		return false
	}
	magic := binary.BigEndian.Uint32(header[0:4])
	if magic != walMagicLittleEndian && magic != walMagicBigEndian {
		return false
	}
	if binary.BigEndian.Uint32(header[4:8]) != walVersion || binary.BigEndian.Uint32(header[8:12]) != pageSize {
		return false
	}
	s0, s1 := walChecksum(header[:24], magic == walMagicBigEndian)
	return s0 == binary.BigEndian.Uint32(header[24:28]) && s1 == binary.BigEndian.Uint32(header[28:32])
}

// walChecksum is the SQLite write-ahead log checksum (https://sqlite.org/fileformat.html#checksum_algorithm) of data, a
// multiple of 8 bytes, read as 32-bit words in the byte order the log's magic names.
func walChecksum(data []byte, bigEndian bool) (uint32, uint32) {
	var s0, s1 uint32
	word := binary.LittleEndian.Uint32
	if bigEndian {
		word = binary.BigEndian.Uint32
	}
	for i := 0; i+8 <= len(data); i += 8 {
		s0 += word(data[i:i+4]) + s1
		s1 += word(data[i+4:i+8]) + s0
	}
	return s0, s1
}
