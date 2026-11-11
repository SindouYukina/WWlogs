package history

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/vfs/xts"
)

// Encryption at rest.
//
// The history file is an SQLite database that the "xts" VFS of github.com/ncruces/go-sqlite3 encrypts page by page: every
// database, write-ahead log, journal and temporary file it opens is encrypted with AES-256-XTS (golang.org/x/crypto/xts,
// IEEE 1619) in 512-byte sectors, the cipher mode of disk encryption. Only the write-ahead log index (-shm) is not encrypted:
// it holds page numbers and checksums, never content. The key is random and stored only sealed by a KeyProvider (keys.go).
//
// The VFS rewrites a sector that a write only partly covers, and write-ahead log frames do not line up with sectors, so a
// reader on another connection or in another process could read a sector half rewritten and decrypt noise. Since
// github.com/ncruces/go-sqlite3 v0.37.0 the VFS holds locks shared through the write-ahead log index around every partial
// sector read and write, which keeps that from happening; the history must not use an earlier version of it.
//
// XTS hides the content from anyone without the key, including copies and backups of the file. It is deterministic and has
// no authentication: comparing two copies shows which sectors changed, and a modified file is not detected as such (SQLite
// may still report it as corrupt). Any process of the same Windows user can unseal a DPAPI key, so encryption at rest does
// not protect the history from software that runs as that user.
//
// A plaintext file that a build before encryption wrote is converted when the store opens: copied into a new encrypted
// file with VACUUM INTO, verified, and put in place of the plaintext file in one atomic move; its plaintext pre-migration
// copies are converted the same way. The plaintext sectors are not overwritten. The file system may keep them in free
// space until it reuses that space, and an SSD may keep them longer, so the conversion is not a secure erase.

const encryptionName = "aes-256-xts"

// encryptionVFS is the SQLite VFS that encrypts the history. Tests replace it to run the store over a VFS that tears
// writes, as an operating system may when a read overlaps a write of the same bytes.
var encryptionVFS = "xts"

// sqliteHeader starts every plaintext SQLite database file.
var sqliteHeader = []byte("SQLite format 3\x00")

var errKeyRejected = errors.New("the history key was rejected")

// fileURI is the SQLite URI of path. The Go VFS opens the path as written, so a Windows path keeps its drive letter without
// a leading slash.
func fileURI(path string, query url.Values) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	uri := "file:" + (&url.URL{Path: filepath.ToSlash(absolute)}).EscapedPath()
	if len(query) > 0 {
		uri += "?" + query.Encode()
	}
	return uri
}

// openEncrypted opens an encrypted history database. Each connection receives the key by PRAGMA before it reads anything,
// never through its URI, and every pragma the store relies on follows the key.
func openEncrypted(path string, key []byte, readOnly bool) (*sql.DB, error) {
	query := url.Values{"vfs": {encryptionVFS}, "_txlock": {"immediate"}}
	pragmas := "PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON; PRAGMA synchronous=NORMAL; PRAGMA temp_store=MEMORY; PRAGMA trusted_schema=OFF"
	if readOnly {
		query.Set("mode", "ro")
		pragmas = "PRAGMA busy_timeout=5000; PRAGMA query_only=1; PRAGMA temp_store=MEMORY; PRAGMA trusted_schema=OFF"
	}
	hexKey := hex.EncodeToString(key)
	return driver.Open(fileURI(path, query), func(conn *sqlite3.Conn) error {
		if err := conn.Exec("PRAGMA hexkey='" + hexKey + "'"); err != nil {
			return errKeyRejected
		}
		return conn.Exec(pragmas)
	})
}

// openPlain opens a plaintext SQLite file without encryption, only to read a file written before encryption.
func openPlain(path string, readOnly bool) (*sql.DB, error) {
	query := url.Values{"_txlock": {"immediate"}}
	if readOnly {
		query.Set("mode", "ro")
	}
	return driver.Open(fileURI(path, query), func(conn *sqlite3.Conn) error {
		return conn.Exec("PRAGMA busy_timeout=5000; PRAGMA temp_store=MEMORY; PRAGMA trusted_schema=OFF")
	})
}

