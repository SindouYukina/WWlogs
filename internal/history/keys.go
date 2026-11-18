package history

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The key of a WWlogs v0.7.0 history.
//
// WWlogs v0.7.0 and earlier gave every history 64 random bytes from crypto/rand as its key and stored them only sealed by a
// KeyProvider (DPAPI for the current Windows user), in a key file next to the history file. No history gets such a key any
// more: a new history derives its key (keyderive.go), and a v0.7.0 history is opened with its sealed key only to be moved
// to a derived key. The key file is never written or replaced here; a key that cannot be unsealed leaves the history
// unreadable rather than overwritten.

// KeyProvider is what sealed the key of a v0.7.0 history. The Windows provider uses DPAPI for the current user
// (keys_windows.go). WWlogs only unseals with it now; nothing seals a new key.
type KeyProvider interface {
	// ID names the provider in the key file; a key file sealed by another provider is refused.
	ID() string
	Seal(key []byte) ([]byte, error)
	Unseal(sealed []byte) ([]byte, error)
}

// historyKeySize is the AES-256-XTS key length.
const historyKeySize = 64

// keyFileMagic starts a history key file, version 1: magic, provider ID length (1 byte), provider ID, sealed key length
// (4 bytes, little endian), sealed key.
const keyFileMagic = "WWLHKEY1"

// maxSealedKey bounds the sealed key a key file may declare.
const maxSealedKey = 1 << 16

var (
	errKeyMissing     = errors.New("the history key file is missing for an existing history file")
	errKeyUnavailable = errors.New("the history key cannot be unsealed")
)

// keyPath is the key file of a history file: combat-history.db keeps its key in combat-history.key.
func keyPath(historyPath string) string {
	return strings.TrimSuffix(historyPath, filepath.Ext(historyPath)) + ".key"
}

// loadKey unseals the key of the v0.7.0 history at path. A missing key file is errKeyMissing: no new key is ever made.
func loadKey(path string, provider KeyProvider) ([]byte, error) {
	data, err := os.ReadFile(keyPath(path))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, errKeyMissing
	case err != nil:
		return nil, fmt.Errorf("%w: %v", errKeyUnavailable, err)
	}
	id, sealed, err := parseKeyFile(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errKeyUnavailable, err)
	}
	if id != provider.ID() {
		return nil, fmt.Errorf("%w: sealed by %q, not %q", errKeyUnavailable, id, provider.ID())
	}
	key, err := provider.Unseal(sealed)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errKeyUnavailable, err)
	}
	if len(key) != historyKeySize {
		clear(key)
		return nil, fmt.Errorf("%w: the unsealed key has %d bytes", errKeyUnavailable, len(key))
	}
	return key, nil
}

func parseKeyFile(data []byte) (string, []byte, error) {
	if !bytes.HasPrefix(data, []byte(keyFileMagic)) || len(data) < len(keyFileMagic)+1 {
		return "", nil, errors.New("not a history key file")
	}
	rest := data[len(keyFileMagic):]
	idLength := int(rest[0])
	rest = rest[1:]
	if len(rest) < idLength+4 {
		return "", nil, errors.New("truncated history key file")
	}
	id := string(rest[:idLength])
	rest = rest[idLength:]
	sealedLength := binary.LittleEndian.Uint32(rest[:4])
	rest = rest[4:]
	if sealedLength == 0 || sealedLength > maxSealedKey || uint32(len(rest)) != sealedLength {
		return "", nil, errors.New("malformed history key file")
	}
	return id, rest, nil
}
