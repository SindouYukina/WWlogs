package history

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"

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
// The history is never written unencrypted: a plaintext SQLite file at the history path is not a history, and the store
// leaves it untouched (Store.open).
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