// encryptedURI names an encrypted file as the target of VACUUM INTO, which opens the target itself: the key travels in the
// URI of that one statement, inside this process only.
func encryptedURI(path string, key []byte) string {
	return fileURI(path, url.Values{"vfs": {encryptionVFS}, "hexkey": {hex.EncodeToString(key)}})
}

// fileKind is what a file on disk is, judged from its first bytes.
type fileKind int

const (
	fileAbsent fileKind = iota
	fileEmpty
	filePlain
	fileOpaque
)

func inspectFile(path string) (fileKind, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileAbsent, nil
	}
	if err != nil {
		return fileAbsent, err
	}
	defer file.Close()
	header := make([]byte, len(sqliteHeader))
	n, err := io.ReadFull(file, header)
	switch {
	case n == 0 && errors.Is(err, io.EOF):
		return fileEmpty, nil
	case err != nil && !errors.Is(err, io.ErrUnexpectedEOF):
		return fileAbsent, err
	case n == len(sqliteHeader) && bytes.Equal(header, sqliteHeader):
		return filePlain, nil
	}
	return fileOpaque, nil
}

// isNotADatabase reports SQLite's answer to a file it cannot read as a database: with encryption, a key that does not
// decrypt it, or a file that is not an encrypted history at all.
func isNotADatabase(err error) bool {
	return errors.Is(err, sqlite3.NOTADB)
}

// Reasons a plaintext conversion failed; the plaintext file is unchanged in every case.
const (
	MigrationInsufficientSpace = "insufficient_space"
	MigrationInUse             = "file_in_use"
	MigrationVerifyFailed      = "verification_failed"
	MigrationCopyFailed        = "copy_failed"
	MigrationUnreadable        = "unreadable"
)

// migrationError is a failed conversion with the stable reason the UI shows.
type migrationError struct {
	reason string
	err    error
}

func (e *migrationError) Error() string {
	return "encrypt plaintext history: " + e.reason + ": " + e.err.Error()
}

func (e *migrationError) Unwrap() error { return e.err }

func migrationFailure(reason string, err error) error {
	return &migrationError{reason: reason, err: err}
}

// spaceMargin is the free space a conversion keeps beyond the copy itself.
const spaceMargin = 16 << 20

// availableSpace reads the free space of a volume; tests replace it to simulate a full disk.
var availableSpace = freeSpace

// encryptPlaintextFile replaces the plaintext SQLite file at path with an encrypted copy. The caller holds the writer lock,
// so no other WWlogs writes the file; a reader still holding it makes the conversion fail as in use. Nothing replaces the
// plaintext file until the copy has passed integrity, foreign key, identity and row count checks; a failure removes the copy
// and leaves the plaintext file as it was. An interrupted run leaves only "<path>.encrypting", which the next run removes.
func encryptPlaintextFile(ctx context.Context, path string, key []byte) error {
	temp := path + ".encrypting"
	if err := removeIfExists(temp); err != nil {
		return migrationFailure(MigrationCopyFailed, err)
	}
	source, err := openPlain(path, false)
	if err != nil {
		return migrationFailure(MigrationUnreadable, err)
	}
	closed := false
	defer func() {
		if !closed {
			source.Close()
		}
	}()
	// Opening read-write lets SQLite recover the write-ahead log; the checkpoint folds it into the file the copy reads.
	if _, err := source.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return migrationFailure(MigrationInUse, err)
	}
	if err := quickCheck(ctx, source); err != nil {
		return migrationFailure(MigrationUnreadable, err)
	}
	if err := requireSpace(path); err != nil {
		return err
	}
	if _, err := source.ExecContext(ctx, "VACUUM INTO ?", encryptedURI(temp, key)); err != nil {
		_ = os.Remove(temp)
		return migrationFailure(MigrationCopyFailed, err)
	}
	if err := syncFile(temp); err != nil {
		_ = os.Remove(temp)
		return migrationFailure(MigrationCopyFailed, err)
	}
	if err := verifyCopy(ctx, source, temp, key); err != nil {
		_ = os.Remove(temp)
		return migrationFailure(MigrationVerifyFailed, err)
	}
	closed = true
	if err := source.Close(); err != nil {
		_ = os.Remove(temp)
		return migrationFailure(MigrationInUse, err)
	}
	// SQLite removes the write-ahead log and its index when the last connection closes; one that remains belongs to a reader
	// still holding the plaintext file, and the encrypted file must never inherit it.
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		_ = os.Remove(temp)
		return migrationFailure(MigrationInUse, errors.New("the plaintext write-ahead log is still in use"))
	}
	for _, leftover := range []string{path + "-wal", path + "-shm"} {
		if err := removeIfExists(leftover); err != nil {
			_ = os.Remove(temp)
			return migrationFailure(MigrationInUse, err)
		}
	}
	if err := replaceFile(temp, path); err != nil {
		_ = os.Remove(temp)
		return migrationFailure(MigrationCopyFailed, err)
	}
	return nil
}

// requireSpace refuses a conversion that could fill the volume: the copy needs at most the source's size.
func requireSpace(path string) error {
	var need uint64 = spaceMargin
	for _, file := range []string{path, path + "-wal"} {
		if info, err := os.Stat(file); err == nil {
			need += uint64(info.Size())
		}
	}
	available, err := availableSpace(path)
	if err != nil {
		return migrationFailure(MigrationInsufficientSpace, err)
	}
	if available < need {
		return migrationFailure(MigrationInsufficientSpace, fmt.Errorf("%d bytes free, %d needed", available, need))
	}
	return nil
}

// verifyCopy checks the encrypted copy against its source: SQLite's integrity and foreign key checks, the same
// application_id and user_version, and the same row count in every table.
func verifyCopy(ctx context.Context, source *sql.DB, copyPath string, key []byte) error {
	target, err := openEncrypted(copyPath, key, true)
	if err != nil {
		return err
	}
	defer target.Close()
	var check string
	if err := target.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
		return errors.Join(errors.New("the encrypted copy failed its integrity check"), err)
	}
	rows, err := target.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	violation := rows.Next()
	rows.Close()
	if violation {
		return errors.New("the encrypted copy has foreign key violations")
	}
	want, err := readIdentity(ctx, source)
	if err != nil {
		return err
	}
	got, err := readIdentity(ctx, target)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("the encrypted copy has identity %+v, the source %+v", got, want)
	}
	tables, err := tableNames(ctx, source)
	if err != nil {
		return err
	}
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		var a, b int64
		if err := source.QueryRowContext(ctx, "SELECT count(*) FROM "+quoted).Scan(&a); err != nil {
			return err
		}
		if err := target.QueryRowContext(ctx, "SELECT count(*) FROM "+quoted).Scan(&b); err != nil {
			return err
		}
		if a != b {
			return fmt.Errorf("table %s has %d rows in the copy and %d in the source", table, b, a)
		}
	}
	return nil
}

func tableNames(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// plaintextBackups lists the pre-migration copies of path that are still plaintext.
func plaintextBackups(path string) []string {
	matches, _ := filepath.Glob(path + ".schema-*.bak")
	var plain []string
	for _, match := range matches {
		if kind, err := inspectFile(match); err == nil && kind == filePlain {
			plain = append(plain, match)
		}
	}
	return plain
}

// backupEncrypted writes an encrypted copy of the open database to path with the same key: a pre-migration copy never exists
// in plaintext.
func backupEncrypted(ctx context.Context, db *sql.DB, path string, key []byte) error {
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", encryptedURI(path, key)); err != nil {
		_ = os.Remove(path)
		return err
	}
	return syncFile(path)
}
